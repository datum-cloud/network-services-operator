package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// BuildPolicyIndexFromClient constructs the per-call in-memory policy index
// from the warm informer cache backed by cl. All reads hit the informer cache
// — no API server round-trips during hook processing.
//
// The extension server runs at the edge, co-located with Envoy Gateway. NSO
// replicates TrafficProtectionPolicy, HTTPProxy, and Connector resources into
// the edge cluster's downstream ns-<uid> namespaces, so the local client has
// everything needed — no upstream control-plane connectivity is required.
//
// baseDirectives are the RouteBaseDirectives from the operator config;
// they are prepended to every policy's per-rule directive list.
func BuildPolicyIndexFromClient(ctx context.Context, cl client.Client, baseDirectives []string) (*PolicyIndex, error) {
	idx := &PolicyIndex{
		DStoUS:           make(map[string]string),
		TenantNamespaces: make(map[string]struct{}),
		ProjectNames:     make(map[string]string),
		TPPs:             make(map[string][]TPPInfo),
		HTTPProxyRules:   make(map[HTTPProxyKey][]string),
		Connectors:       make(map[ConnectorKey]ConnectorInfo),
		VPCPods:          make(map[VPCPodKey]VPCPodInfo),
		VPCPodBackends:   make(map[VPCPodBackendKey]VPCPodInfo),
	}
	if err := populateFromClient(ctx, cl, idx, baseDirectives); err != nil {
		return nil, err
	}
	return idx, nil
}

// populateFromClient accumulates policy data from a single client into idx.
// It is the shared implementation called by BuildPolicyIndexFromClient (single
// cluster) and — in test scenarios — directly by index tests that simulate
// multi-cluster accumulation by calling it once per fake cluster client.
//
// Three list operations + per-proxy Connector Get calls. All reads are served
// from the informer cache when the client is cache-backed.
func populateFromClient(ctx context.Context, cl client.Client, idx *PolicyIndex, baseDirectives []string) error {
	// --- Downstream → upstream namespace map ---
	var nsList corev1.NamespaceList
	if err := cl.List(ctx, &nsList); err != nil {
		return fmt.Errorf("list Namespaces: %w", err)
	}
	namespaceLabels := make(map[string]map[string]string, len(nsList.Items))
	for _, ns := range nsList.Items {
		namespaceLabels[ns.Name] = ns.Labels
		if upstreamName := ns.Labels[downstreamclient.UpstreamOwnerNamespaceLabel]; upstreamName != "" {
			// Two-cluster edge topology (GAP-1b fix): replica namespaces carry
			// UpstreamOwnerNamespaceLabel stamped by mappednamespace.go. Key
			// DStoUS by ns.Name (the replica namespace name, which is exactly the
			// dsNS value in EG VH filter_metadata) and resolve to the true
			// upstream namespace name from the label — no UID arithmetic needed.
			idx.DStoUS[ns.Name] = upstreamName
			idx.TenantNamespaces[ns.Name] = struct{}{}
		} else {
			// Single-cluster (no namespace mapping in play): EG and the Gateway
			// resource live in the same cluster. EG puts the plain namespace name
			// in filter_metadata, so dsNS == ns.Name. Identity entry.
			idx.DStoUS[ns.Name] = ns.Name
		}

		// Derive project name from the upstream cluster name label. The label
		// value is "cluster-<projectName>" (written by mappednamespace.go). In
		// single-cluster deployments where no cluster name is configured the
		// label is absent and ProjectNames[ns.Name] is left empty.
		if clusterLabel := ns.Labels[downstreamclient.UpstreamOwnerClusterNameLabel]; clusterLabel != "" {
			idx.ProjectNames[ns.Name] = strings.TrimPrefix(clusterLabel, "cluster-")
		}
	}

	// --- TrafficProtectionPolicies ---
	var tppList networkingv1alpha.TrafficProtectionPolicyList
	if err := cl.List(ctx, &tppList); err != nil {
		return fmt.Errorf("list TrafficProtectionPolicies: %w", err)
	}
	// Sort for stable precedence: older creation timestamp wins; ties broken
	// by name. This matches the NSO reconciler's policy ordering.
	sort.Slice(tppList.Items, func(i, j int) bool {
		ti := tppList.Items[i].CreationTimestamp
		tj := tppList.Items[j].CreationTimestamp
		if !ti.Equal(&tj) {
			return ti.Before(&tj)
		}
		return tppList.Items[i].Name < tppList.Items[j].Name
	})
	for i := range tppList.Items {
		tpp := &tppList.Items[i]
		// Index policies by their downstream replica namespace. Replica namespace
		// names are unique across projects on an edge; upstream project
		// namespaces are not (they are commonly all "default"), so indexing by
		// the upstream namespace label would let policies collide across tenants.
		info := TPPInfo{
			Namespace:  tpp.Namespace,
			Name:       tpp.Name,
			Generation: tpp.Generation,
			Mode:       tpp.Spec.Mode,
			TargetRefs: tpp.Spec.TargetRefs,
			Directives: computeCorazaDirectives(tpp, baseDirectives),
		}
		idx.TPPs[tpp.Namespace] = append(idx.TPPs[tpp.Namespace], info)
	}

	// --- EndpointSlices → VRF-binding lookups ---
	var sliceList discoveryv1.EndpointSliceList
	if err := cl.List(ctx, &sliceList); err != nil {
		return fmt.Errorf("list EndpointSlices: %w", err)
	}
	lookups := newVPCLookups(&sliceList, namespaceLabels)

	// --- HTTPProxies → ConnectorInfo ---
	var proxyList networkingv1alpha.HTTPProxyList
	if err := cl.List(ctx, &proxyList); err != nil {
		return fmt.Errorf("list HTTPProxies: %w", err)
	}
	for i := range proxyList.Items {
		proxy := &proxyList.Items[i]
		// Key every per-rule lookup by proxy.Namespace, the replica namespace
		// this HTTPProxy lives in on the edge, consistent with TPP indexing
		// above. The upstream namespace label is deliberately not used: it is
		// "default" for every project, so two projects with an HTTPProxy of the
		// same name would overwrite each other's entry.
		ruleNames := make([]string, len(proxy.Spec.Rules))
		for i, rule := range proxy.Spec.Rules {
			if rule.Name != nil {
				ruleNames[i] = string(*rule.Name)
			}
		}
		idx.HTTPProxyRules[HTTPProxyKey{Namespace: proxy.Namespace, Name: proxy.Name}] = ruleNames
		for ruleIndex, rule := range proxy.Spec.Rules {
			var ruleTenants []tenantResolution
			for backendIndex, backend := range rule.Backends {
				backendKey := VPCPodBackendKey{
					Namespace:     proxy.Namespace,
					HTTPProxyName: proxy.Name,
					RuleIndex:     ruleIndex,
					BackendIndex:  backendIndex,
				}
				switch {
				case backend.Connector != nil:
					targetHost, targetPort, err := parseEndpoint(backend.Endpoint)
					if err != nil {
						// Skip invalid endpoints; proxy admission should have caught them.
						continue
					}

					key := ConnectorKey{
						Namespace:     proxy.Namespace,
						HTTPProxyName: proxy.Name,
						RuleIndex:     ruleIndex,
					}

					var connector networkingv1alpha1.Connector
					if lookupErr := cl.Get(ctx, client.ObjectKey{
						Namespace: proxy.Namespace,
						Name:      backend.Connector.Name,
					}, &connector); lookupErr != nil {
						// Connector missing or transient error; treat as offline.
						idx.Connectors[key] = ConnectorInfo{
							Online:     false,
							TargetHost: targetHost,
							TargetPort: targetPort,
						}
						continue
					}

					online, nodeID := connectorLiveness(&connector)

					idx.Connectors[key] = ConnectorInfo{
						Online:     online,
						TargetHost: targetHost,
						TargetPort: targetPort,
						NodeID:     nodeID,
					}

				case backend.Instance != nil, backend.NetworkService != nil:
					resolved := backendTenant(ctx, cl, proxy, ruleIndex, backendIndex, lookups)
					idx.VPCPodBackends[backendKey] = resolved.info()
					ruleTenants = append(ruleTenants, resolved)
				}
			}

			if len(ruleTenants) > 0 {
				idx.VPCPods[VPCPodKey{
					Namespace:     proxy.Namespace,
					HTTPProxyName: proxy.Name,
					RuleIndex:     ruleIndex,
				}] = mergeTenants(ruleTenants).info()
			}
		}
	}
	return nil
}

func backendTenant(
	ctx context.Context,
	cl client.Client,
	proxy *networkingv1alpha.HTTPProxy,
	ruleIndex, backendIndex int,
	lookups vpcLookups,
) tenantResolution {
	backend := proxy.Spec.Rules[ruleIndex].Backends[backendIndex]
	switch {
	case backend.Instance != nil:
		var endpointSlice discoveryv1.EndpointSlice
		if err := cl.Get(ctx, client.ObjectKey{
			Namespace: proxy.Namespace,
			Name:      backend.Instance.Name,
		}, &endpointSlice); err != nil {
			return tenantResolution{}
		}
		tenantID := endpointSlice.Labels[VPCPodTenantIDLabel]
		if tenantID != "" && !trustedTenantSlice(&endpointSlice, lookups.namespaceLabels[proxy.Namespace]) {
			return tenantResolution{untrusted: true}
		}
		return tenantResolution{tenantID: tenantID}
	case backend.NetworkService != nil:
		return lookups.tenantForAddresses(proxy.Namespace, lookups.addressesByOwner[NetworkServiceBackendSliceKey(proxy, ruleIndex, backendIndex)])
	}
	return tenantResolution{}
}

// NetworkServiceBackendSliceKey names the EndpointSlice the HTTPProxy
// controller synthesizes for a networkService backend, as its edge copy
// records it in UpstreamOwnerNameLabel.
func NetworkServiceBackendSliceKey(proxy *networkingv1alpha.HTTPProxy, ruleIndex, backendIndex int) client.ObjectKey {
	return client.ObjectKey{
		Namespace: proxy.Namespace,
		Name:      fmt.Sprintf("%s-%d-%d", proxy.Name, ruleIndex, backendIndex),
	}
}

// VPCBackendTenants returns the tenant each VPC backend of proxy resolves to,
// keyed by backend, exactly as BuildPolicyIndexFromClient resolves them. A
// proxy with no VPC backend returns an empty map.
func VPCBackendTenants(ctx context.Context, cl client.Client, proxy *networkingv1alpha.HTTPProxy) (map[VPCPodBackendKey]string, error) {
	tenants := map[VPCPodBackendKey]string{}
	if !HasVPCBackend(proxy) {
		return tenants, nil
	}

	var namespace corev1.Namespace
	if err := cl.Get(ctx, client.ObjectKey{Name: proxy.Namespace}, &namespace); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get Namespace %s: %w", proxy.Namespace, err)
	}
	var sliceList discoveryv1.EndpointSliceList
	if err := cl.List(ctx, &sliceList, client.InNamespace(proxy.Namespace)); err != nil {
		return nil, fmt.Errorf("list EndpointSlices: %w", err)
	}
	lookups := newVPCLookups(&sliceList, map[string]map[string]string{proxy.Namespace: namespace.Labels})

	for ruleIndex, rule := range proxy.Spec.Rules {
		for backendIndex, backend := range rule.Backends {
			if backend.Instance == nil && backend.NetworkService == nil {
				continue
			}
			key := VPCPodBackendKey{
				Namespace:     proxy.Namespace,
				HTTPProxyName: proxy.Name,
				RuleIndex:     ruleIndex,
				BackendIndex:  backendIndex,
			}
			tenants[key] = backendTenant(ctx, cl, proxy, ruleIndex, backendIndex, lookups).tenantID
		}
	}
	return tenants, nil
}

// HasVPCBackend reports whether any backend of proxy reaches a VPC workload.
func HasVPCBackend(proxy *networkingv1alpha.HTTPProxy) bool {
	for _, rule := range proxy.Spec.Rules {
		for _, backend := range rule.Backends {
			if backend.Instance != nil || backend.NetworkService != nil {
				return true
			}
		}
	}
	return false
}

func trustedTenantSlice(slice *discoveryv1.EndpointSlice, nsLabels map[string]string) bool {
	for _, owner := range slice.OwnerReferences {
		if owner.APIVersion == "v1" && owner.Kind == "Pod" {
			return true
		}
	}
	if slice.Labels[VPCEndpointSliceProjectionLabel] != "true" {
		return false
	}
	clusterName := nsLabels[downstreamclient.UpstreamOwnerClusterNameLabel]
	return clusterName != "" && slice.Labels[downstreamclient.UpstreamOwnerClusterNameLabel] == clusterName
}

type namespacedAddress struct {
	namespace string
	address   string
}

type vpcLookups struct {
	namespaceLabels  map[string]map[string]string
	tenantByAddress  map[namespacedAddress]tenantResolution
	addressesByOwner map[client.ObjectKey][]string
}

func newVPCLookups(sliceList *discoveryv1.EndpointSliceList, namespaceLabels map[string]map[string]string) vpcLookups {
	lookups := vpcLookups{
		namespaceLabels:  namespaceLabels,
		tenantByAddress:  make(map[namespacedAddress]tenantResolution),
		addressesByOwner: make(map[client.ObjectKey][]string),
	}

	for i := range sliceList.Items {
		slice := &sliceList.Items[i]
		tenantID := slice.Labels[VPCPodTenantIDLabel]
		claim := tenantResolution{tenantID: tenantID}
		if tenantID != "" && !trustedTenantSlice(slice, namespaceLabels[slice.Namespace]) {
			claim = tenantResolution{untrusted: true}
		}
		owner := slice.Labels[downstreamclient.UpstreamOwnerNameLabel]
		if tenantID == "" && owner == "" {
			continue
		}

		ownerKey := client.ObjectKey{Namespace: slice.Namespace, Name: owner}
		for _, endpoint := range slice.Endpoints {
			for _, rawAddress := range endpoint.Addresses {
				address := canonicalAddress(rawAddress)
				if address == "" {
					continue
				}
				if tenantID != "" {
					key := namespacedAddress{namespace: slice.Namespace, address: address}
					merged := claim
					if existing, ok := lookups.tenantByAddress[key]; ok {
						merged = mergeTenants([]tenantResolution{existing, claim})
					}
					lookups.tenantByAddress[key] = merged
				}
				if owner != "" {
					lookups.addressesByOwner[ownerKey] = append(lookups.addressesByOwner[ownerKey], address)
				}
			}
		}
	}

	return lookups
}

// canonicalAddress normalises an endpoint address so two spellings of one
// IPv6 address compare equal. Returns "" for anything unparseable.
func canonicalAddress(address string) string {
	ip := net.ParseIP(address)
	if ip == nil {
		return ""
	}
	return ip.String()
}

// tenantForAddresses resolves the one tenant every member of a networkService
// backend sits behind.
//
// A VRF binding is a property of the whole Envoy cluster rather than of an
// individual endpoint, so a backend whose members span more than one VPC has
// no correct answer and is given none: binding to either VPC's device would
// black-hole the other's members. Members that resolve to no tenant are
// skipped rather than treated as a conflict: a slice that has not federated
// in yet must not unbind the members that have.
func (l vpcLookups) tenantForAddresses(namespace string, addresses []string) tenantResolution {
	candidates := make([]tenantResolution, 0, len(addresses))
	for _, address := range addresses {
		if candidate, ok := l.tenantByAddress[namespacedAddress{namespace: namespace, address: address}]; ok {
			candidates = append(candidates, candidate)
		}
	}
	return mergeTenants(candidates)
}

type tenantResolution struct {
	tenantID  string
	conflict  bool
	untrusted bool
}

func (r tenantResolution) info() VPCPodInfo {
	switch {
	case r.conflict:
		return VPCPodInfo{Refusal: VPCBindingRefusalConflict}
	case r.untrusted:
		return VPCPodInfo{Refusal: VPCBindingRefusalUntrusted}
	case r.tenantID == "":
		return VPCPodInfo{Refusal: VPCBindingRefusalUnresolved}
	}
	return VPCPodInfo{TenantID: r.tenantID}
}

func mergeTenants(candidates []tenantResolution) tenantResolution {
	var vpc string
	var merged tenantResolution
	for _, candidate := range candidates {
		if candidate.conflict {
			return tenantResolution{conflict: true}
		}
		candidateVPC, ok := TenantVPC(candidate.tenantID)
		if !ok {
			merged.untrusted = merged.untrusted || candidate.untrusted
			continue
		}
		if vpc == "" {
			vpc, merged.tenantID = candidateVPC, candidate.tenantID
			continue
		}
		if candidateVPC != vpc {
			return tenantResolution{conflict: true}
		}
	}
	if merged.tenantID != "" {
		merged.untrusted = false
	}
	return merged
}

// connectorLiveness determines whether a connector is online and, if so, its
// tunnel node ID. It prefers the UpstreamStatusAnnotation, which is how a
// Connector's authoritative status reaches edge member clusters: the Ready
// condition and ConnectionDetails are computed in the Project control plane and
// live in the status subresource, but Karmada propagates a resource template's
// spec + metadata (annotations) to members, NOT the status subresource. The
// replicator mirrors the upstream status into the annotation; this reads it
// back and classifies it with the same logic used for live status.
//
// When the annotation is absent or unparseable — single-cluster deployments
// (where the local object carries real status), or member-cluster objects
// written before this rollout — it falls back to the live status, preserving
// the previous behaviour.
func connectorLiveness(connector *networkingv1alpha1.Connector) (online bool, nodeID string) {
	if raw, ok := connector.Annotations[networkingv1alpha1.UpstreamStatusAnnotation]; ok {
		var status networkingv1alpha1.ConnectorStatus
		if err := json.Unmarshal([]byte(raw), &status); err == nil {
			return connectorStatusLiveness(&status)
		}
		// Unparseable annotation: fall through to live-status classification.
	}

	return connectorStatusLiveness(&connector.Status)
}

// ConnectorLiveness reports whether a Connector is online and, if so, its tunnel
// node id, using the same annotation-first classification the extension server
// applies when programming routes. It is exported so callers can detect liveness
// changes consistently with what the hook will program.
func ConnectorLiveness(connector *networkingv1alpha1.Connector) (online bool, nodeID string) {
	return connectorLiveness(connector)
}

// connectorStatusLiveness derives the (online, nodeID) classification from a
// ConnectorStatus, regardless of whether that status came from the mirrored
// upstream-status annotation or the object's live status. Online is the Ready
// condition being True; nodeID is the PublicKey connection id (the only
// connection type defined today), read directly with nil guards.
func connectorStatusLiveness(status *networkingv1alpha1.ConnectorStatus) (online bool, nodeID string) {
	online = apimeta.IsStatusConditionTrue(
		status.Conditions,
		networkingv1alpha1.ConnectorConditionReady,
	)
	if online {
		if details := status.ConnectionDetails; details != nil &&
			details.Type == networkingv1alpha1.PublicKeyConnectorConnectionType &&
			details.PublicKey != nil {
			nodeID = details.PublicKey.Id
		}
	}
	return online, nodeID
}

// computeCorazaDirectives builds the Coraza simple_directives list for a TPP,
// mirroring getCorazaDirectivesForTrafficProtectionPolicy in
// internal/controller/trafficprotectionpolicy_controller.go.
func computeCorazaDirectives(
	tpp *networkingv1alpha.TrafficProtectionPolicy,
	baseDirectives []string,
) []string {
	if tpp.Spec.InvertedParanoiaLevels() != nil {
		return nil
	}

	var owaspCRS *networkingv1alpha.OWASPCRS
	for _, ruleSet := range tpp.Spec.RuleSets {
		if ruleSet.Type == networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet {
			cp := ruleSet.OWASPCoreRuleSet
			owaspCRS = &cp
			break
		}
	}
	if owaspCRS == nil {
		return nil
	}

	secRuleEngine := "DetectionOnly"
	switch tpp.Spec.Mode {
	case networkingv1alpha.TrafficProtectionPolicyEnforce:
		secRuleEngine = "On"
	case networkingv1alpha.TrafficProtectionPolicyDisabled:
		secRuleEngine = "Off"
	}

	directives := make([]string, len(baseDirectives))
	copy(directives, baseDirectives)

	directives = append(directives,
		fmt.Sprintf("SecRuleEngine %s", secRuleEngine),
		fmt.Sprintf(
			`SecAction "id:900110,phase:1,nolog,pass,t:none,setvar:tx.inbound_anomaly_score_threshold=%d,setvar:tx.outbound_anomaly_score_threshold=%d"`,
			owaspCRS.ScoreThresholds.Inbound,
			owaspCRS.ScoreThresholds.Outbound,
		),
		fmt.Sprintf(
			`SecAction "id:900000,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.blocking_paranoia_level=%d"`,
			owaspCRS.ParanoiaLevels.Blocking,
		),
		fmt.Sprintf(
			`SecAction "id:900001,phase:1,pass,t:none,nolog,tag:'OWASP_CRS',setvar:tx.detection_paranoia_level=%d"`,
			owaspCRS.ParanoiaLevels.Detection,
		),
	)

	if tpp.Spec.SamplingPercentage > 0 && tpp.Spec.SamplingPercentage < 100 {
		directives = append(directives,
			fmt.Sprintf(
				`SecAction "id:900400,phase:1,pass,nolog,setvar:tx.sampling_percentage=%d"`,
				tpp.Spec.SamplingPercentage,
			),
		)
	}

	directives = append(directives, "Include @owasp_crs/*.conf")

	if ruleExclusions := owaspCRS.RuleExclusions; ruleExclusions != nil {
		for _, tag := range ruleExclusions.Tags {
			directives = append(directives, fmt.Sprintf("SecRuleRemoveByTag %q", tag))
		}
		for _, v := range ruleExclusions.IDs {
			directives = append(directives, fmt.Sprintf("SecRuleRemoveById %d", v))
		}
		for _, v := range ruleExclusions.IDRanges {
			directives = append(directives, fmt.Sprintf("SecRuleRemoveById %q", v))
		}
	}

	return directives
}

// parseEndpoint extracts the hostname and port from a backend endpoint URL,
// mirroring backendEndpointTarget in internal/controller/httpproxy_controller.go.
func parseEndpoint(endpoint string) (string, int, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", 0, fmt.Errorf("parse endpoint %q: %w", endpoint, err)
	}
	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("endpoint %q has no hostname", endpoint)
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		if err != nil {
			return "", 0, fmt.Errorf("endpoint %q has invalid port: %w", endpoint, err)
		}
	}
	return host, port, nil
}

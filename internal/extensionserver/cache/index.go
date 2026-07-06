package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"

	corev1 "k8s.io/api/core/v1"
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
		DStoUS:     make(map[string]string),
		TPPs:       make(map[string][]TPPInfo),
		Connectors: make(map[ConnectorKey]ConnectorInfo),
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
	for _, ns := range nsList.Items {
		if upstreamName := ns.Labels[downstreamclient.UpstreamOwnerNamespaceLabel]; upstreamName != "" {
			// Two-cluster edge topology (GAP-1b fix): replica namespaces carry
			// UpstreamOwnerNamespaceLabel stamped by mappednamespace.go. Key
			// DStoUS by ns.Name (the replica namespace name, which is exactly the
			// dsNS value in EG VH filter_metadata) and resolve to the true
			// upstream namespace name from the label — no UID arithmetic needed.
			idx.DStoUS[ns.Name] = upstreamName
		} else {
			// Single-cluster (no namespace mapping in play): EG and the Gateway
			// resource live in the same cluster. EG puts the plain namespace name
			// in filter_metadata, so dsNS == ns.Name. Identity entry.
			idx.DStoUS[ns.Name] = ns.Name
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
		// Resolve the effective upstream namespace for indexing. In the two-cluster
		// edge topology, replica TPPs carry UpstreamOwnerNamespaceLabel pointing to
		// the true upstream namespace name (matching the DStoUS value resolved above).
		// In single-cluster (no label), fall back to tpp.Namespace which is the
		// upstream namespace name directly.
		effectiveNS := tpp.Labels[downstreamclient.UpstreamOwnerNamespaceLabel]
		if effectiveNS == "" {
			effectiveNS = tpp.Namespace
		}
		info := TPPInfo{
			Namespace:  tpp.Namespace,
			Name:       tpp.Name,
			Mode:       tpp.Spec.Mode,
			TargetRefs: tpp.Spec.TargetRefs,
			Directives: computeCorazaDirectives(tpp, baseDirectives),
		}
		idx.TPPs[effectiveNS] = append(idx.TPPs[effectiveNS], info)
	}

	// --- HTTPProxies → ConnectorInfo ---
	var proxyList networkingv1alpha.HTTPProxyList
	if err := cl.List(ctx, &proxyList); err != nil {
		return fmt.Errorf("list HTTPProxies: %w", err)
	}
	for i := range proxyList.Items {
		proxy := &proxyList.Items[i]
		// Resolve the effective upstream namespace for the ConnectorKey, consistent
		// with TPP indexing above. In two-cluster replica HTTPProxies carry
		// UpstreamOwnerNamespaceLabel; in single-cluster fall back to proxy.Namespace.
		effectiveNS := proxy.Labels[downstreamclient.UpstreamOwnerNamespaceLabel]
		if effectiveNS == "" {
			effectiveNS = proxy.Namespace
		}
		for ruleIndex, rule := range proxy.Spec.Rules {
			for _, backend := range rule.Backends {
				if backend.Connector == nil {
					continue
				}

				targetHost, targetPort, err := parseEndpoint(backend.Endpoint)
				if err != nil {
					// Skip invalid endpoints; proxy admission should have caught them.
					continue
				}

				key := ConnectorKey{
					UpstreamNS:    effectiveNS,
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
			}
		}
	}
	return nil
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

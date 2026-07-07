// Package cache provides the informer-backed cache layer for the extension
// server. It exposes a per-request PolicyIndex built from upstream cluster
// watches of TrafficProtectionPolicy, HTTPProxy, Connector, and Namespace
// across all Milo-discovered project clusters.
package cache

import (
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// PolicyIndex is the per-request aggregation of all policy data needed to
// drive the PostTranslateModify mutation families. It is built at the top of
// every PostTranslateModify call from the warm informer cache — no API
// round-trips during hook processing.
//
// Keys use upstream namespace names (not cluster-qualified names) because in
// Datum's Milo architecture project namespace names are globally unique across
// clusters (they are derived from project identifiers). BuildPolicyIndex
// merges data from all engaged clusters into this flat structure.
type PolicyIndex struct {
	// DStoUS maps the downstream namespace name (as it appears in EG VH
	// filter_metadata) to the upstream namespace name used to key TPPs and
	// Connectors. Two resolution strategies:
	//
	//   Two-cluster edge (production): replica namespaces carry
	//   meta.datumapis.com/upstream-namespace stamped by mappednamespace.go.
	//   Entry: ns.Name → label-value (the true upstream namespace name).
	//   EG puts the replica namespace name in filter_metadata; the label
	//   resolves it to the upstream name without any UID arithmetic.
	//
	//   Single-cluster (no namespace mapping): EG and the Gateway live in the
	//   same cluster. EG puts the plain namespace name in filter_metadata so
	//   dsNS == ns.Name. Identity entry: ns.Name → ns.Name.
	//
	// Values are upstream namespace names; they key into TPPs and Connectors.
	DStoUS map[string]string

	// ProjectNames maps downstream namespace names to the human-readable
	// project name for that namespace. Derived from
	// meta.datumapis.com/upstream-cluster-name on replica namespaces (value
	// format: "cluster-<projectName>"). Empty string when the label is absent
	// (e.g. single-cluster dev with no cluster name configured).
	//
	// TODO: replace with resourcemanager.miloapis.com/project-name once that
	// label is available on project namespaces.
	ProjectNames map[string]string

	// TPPs maps upstream namespace names to the list of
	// TrafficProtectionPolicies in that namespace, sorted by creation
	// timestamp then name to match NSO reconciler precedence order.
	// Accumulated across all engaged clusters.
	TPPs map[string][]TPPInfo

	// Connectors maps (upstreamNS, httpProxyName, ruleIndex) to ConnectorInfo.
	// Only populated for HTTPProxy rules that have a Connector backend.
	// Accumulated across all engaged clusters.
	Connectors map[ConnectorKey]ConnectorInfo
}

// TPPInfo holds the fields of a TrafficProtectionPolicy needed by the
// mutation layer to inject Coraza WAF config.
type TPPInfo struct {
	Namespace  string
	Name       string
	Mode       networkingv1alpha.TrafficProtectionPolicyMode
	TargetRefs []v1alpha2.LocalPolicyTargetReferenceWithSectionName
	// Directives is the pre-computed list of Coraza simple_directives for
	// this policy. It combines the operator's RouteBaseDirectives with the
	// per-policy OWASP CRS settings derived from the TPP spec.
	Directives []string
}

// ConnectorInfo holds the fields needed to mutate connector clusters and routes.
type ConnectorInfo struct {
	// Online is true when the connector's Ready condition is True.
	Online bool
	// TargetHost is the hostname parsed from the HTTPProxy backend endpoint URL.
	// Used as the tunnel address host and appended to VirtualHost domains for
	// online connectors (production behavior per design §2.3, conflict C1).
	TargetHost string
	// TargetPort is the port parsed from the HTTPProxy backend endpoint URL.
	TargetPort int
	// NodeID is the connector's public key ID from
	// Connector.Status.ConnectionDetails.PublicKey.Id. Used as endpoint_id
	// in the tunnel filter_metadata.
	NodeID string
}

// ConnectorKey uniquely identifies an HTTPProxy rule that has a Connector
// backend. In Datum's Milo architecture upstream namespace names are globally
// unique, so no cluster qualifier is needed in the key.
type ConnectorKey struct {
	// UpstreamNS is the effective upstream namespace name resolved from the
	// HTTPProxy's UpstreamOwnerNamespaceLabel (two-cluster) or proxy.Namespace
	// (single-cluster). It matches the value stored in DStoUS and the key used
	// for idx.TPPs, keeping WAF and Connector resolution consistent.
	UpstreamNS    string
	HTTPProxyName string
	RuleIndex     int
}

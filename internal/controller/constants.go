// SPDX-License-Identifier: AGPL-3.0-only

package controller

// Kubernetes resource Kind constants used across multiple controllers.
const (
	KindClusterIssuer           = "ClusterIssuer"
	KindGatewayClass            = "GatewayClass"
	KindBackendTLSPolicy        = "BackendTLSPolicy"
	KindTrafficProtectionPolicy = "TrafficProtectionPolicy"
	KindHTTPProxy               = "HTTPProxy"
	KindConnector               = "Connector"
)

// API group constants.
const (
	groupEnvoyGateway      = "gateway.envoyproxy.io"
	groupGatewayNetworking = "gateway.networking.k8s.io"
)

// API version constants.
const (
	versionV1Alpha  = "v1alpha"
	versionV1Alpha1 = "v1alpha1"
)

// Envoy xDS type URL constants.
const (
	routeConfigurationTypeURL = "type.googleapis.com/envoy.config.route.v3.RouteConfiguration"
)

// JSON/map field key constants used in Envoy proxy configuration and condition maps.
const (
	jsonKeyName        = "name"
	jsonKeyNamespace   = "namespace"
	jsonKeyType        = "type"
	jsonKeyTypedConfig = "typed_config"
	jsonKeyAtType      = "@type"
	jsonKeyKind        = "kind"
	jsonKeyMatch       = "match"
	jsonKeyStatus      = "status"
	jsonKeyOwner       = "owner"
	jsonPatchOpAdd     = "add"
)

// DNS record type constants.
const (
	dnsRecordTypeTXT = "TXT"
)

// Condition type constants for unstructured object parsing.
const (
	conditionTypeAccepted   = "Accepted"
	conditionTypeProgrammed = "Programmed"
)

// cert-manager condition status values used when parsing unstructured Certificate objects.
const (
	certManagerConditionStatusTrue = "True"
)

// Label value constants.
const (
	labelValueTrue = "true"
)

// Metric label name constants.
const (
	metricLabelResourceKind = "resource_kind"
)

// API group constants for networking.datumapis.com resources.
const (
	groupNetworkingDatumAPIs = "networking.datumapis.com"
)

// Sync outcome constants used as Prometheus label values.
const (
	syncOutcomeError = "error"
)

// Service ClusterIP constants.
const (
	clusterIPNone = "None"
)

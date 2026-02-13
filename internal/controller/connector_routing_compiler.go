// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"encoding/json"
	"fmt"
	"net/http"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// connectorBackendPatch captures route/backend data used to compile connector Envoy patches.
type connectorBackendPatch struct {
	// Gateway listener section name (default-https, etc.).
	sectionName *gatewayv1.SectionName

	// Identify the HTTPRoute rule/match this backend applies to.
	ruleIndex  int
	matchIndex int

	targetHost string
	targetPort int
	nodeID     string
}

// connectorClusterName returns the cluster name Envoy Gateway assigns to the
// HTTPRoute backend for the given rule. Must match Envoy Gateway's naming so we
// can patch that cluster to point at the internal listener.
func connectorClusterName(downstreamNamespace, httpRouteName string, ruleIndex int) string {
	return fmt.Sprintf("httproute/%s/%s/rule/%d", downstreamNamespace, httpRouteName, ruleIndex)
}

// buildConnectorInternalListenerClusterJSON returns the Envoy cluster config
// JSON that points at the internal listener "connector-tunnel" with endpoint
// metadata (tunnel.address, tunnel.endpoint_id). InternalUpstreamTransport
// copies that metadata to the internal connection so TcpProxy can use
// %DYNAMIC_METADATA(tunnel:address)% and tunnel:endpoint_id for CONNECT.
func buildConnectorInternalListenerClusterJSON(clusterName, internalListenerName string, backend connectorBackendPatch) ([]byte, error) {
	tunnelAddress := fmt.Sprintf("%s:%d", backend.targetHost, backend.targetPort)
	cluster := map[string]any{
		"name":            clusterName,
		"type":            "STATIC",
		"connect_timeout": "5s",
		"load_assignment": map[string]any{
			"cluster_name": clusterName,
			"endpoints": []map[string]any{
				{
					"lb_endpoints": []map[string]any{
						{
							"endpoint": map[string]any{
								"address": map[string]any{
									"envoy_internal_address": map[string]any{
										"server_listener_name": internalListenerName,
									},
								},
							},
							"metadata": map[string]any{
								"filter_metadata": map[string]any{
									"tunnel": map[string]any{
										"address":     tunnelAddress,
										"endpoint_id": backend.nodeID,
									},
								},
							},
						},
					},
				},
			},
		},
		"transport_socket": map[string]any{
			"name": "envoy.transport_sockets.internal_upstream",
			"typed_config": map[string]any{
				"@type": "type.googleapis.com/envoy.extensions.transport_sockets.internal_upstream.v3.InternalUpstreamTransport",
				"passthrough_metadata": []map[string]any{
					{
						"kind": map[string]any{"host": map[string]any{}},
						"name": "tunnel",
					},
				},
				"transport_socket": map[string]any{
					"name": "envoy.transport_sockets.raw_buffer",
					"typed_config": map[string]any{
						"@type": "type.googleapis.com/envoy.extensions.transport_sockets.raw_buffer.v3.RawBuffer",
					},
				},
			},
		},
	}
	return json.Marshal(cluster)
}

func buildConnectorEnvoyPatches(
	downstreamNamespace string,
	internalListenerName string,
	gateway *gatewayv1.Gateway,
	httpProxy *networkingv1alpha.HTTPProxy,
	backends []connectorBackendPatch,
) ([]envoygatewayv1alpha1.EnvoyJSONPatchConfig, error) {
	patches := make([]envoygatewayv1alpha1.EnvoyJSONPatchConfig, 0)
	// Cluster patch (per connector backend): point the route's cluster at the internal
	// listener with endpoint metadata.
	for _, backend := range backends {
		clusterName := connectorClusterName(downstreamNamespace, httpProxy.Name, backend.ruleIndex)
		clusterJSON, err := buildConnectorInternalListenerClusterJSON(clusterName, internalListenerName, backend)
		if err != nil {
			return nil, fmt.Errorf("failed to build connector cluster JSON: %w", err)
		}
		patches = append(patches, envoygatewayv1alpha1.EnvoyJSONPatchConfig{
			Type: "type.googleapis.com/envoy.config.cluster.v3.Cluster",
			Name: clusterName,
			Operation: envoygatewayv1alpha1.JSONPatchOperation{
				Op:    envoygatewayv1alpha1.JSONPatchOperationType("replace"),
				Path:  ptr.To(""),
				Value: &apiextensionsv1.JSON{Raw: clusterJSON},
			},
		})
	}

	// Add each unique tunnel target (host only) to HTTPS listener
	// RouteConfiguration(s) so CONNECT requests with :authority set to that
	// target match the vhost.
	//
	// Current behavior: HTTPProxy connector backends set sectionName=nil, so we
	// patch all HTTPS listeners.
	//
	// Future extension: when sectionName is populated from route attachment
	// context, patch only that specific HTTPS listener's RouteConfiguration.
	allHTTPSRouteConfigNames := gatewayHTTPSRouteConfigNames(downstreamNamespace, gateway)
	seenDomainRouteConfig := make(map[string]struct{})
	for _, backend := range backends {
		domain := backend.targetHost
		routeConfigNames := gatewayHTTPSRouteConfigNamesForSection(
			downstreamNamespace,
			gateway,
			backend.sectionName,
			allHTTPSRouteConfigNames,
		)
		domainValue, err := json.Marshal(domain)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal tunnel domain %q: %w", domain, err)
		}
		for _, routeConfigName := range routeConfigNames {
			key := fmt.Sprintf("%s|%s", domain, routeConfigName)
			if _, ok := seenDomainRouteConfig[key]; ok {
				continue
			}
			seenDomainRouteConfig[key] = struct{}{}
			patches = append(patches, envoygatewayv1alpha1.EnvoyJSONPatchConfig{
				Type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
				Name: routeConfigName,
				Operation: envoygatewayv1alpha1.JSONPatchOperation{
					Op:    envoygatewayv1alpha1.JSONPatchOperationType("add"),
					Path:  ptr.To("/virtual_hosts/0/domains/-"),
					Value: &apiextensionsv1.JSON{Raw: domainValue},
				},
			})
		}
	}

	// Add CONNECT routes so CONNECT requests are routed to connector clusters.
	//
	// - Pure CONNECT fallback uses connect_matcher (no path semantics).
	// - Extended CONNECT path-aware routes are generated when a rule explicitly
	//   matches method CONNECT with a non-root path.
	//
	// Insert at index 0 so CONNECT routes are matched before path-based routes.
	connectRoutes := buildConnectorConnectRoutes(downstreamNamespace, httpProxy, backends)
	for _, connectRoute := range connectRoutes {
		routeValue, err := json.Marshal(connectRoute.route)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal CONNECT route: %w", err)
		}
		routeConfigNames := gatewayHTTPSRouteConfigNamesForSection(
			downstreamNamespace,
			gateway,
			connectRoute.sectionName,
			allHTTPSRouteConfigNames,
		)
		for _, routeConfigName := range routeConfigNames {
			patches = append(patches, envoygatewayv1alpha1.EnvoyJSONPatchConfig{
				Type: "type.googleapis.com/envoy.config.route.v3.RouteConfiguration",
				Name: routeConfigName,
				Operation: envoygatewayv1alpha1.JSONPatchOperation{
					Op:    envoygatewayv1alpha1.JSONPatchOperationType("add"),
					Path:  ptr.To("/virtual_hosts/0/routes/0"),
					Value: &apiextensionsv1.JSON{Raw: routeValue},
				},
			})
		}
	}

	return patches, nil
}

func gatewayHTTPSRouteConfigNames(downstreamNamespace string, gateway *gatewayv1.Gateway) []string {
	names := make([]string, 0)
	seen := make(map[string]struct{})

	for _, listener := range gateway.Spec.Listeners {
		if listener.Protocol != gatewayv1.HTTPSProtocolType {
			continue
		}
		name := fmt.Sprintf("%s/%s/%s", downstreamNamespace, gateway.Name, listener.Name)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	return names
}

func gatewayHTTPSRouteConfigNamesForSection(
	downstreamNamespace string,
	gateway *gatewayv1.Gateway,
	sectionName *gatewayv1.SectionName,
	allHTTPSRouteConfigNames []string,
) []string {
	if sectionName == nil {
		return allHTTPSRouteConfigNames
	}

	for _, listener := range gateway.Spec.Listeners {
		if listener.Name != *sectionName || listener.Protocol != gatewayv1.HTTPSProtocolType {
			continue
		}
		return []string{fmt.Sprintf("%s/%s/%s", downstreamNamespace, gateway.Name, listener.Name)}
	}

	// If the provided section is not an HTTPS listener, no RouteConfiguration should be patched.
	return nil
}

type connectorConnectRoute struct {
	sectionName *gatewayv1.SectionName
	route       map[string]any
}

func buildConnectorConnectRoutes(
	downstreamNamespace string,
	httpProxy *networkingv1alpha.HTTPProxy,
	backends []connectorBackendPatch,
) []connectorConnectRoute {
	if len(backends) == 0 {
		return nil
	}

	clusterByRule := map[int]string{}
	sectionByRule := map[int]*gatewayv1.SectionName{}
	for _, backend := range backends {
		if _, ok := clusterByRule[backend.ruleIndex]; ok {
			continue
		}
		clusterByRule[backend.ruleIndex] = connectorClusterName(downstreamNamespace, httpProxy.Name, backend.ruleIndex)
		sectionByRule[backend.ruleIndex] = backend.sectionName
	}

	// Default pure CONNECT route falls back to the first connector backend.
	fallbackCluster := connectorClusterName(downstreamNamespace, httpProxy.Name, backends[0].ruleIndex)
	fallbackSection := backends[0].sectionName
	connectRoutes := make([]connectorConnectRoute, 0)

	// Extended CONNECT path-aware routes are derived from explicit CONNECT method matches.
	// We only create path-aware routes for non-root paths to avoid swallowing the pure CONNECT fallback.
	for ruleIndex, rule := range httpProxy.Spec.Rules {
		clusterName, ok := clusterByRule[ruleIndex]
		if !ok {
			continue
		}

		for _, match := range rule.Matches {
			if match.Method == nil || string(*match.Method) != http.MethodConnect {
				continue
			}
			if match.Path == nil || match.Path.Value == nil {
				continue
			}

			pathValue := ptr.Deref(match.Path.Value, "")
			if pathValue == "" || pathValue == "/" {
				// Explicit CONNECT "/" acts as fallback target.
				fallbackCluster = clusterName
				fallbackSection = sectionByRule[ruleIndex]
				continue
			}

			connectMatch := map[string]any{
				"headers": []map[string]any{
					{
						"name": ":method",
						"string_match": map[string]any{
							"exact": http.MethodConnect,
						},
					},
				},
			}
			// TODO: Add optional :protocol matching for extended CONNECT routes
			// when we need to distinguish routes beyond path semantics.

			switch ptr.Deref(match.Path.Type, gatewayv1.PathMatchPathPrefix) {
			case gatewayv1.PathMatchPathPrefix:
				connectMatch["prefix"] = pathValue
			case gatewayv1.PathMatchExact:
				connectMatch["path"] = pathValue
			default:
				// Regex path matching can be added later when needed.
				continue
			}

			connectRoutes = append(connectRoutes, connectorConnectRoute{
				sectionName: sectionByRule[ruleIndex],
				route: map[string]any{
					"name":  fmt.Sprintf("connector-connect-%s-rule-%d", httpProxy.Name, ruleIndex),
					"match": connectMatch,
					"route": map[string]any{
						"cluster": clusterName,
						"upgrade_configs": []map[string]any{
							{
								"upgrade_type":   "CONNECT",
								"connect_config": map[string]any{},
							},
						},
					},
				},
			})
		}
	}

	// Always include a pure CONNECT fallback route.
	connectRoutes = append(connectRoutes, connectorConnectRoute{
		sectionName: fallbackSection,
		route: map[string]any{
			"name": fmt.Sprintf("connector-connect-%s", httpProxy.Name),
			"match": map[string]any{
				"connect_matcher": map[string]any{},
			},
			"route": map[string]any{
				"cluster": fallbackCluster,
				"upgrade_configs": []map[string]any{
					{
						"upgrade_type":   "CONNECT",
						"connect_config": map[string]any{},
					},
				},
			},
		},
	})

	return connectRoutes
}

func connectorRouteJSONPath(
	downstreamNamespace string,
	gateway *gatewayv1.Gateway,
	httpRouteName string,
	sectionName *gatewayv1.SectionName,
	ruleIndex int,
	matchIndex int,
) string {
	// vhost matches the Gateway + optional sectionName
	vhostConstraints := fmt.Sprintf(
		`@.metadata.filter_metadata["envoy-gateway"].resources[0].kind=="%s" && @.metadata.filter_metadata["envoy-gateway"].resources[0].namespace=="%s" && @.metadata.filter_metadata["envoy-gateway"].resources[0].name=="%s"`,
		KindGateway,
		downstreamNamespace,
		gateway.Name,
	)

	if sectionName != nil {
		vhostConstraints += fmt.Sprintf(
			` && @.metadata.filter_metadata["envoy-gateway"].resources[0].sectionName=="%s"`,
			string(*sectionName),
		)
	}

	// routes match the HTTPRoute + rule/match
	routeConstraints := fmt.Sprintf(
		`@.metadata.filter_metadata["envoy-gateway"].resources[0].kind=="%s" && @.metadata.filter_metadata["envoy-gateway"].resources[0].namespace=="%s" && @.metadata.filter_metadata["envoy-gateway"].resources[0].name=="%s" && @.name =~ ".*?/rule/%d/match/%d/.*"`,
		KindHTTPRoute,
		downstreamNamespace,
		httpRouteName,
		ruleIndex,
		matchIndex,
	)

	return sanitizeJSONPath(fmt.Sprintf(
		`..virtual_hosts[?(%s)]..routes[?(!@.bogus && %s)]`,
		vhostConstraints,
		routeConstraints,
	))
}

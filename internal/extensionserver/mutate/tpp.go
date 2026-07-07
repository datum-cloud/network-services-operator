package mutate

import (
	"encoding/json"
	"fmt"
	"strings"

	xdstypev3 "github.com/cncf/xds/go/xds/type/v3"
	golangv3alpha "github.com/envoyproxy/go-control-plane/contrib/envoy/extensions/filters/http/golang/v3alpha"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

const (
	// hcmFilterName is the well-known network-filter name EG assigns to the
	// HttpConnectionManager whose http_filters list NSO injects the Coraza
	// filter into (position 0, ahead of the router).
	hcmFilterName = "envoy.filters.network.http_connection_manager"

	// datumGatewayMetadataKey is the filter_metadata namespace NSO writes per
	// route to record the governing TrafficProtectionPolicy. See STATE.md
	// SINGLE SOURCE OF TRUTH.
	datumGatewayMetadataKey = "datum-gateway"

	// envoyGatewayMetadataKey is the filter_metadata namespace EG populates
	// with resource references (Gateway, HTTPRoute) on VHs and routes.
	envoyGatewayMetadataKey = "envoy-gateway"

	// kindGateway and kindHTTPRoute are the EG resource kind strings used in
	// filter_metadata resource references and TPP targetRef matching.
	kindGateway   = "Gateway"
	kindHTTPRoute = "HTTPRoute"

	// egMetaFieldResources, egMetaFieldKind, egMetaFieldNamespace, and
	// egMetaFieldName are the field keys used in the EG filter_metadata
	// resource reference struct and in the datum-gateway metadata struct.
	egMetaFieldResources = "resources"
	egMetaFieldKind      = "kind"
	egMetaFieldNamespace = "namespace"
	egMetaFieldName      = "name"
)

// CorazaConfig carries the Coraza WAF configuration needed for xDS mutation.
// Values are sourced from the operator's GatewayConfig.Coraza field.
type CorazaConfig struct {
	// Disabled disables all Coraza WAF injection when true. When true,
	// InjectCorazaListenerFilters and ApplyTPPRouteConfig are unconditional
	// no-ops — no golang HTTP listener filters and no per-route WAF config are
	// emitted. Set gateway.coraza.disabled: true in the operator config to skip
	// WAF injection (e.g. when the Coraza-enabled Envoy image is unavailable).
	Disabled bool
	// FilterName is the HTTP filter name in Envoy (e.g. "coraza-waf").
	FilterName string
	// LibraryID is the globally unique ID for the dynamic library file.
	LibraryID string
	// LibraryPath is the path to the Coraza dynamic library.
	LibraryPath string
	// PluginName is the globally unique name of the Coraza plugin.
	PluginName string
	// ListenerDirectives is the directive list for the disabled listener-scope
	// Coraza filter. These appear in the global filter installed (disabled) at
	// every HCM; route-level activation uses per-policy directives instead.
	ListenerDirectives []string
	// TraceRouteMetadataExtractor is a CEL expression for trace span attribute
	// extraction from route metadata. May be empty.
	TraceRouteMetadataExtractor string
}

// InjectCorazaListenerFilters prepends the Coraza golang HTTP filter
// (disabled=true) to every RDS-based HttpConnectionManager in every filter
// chain of the listener. The filter is disabled at listener scope and activated
// per-route via typed_per_filter_config. Ports InjectCorazaListenerFilters from
// test/perf/extserver/internal/mutate/tpp.go with CorazaConfig parameter.
//
// Only HCMs that use dynamic route discovery via RDS are mutated. HCMs with an
// inline static route_config (e.g. EG's internal readiness listener) are
// skipped — injecting the golang Coraza filter into them breaks Envoy on
// standard images that do not have the golang filter compiled in. This
// heuristic is name-independent: it tests the HCM route_specifier, not the
// listener name, so it is stable across EG naming scheme changes.
//
// Returns the number of HCMs mutated.
func InjectCorazaListenerFilters(l *listenerv3.Listener, cfg *CorazaConfig) (int, error) {
	// When disabled or unconfigured, skip all Coraza injection so that zero
	// golang HTTP filters are emitted. This is the primary defence against
	// injecting into standard Envoy images that lack the golang filter.
	if cfg.Disabled || cfg.FilterName == "" {
		return 0, nil
	}

	filterAny, err := corazaListenerFilterAny(cfg)
	if err != nil {
		return 0, fmt.Errorf("build coraza listener filter: %w", err)
	}

	chains := make([]*listenerv3.FilterChain, 0, len(l.GetFilterChains())+1)
	chains = append(chains, l.GetFilterChains()...)
	if dfc := l.GetDefaultFilterChain(); dfc != nil {
		chains = append(chains, dfc)
	}

	mutated := 0
	for _, fc := range chains {
		for _, f := range fc.GetFilters() {
			if f.GetName() != hcmFilterName {
				continue
			}
			tc := f.GetTypedConfig()
			if tc == nil {
				continue
			}
			hcm := &hcmv3.HttpConnectionManager{}
			if err := tc.UnmarshalTo(hcm); err != nil {
				return mutated, fmt.Errorf("unmarshal HCM in filter chain %q: %w", fc.GetName(), err)
			}
			// Only inject into HCMs that use dynamic route discovery (RDS).
			// EG's internal listeners (e.g. envoy-gateway-proxy-ready-0.0.0.0-19003)
			// use an inline static route_config with a direct_response health check,
			// NOT RDS. Injecting the golang Coraza filter into those listeners causes
			// Envoy to reject the entire listener set on standard images.
			//
			// This check is name-independent: it operates on the HCM route_specifier
			// oneof, not on the listener or filter-chain name, so it remains correct
			// across EG naming-scheme changes (e.g. XDSNameSchemeV2).
			if hcm.GetRds() == nil {
				continue
			}
			if hcmHasFilter(hcm, cfg.FilterName) {
				continue
			}
			corazaFilter := &hcmv3.HttpFilter{
				Name:       cfg.FilterName,
				Disabled:   true, // enabled per-route via typed_per_filter_config
				ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: filterAny},
			}
			hcm.HttpFilters = append([]*hcmv3.HttpFilter{corazaFilter}, hcm.HttpFilters...)
			newTC, err := anypb.New(hcm)
			if err != nil {
				return mutated, fmt.Errorf("marshal HCM in filter chain %q: %w", fc.GetName(), err)
			}
			f.ConfigType = &listenerv3.Filter_TypedConfig{TypedConfig: newTC}
			mutated++
		}
	}
	return mutated, nil
}

// ApplyTPPRouteConfig applies per-route WAF config for routes governed by a
// TrafficProtectionPolicy. For each VirtualHost it:
//  1. Extracts the EG filter_metadata["envoy-gateway"] gateway resource ref.
//  2. Resolves the upstream namespace via idx.DStoUS.
//  3. Stamps project_name into datum-gateway route metadata on every NSO-owned route.
//  4. Finds the governing TPP from idx.TPPs (route-level wins over gateway-level).
//  5. Writes typed_per_filter_config and datum-gateway metadata on governed routes.
//
// Returns the number of routes mutated (WAF-configured routes only).
func ApplyTPPRouteConfig(
	rc *routev3.RouteConfiguration,
	idx *extcache.PolicyIndex,
	cfg *CorazaConfig,
) (int, error) {
	mutated := 0
	for _, vh := range rc.GetVirtualHosts() {
		// Extract VH-level EG resource reference; expect Kind=Gateway.
		vhKind, dsNS, gwName, vhOK := extractEGResource(vh.GetMetadata())
		if !vhOK || vhKind != kindGateway {
			continue
		}

		// Resolve upstream namespace from downstream-ns→upstream-ns map.
		upstreamNS, ok := idx.DStoUS[dsNS]
		if !ok {
			// VH is not NSO-owned; skip.
			continue
		}

		projectName := idx.ProjectNames[dsNS]
		tpps := idx.TPPs[upstreamNS]

		// Gateway-level governing TPP (no SectionName scoping in P1; see design §2.2 C5).
		gwTPP := findGatewayTPP(tpps, gwName)

		for _, rt := range vh.GetRoutes() {
			// Stamp project_name on every NSO-owned route so the Envoy access log
			// can emit it via %METADATA(ROUTE:datum-gateway:project_name)%.
			// applyRouteWAFConfig overwrites this entry for TPP-governed routes,
			// so it also includes project_name in the metadata it builds.
			injectProjectNameMetadata(rt, projectName)

			// WAF per-route config requires the listener-level filter to be present.
			// Skip when Coraza is disabled (e.g. standard Envoy image without the
			// golang filter) so that project_name is still stamped above.
			if cfg.Disabled {
				continue
			}

			// Check for a route-level TPP (HTTPRoute targeting) — takes precedence.
			_, _, routeName, _ := extractEGResource(rt.GetMetadata())
			routeTPP := findRouteTPP(tpps, routeName)

			governing := routeTPP
			if governing == nil {
				governing = gwTPP
			}
			if governing == nil || len(governing.Directives) == 0 {
				continue
			}

			if err := applyRouteWAFConfig(rt, governing, projectName, cfg); err != nil {
				return mutated, fmt.Errorf("apply WAF config to route %q: %w", rt.GetName(), err)
			}
			mutated++
		}
	}
	return mutated, nil
}

// injectProjectNameMetadata writes project_name into the datum-gateway
// filter_metadata of a route. Called for every NSO-owned route regardless of
// whether a TrafficProtectionPolicy governs it, so that
// %METADATA(ROUTE:datum-gateway:project_name)% is always available in the
// Envoy access log format.
func injectProjectNameMetadata(rt *routev3.Route, projectName string) {
	if rt.Metadata == nil {
		rt.Metadata = &corev3.Metadata{}
	}
	if rt.Metadata.FilterMetadata == nil {
		rt.Metadata.FilterMetadata = make(map[string]*structpb.Struct)
	}
	existing := rt.Metadata.FilterMetadata[datumGatewayMetadataKey]
	if existing == nil {
		s, _ := structpb.NewStruct(map[string]any{"project_name": projectName})
		rt.Metadata.FilterMetadata[datumGatewayMetadataKey] = s
		return
	}
	if existing.Fields == nil {
		existing.Fields = make(map[string]*structpb.Value)
	}
	existing.Fields["project_name"] = structpb.NewStringValue(projectName)
}

// applyRouteWAFConfig writes the datum-gateway filter_metadata and Coraza
// typed_per_filter_config onto a single route.
func applyRouteWAFConfig(rt *routev3.Route, tpp *extcache.TPPInfo, projectName string, cfg *CorazaConfig) error {
	meta, err := buildDatumGatewayMetadata(tpp, projectName)
	if err != nil {
		return fmt.Errorf("build datum-gateway metadata: %w", err)
	}
	tpfc, err := buildCorazaConfigsPerRoute(tpp.Directives, cfg)
	if err != nil {
		return fmt.Errorf("build coraza per-route config: %w", err)
	}

	if rt.Metadata == nil {
		rt.Metadata = &corev3.Metadata{}
	}
	if rt.Metadata.FilterMetadata == nil {
		rt.Metadata.FilterMetadata = make(map[string]*structpb.Struct)
	}
	rt.Metadata.FilterMetadata[datumGatewayMetadataKey] = meta

	if rt.TypedPerFilterConfig == nil {
		rt.TypedPerFilterConfig = make(map[string]*anypb.Any)
	}
	rt.TypedPerFilterConfig[cfg.FilterName] = tpfc
	return nil
}

// --- Targeting helpers ---

// findGatewayTPP returns the first TPP in tpps whose TargetRefs includes a
// Gateway target matching gatewayName (case-sensitive, local-policy semantics).
// SectionName listener scoping is deferred to P2 (see design plan §2.2 C5).
func findGatewayTPP(tpps []extcache.TPPInfo, gatewayName string) *extcache.TPPInfo {
	for i := range tpps {
		for _, ref := range tpps[i].TargetRefs {
			if string(ref.Kind) == kindGateway && string(ref.Name) == gatewayName {
				return &tpps[i]
			}
		}
	}
	return nil
}

// findRouteTPP returns the first TPP in tpps whose TargetRefs includes an
// HTTPRoute target matching routeName.
func findRouteTPP(tpps []extcache.TPPInfo, routeName string) *extcache.TPPInfo {
	if routeName == "" {
		return nil
	}
	for i := range tpps {
		for _, ref := range tpps[i].TargetRefs {
			if string(ref.Kind) == kindHTTPRoute && string(ref.Name) == routeName {
				return &tpps[i]
			}
		}
	}
	return nil
}

// --- EG metadata extraction ---

// extractEGResource reads the EG-populated filter_metadata["envoy-gateway"]
// resource reference from a metadata struct. Returns the kind, namespace,
// name of the first resource, and ok=true if found.
func extractEGResource(md *corev3.Metadata) (kind, namespace, name string, ok bool) {
	if md == nil {
		return
	}
	egMeta := md.GetFilterMetadata()[envoyGatewayMetadataKey]
	if egMeta == nil {
		return
	}
	resources := egMeta.GetFields()[egMetaFieldResources]
	if resources == nil {
		return
	}
	list := resources.GetListValue()
	if list == nil || len(list.GetValues()) == 0 {
		return
	}
	resource := list.GetValues()[0].GetStructValue()
	if resource == nil {
		return
	}
	fields := resource.GetFields()
	kind = fields[egMetaFieldKind].GetStringValue()
	namespace = fields[egMetaFieldNamespace].GetStringValue()
	name = fields[egMetaFieldName].GetStringValue()
	ok = true
	return
}

// --- Proto building helpers ---

// corazaListenerFilterAny builds the golang HTTP filter Any for the disabled
// listener-scope Coraza filter. Mirrors CorazaListenerFilterAny in the seed.
func corazaListenerFilterAny(cfg *CorazaConfig) (*anypb.Any, error) {
	pc, err := corazaPluginConfigAny(cfg.ListenerDirectives, cfg)
	if err != nil {
		return nil, err
	}
	gcfg := &golangv3alpha.Config{
		LibraryId:    cfg.LibraryID,
		LibraryPath:  cfg.LibraryPath,
		PluginName:   cfg.PluginName,
		PluginConfig: pc,
	}
	return anypb.New(gcfg)
}

// buildCorazaConfigsPerRoute builds the per-route ConfigsPerRoute Any carrying
// the policy's directives. Mirrors corazaConfigsPerRouteAny in the seed.
func buildCorazaConfigsPerRoute(directives []string, cfg *CorazaConfig) (*anypb.Any, error) {
	pc, err := corazaPluginConfigAny(directives, cfg)
	if err != nil {
		return nil, err
	}
	cpr := &golangv3alpha.ConfigsPerRoute{
		PluginsConfig: map[string]*golangv3alpha.RouterPlugin{
			cfg.PluginName: {
				Override: &golangv3alpha.RouterPlugin_Config{Config: pc},
			},
		},
	}
	return anypb.New(cpr)
}

// corazaPluginConfigAny builds the xds.type.v3.TypedStruct carrying the
// Coraza plugin config. Matches the production format in
// trafficprotectionpolicy_controller.go: getCorazaListenerFilterConfig.
func corazaPluginConfigAny(directives []string, cfg *CorazaConfig) (*anypb.Any, error) {
	directiveBytes, err := json.Marshal(directives)
	if err != nil {
		return nil, fmt.Errorf("marshal coraza directives: %w", err)
	}
	directivesStr := sanitizeJSONPath(
		fmt.Sprintf(`{"coraza":{"simple_directives":%s}}`, string(directiveBytes)),
	)

	fields := map[string]any{
		"log_format":        "json",
		"directives":        directivesStr,
		"default_directive": "coraza",
	}
	if cfg.TraceRouteMetadataExtractor != "" {
		fields["trace_route_metadata_extractor"] = cfg.TraceRouteMetadataExtractor
	}

	val, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, fmt.Errorf("build coraza plugin config struct: %w", err)
	}
	return anypb.New(&xdstypev3.TypedStruct{Value: val})
}

// buildDatumGatewayMetadata builds the filter_metadata["datum-gateway"] struct
// for a governed route. Matches the metadata contract in STATE.md.
func buildDatumGatewayMetadata(tpp *extcache.TPPInfo, projectName string) (*structpb.Struct, error) {
	s, err := structpb.NewStruct(map[string]any{
		"project_name": projectName,
		egMetaFieldResources: []any{
			map[string]any{
				egMetaFieldKind:      "TrafficProtectionPolicy",
				egMetaFieldNamespace: tpp.Namespace,
				egMetaFieldName:      tpp.Name,
				"mode":               string(tpp.Mode),
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("build datum-gateway metadata: %w", err)
	}
	return s, nil
}

// hcmHasFilter returns true if the HCM already has an HTTP filter with the
// given name, preventing duplicate injection on multiple reconcile passes.
func hcmHasFilter(hcm *hcmv3.HttpConnectionManager, name string) bool {
	for _, f := range hcm.GetHttpFilters() {
		if f.GetName() == name {
			return true
		}
	}
	return false
}

// sanitizeJSONPath removes embedded newlines and tabs from JSON strings.
// Mirrors sanitizeJSONPath in internal/controller/trafficprotectionpolicy_controller.go.
func sanitizeJSONPath(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	return strings.ReplaceAll(s, "\t", "")
}

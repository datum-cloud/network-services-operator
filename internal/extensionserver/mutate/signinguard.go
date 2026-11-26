package mutate

import (
	"fmt"
	"sort"
	"strings"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	jwtauthnv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/jwt_authn/v3"
	oauth2v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/oauth2/v3"
	routerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

const (
	jwtAuthnFilterName        = "envoy.filters.http.jwt_authn"
	oauth2FilterName          = "envoy.filters.http.oauth2"
	routerFilterName          = "envoy.filters.http.router"
	SignInGuardListenerPrefix = "signin-upstream-guard/"
	signInGuardClusterSuffix  = "/signin-upstream-guard"
)

func ApplySignInUpstreamGuard(
	listeners []*listenerv3.Listener,
	routes []*routev3.RouteConfiguration,
	clusters []*clusterv3.Cluster,
	idx *extcache.PolicyIndex,
) ([]*listenerv3.Listener, []*clusterv3.Cluster, int, error) {
	targets, err := signInClusterNames(listeners, routes, idx)
	if err != nil {
		return listeners, clusters, 0, err
	}
	for _, cl := range clusters {
		if isTenantCluster(cl.GetName(), idx) && isSecurityPolicyCluster(cl.GetName()) {
			targets[cl.GetName()] = struct{}{}
		}
	}
	if len(targets) == 0 {
		return listeners, clusters, 0, nil
	}

	filter, err := upstreamAddressGuardFilter()
	if err != nil {
		return listeners, clusters, 0, err
	}
	existing := make(map[string]struct{}, len(listeners))
	for _, l := range listeners {
		existing[l.GetName()] = struct{}{}
	}

	addedClusters := make([]*clusterv3.Cluster, 0, len(targets))
	addedListeners := make([]*listenerv3.Listener, 0, len(targets))
	guarded := 0
	for i, cl := range clusters {
		if strings.HasSuffix(cl.GetName(), signInGuardClusterSuffix) {
			continue
		}
		if _, ok := targets[cl.GetName()]; !ok || !guardResolvesNames(cl) {
			continue
		}
		listenerName := SignInGuardListenerPrefix + cl.GetName()
		if _, ok := existing[listenerName]; ok {
			continue
		}

		inner := proto.Clone(cl).(*clusterv3.Cluster)
		inner.Name = cl.GetName() + signInGuardClusterSuffix
		if la := inner.GetLoadAssignment(); la != nil {
			la.ClusterName = inner.Name
		}
		if _, err := guardCluster(inner, filter); err != nil {
			return listeners, clusters, guarded, fmt.Errorf("guard sign-in cluster %q: %w", cl.GetName(), err)
		}

		outer := hairpinCluster(cl, listenerName)
		l, err := hairpinListener(listenerName, inner.Name)
		if err != nil {
			return listeners, clusters, guarded, fmt.Errorf("hairpin listener %q: %w", listenerName, err)
		}

		clusters[i] = outer
		addedClusters = append(addedClusters, inner)
		addedListeners = append(addedListeners, l)
		guarded++
	}
	sort.Slice(addedClusters, func(a, b int) bool { return addedClusters[a].GetName() < addedClusters[b].GetName() })
	sort.Slice(addedListeners, func(a, b int) bool { return addedListeners[a].GetName() < addedListeners[b].GetName() })
	return append(listeners, addedListeners...), append(clusters, addedClusters...), guarded, nil
}

func isSecurityPolicyCluster(name string) bool {
	kind, _, _ := strings.Cut(name, "/")
	return kind == "securitypolicy"
}

func signInClusterNames(
	listeners []*listenerv3.Listener,
	routes []*routev3.RouteConfiguration,
	idx *extcache.PolicyIndex,
) (map[string]struct{}, error) {
	requirements := map[string]*jwtauthnv3.JwtRequirement{}
	providers := map[string]*jwtauthnv3.JwtProvider{}
	shared := map[string]struct{}{}
	for _, l := range listeners {
		for _, fc := range append(l.GetFilterChains(), l.GetDefaultFilterChain()) {
			for _, f := range fc.GetFilters() {
				if f.GetName() != hcmFilterName {
					continue
				}
				hcm := &hcmv3.HttpConnectionManager{}
				if err := f.GetTypedConfig().UnmarshalTo(hcm); err != nil {
					continue
				}
				for _, hf := range hcm.GetHttpFilters() {
					if hf.GetTypedConfig() == nil {
						continue
					}
					switch hf.GetName() {
					case jwtAuthnFilterName:
						cfg := &jwtauthnv3.JwtAuthentication{}
						if err := hf.GetTypedConfig().UnmarshalTo(cfg); err != nil {
							return nil, fmt.Errorf("decode jwt_authn on listener %q: %w", l.GetName(), err)
						}
						for k, v := range cfg.GetRequirementMap() {
							requirements[k] = v
						}
						for k, v := range cfg.GetProviders() {
							providers[k] = v
						}
						for _, rule := range cfg.GetRules() {
							addSignInClusters(shared, rule.GetRequires(), cfg.GetProviders())
						}
					case oauth2FilterName:
						cfg := &oauth2v3.OAuth2{}
						if err := hf.GetTypedConfig().UnmarshalTo(cfg); err == nil {
							addCluster(shared, cfg.GetConfig().GetTokenEndpoint().GetCluster())
						}
					}
				}
			}
		}
	}

	targets := map[string]struct{}{}
	for _, rc := range routes {
		for _, vh := range rc.GetVirtualHosts() {
			for _, rt := range vh.GetRoutes() {
				into := targets
				if !isTenantCluster(rt.GetName(), idx) {
					into = shared
					addCluster(shared, rt.GetRoute().GetCluster())
					for _, wc := range rt.GetRoute().GetWeightedClusters().GetClusters() {
						addCluster(shared, wc.GetName())
					}
				}
				per := rt.GetTypedPerFilterConfig()
				if raw, ok := per[oauth2FilterName]; ok {
					cfg := &oauth2v3.OAuth2PerRoute{}
					if err := raw.UnmarshalTo(cfg); err == nil {
						addCluster(into, cfg.GetConfig().GetTokenEndpoint().GetCluster())
					}
				}
				if raw, ok := per[jwtAuthnFilterName]; ok {
					cfg := &jwtauthnv3.PerRouteConfig{}
					if err := raw.UnmarshalTo(cfg); err == nil {
						addSignInClusters(into, requirements[cfg.GetRequirementName()], providers)
					}
				}
			}
		}
	}
	for name := range shared {
		delete(targets, name)
	}
	return targets, nil
}

func addSignInClusters(into map[string]struct{}, req *jwtauthnv3.JwtRequirement, providers map[string]*jwtauthnv3.JwtProvider) {
	for _, name := range requirementProviders(req) {
		addCluster(into, providers[name].GetRemoteJwks().GetHttpUri().GetCluster())
	}
}

func addCluster(into map[string]struct{}, name string) {
	if name != "" {
		into[name] = struct{}{}
	}
}

func requirementProviders(req *jwtauthnv3.JwtRequirement) []string {
	if req == nil {
		return nil
	}
	switch r := req.GetRequiresType().(type) {
	case *jwtauthnv3.JwtRequirement_ProviderName:
		return []string{r.ProviderName}
	case *jwtauthnv3.JwtRequirement_ProviderAndAudiences:
		return []string{r.ProviderAndAudiences.GetProviderName()}
	case *jwtauthnv3.JwtRequirement_RequiresAny:
		out := make([]string, 0, len(r.RequiresAny.GetRequirements()))
		for _, sub := range r.RequiresAny.GetRequirements() {
			out = append(out, requirementProviders(sub)...)
		}
		return out
	case *jwtauthnv3.JwtRequirement_RequiresAll:
		out := make([]string, 0, len(r.RequiresAll.GetRequirements()))
		for _, sub := range r.RequiresAll.GetRequirements() {
			out = append(out, requirementProviders(sub)...)
		}
		return out
	}
	return nil
}

func hairpinCluster(orig *clusterv3.Cluster, listenerName string) *clusterv3.Cluster {
	out := &clusterv3.Cluster{
		Name:                          orig.GetName(),
		ClusterDiscoveryType:          &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
		ConnectTimeout:                orig.GetConnectTimeout(),
		TypedExtensionProtocolOptions: map[string]*anypb.Any{},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: orig.GetName(),
			Endpoints: []*endpointv3.LocalityLbEndpoints{{
				LbEndpoints: []*endpointv3.LbEndpoint{{
					HostIdentifier: &endpointv3.LbEndpoint_Endpoint{Endpoint: &endpointv3.Endpoint{
						Address: &corev3.Address{Address: &corev3.Address_EnvoyInternalAddress{
							EnvoyInternalAddress: &corev3.EnvoyInternalAddress{
								AddressNameSpecifier: &corev3.EnvoyInternalAddress_ServerListenerName{
									ServerListenerName: listenerName,
								},
							},
						}},
					}},
				}},
			}},
		},
	}
	if out.ConnectTimeout == nil {
		out.ConnectTimeout = durationpb.New(5 * time.Second)
	}
	if raw, ok := orig.GetTypedExtensionProtocolOptions()[httpProtocolOptionsKey]; ok {
		out.TypedExtensionProtocolOptions[httpProtocolOptionsKey] = raw
	}
	return out
}

func hairpinListener(name, cluster string) (*listenerv3.Listener, error) {
	router, err := anypb.New(&routerv3.Router{SuppressEnvoyHeaders: true})
	if err != nil {
		return nil, err
	}
	hcm, err := anypb.New(&hcmv3.HttpConnectionManager{
		StatPrefix:        "signin_upstream_guard",
		CodecType:         hcmv3.HttpConnectionManager_AUTO,
		GenerateRequestId: wrapperspb.Bool(false),
		RouteSpecifier: &hcmv3.HttpConnectionManager_RouteConfig{RouteConfig: &routev3.RouteConfiguration{
			Name: name,
			VirtualHosts: []*routev3.VirtualHost{{
				Name:    name,
				Domains: []string{"*"},
				Routes: []*routev3.Route{{
					Match: &routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"}},
					Action: &routev3.Route_Route{Route: &routev3.RouteAction{
						ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: cluster},
						Timeout:          durationpb.New(0),
					}},
				}},
			}},
		}},
		HttpFilters: []*hcmv3.HttpFilter{{
			Name:       routerFilterName,
			ConfigType: &hcmv3.HttpFilter_TypedConfig{TypedConfig: router},
		}},
	})
	if err != nil {
		return nil, err
	}
	return &listenerv3.Listener{
		Name:              name,
		ListenerSpecifier: &listenerv3.Listener_InternalListener{InternalListener: &listenerv3.Listener_InternalListenerConfig{}},
		FilterChains: []*listenerv3.FilterChain{{
			Filters: []*listenerv3.Filter{{
				Name:       hcmFilterName,
				ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcm},
			}},
		}},
	}, nil
}

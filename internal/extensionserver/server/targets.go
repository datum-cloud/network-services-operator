package server

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

const kindGateway, kindHTTPRoute = "Gateway", "HTTPRoute"

type objectKey struct{ namespace, name string }

// targetIndex says which policy targets exist on this edge, from the cache.
// It decides removals, which must not depend on the content of any one build.
type targetIndex struct {
	gateways map[objectKey]bool
	routes   map[objectKey]bool
	rules    map[objectKey]map[string]bool // HTTPProxy rule names; a rule-scoped target names one
}

func buildTargetIndex(ctx context.Context, cl client.Reader) (targetIndex, error) {
	idx := targetIndex{gateways: map[objectKey]bool{}, routes: map[objectKey]bool{}, rules: map[objectKey]map[string]bool{}}
	for kind, into := range map[string]map[objectKey]bool{kindGateway: idx.gateways, kindHTTPRoute: idx.routes} {
		list := extcache.GatewayAPIMetadataList(kind)
		if err := cl.List(ctx, list); err != nil {
			return idx, fmt.Errorf("list %s: %w", kind, err)
		}
		for _, item := range list.Items {
			into[objectKey{item.Namespace, item.Name}] = true
		}
	}
	var proxies networkingv1alpha.HTTPProxyList
	if err := cl.List(ctx, &proxies); err != nil {
		return idx, fmt.Errorf("list HTTPProxies: %w", err)
	}
	for _, proxy := range proxies.Items {
		names := map[string]bool{}
		for _, rule := range proxy.Spec.Rules {
			if rule.Name != nil {
				names[string(*rule.Name)] = true
			}
		}
		idx.rules[objectKey{proxy.Namespace, proxy.Name}] = names
	}
	return idx, nil
}

// exists reports whether a policy's target exists. A Gateway target's
// sectionName is not resolved, as the WAF does not resolve it either; an
// HTTPRoute target's sectionName names a rule of the HTTPProxy of the same name.
func (t targetIndex) exists(namespace string, ref gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName) bool {
	key := objectKey{namespace, string(ref.Name)}
	switch string(ref.Kind) {
	case kindGateway:
		return t.gateways[key]
	case kindHTTPRoute:
		if !t.routes[key] {
			return false
		}
		return ref.SectionName == nil || *ref.SectionName == "" || t.rules[key][string(*ref.SectionName)]
	}
	return false
}

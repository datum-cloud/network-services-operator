// SPDX-License-Identifier: AGPL-3.0-only

package crd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func hostnameProxy(name string, mutate func(*networkingv1alpha.HTTPProxy)) *networkingv1alpha.HTTPProxy {
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{
					{Endpoint: "https://api.example.com"},
				},
			}},
		},
	}
	mutate(proxy)
	return proxy
}

// The generated schema locks urlRewrite.hostname but leaves every other route
// into that same field open. Those open doors are why admission validation
// (issue #347) cannot be replaced by kubebuilder markers alone; this test pins
// the asymmetry so a future schema change does not silently duplicate or
// contradict the webhook.
func TestHTTPProxyCRDHostnameConstraints(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	t.Run("urlRewrite.hostname is constrained by the schema", func(t *testing.T) {
		proxy := hostnameProxy("schema-url-rewrite", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.Rules[0].Filters = []gatewayv1.HTTPRouteFilter{{
				Type: gatewayv1.HTTPRouteFilterURLRewrite,
				URLRewrite: &gatewayv1.HTTPURLRewriteFilter{
					Hostname: ptr.To(gatewayv1.PreciseHostname("Coffee")),
				},
			}}
		})

		err := cl.Create(ctx, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})

	t.Run("a Host header value is not constrained by the schema", func(t *testing.T) {
		proxy := hostnameProxy("schema-host-header", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.Rules[0].Filters = []gatewayv1.HTTPRouteFilter{{
				Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
				RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
					Set: []gatewayv1.HTTPHeader{{Name: "Host", Value: "example.com:8080"}},
				},
			}}
		})

		require.NoError(t, cl.Create(ctx, proxy))
		t.Cleanup(func() { _ = cl.Delete(ctx, proxy) })
	})

	t.Run("tls.hostname is not constrained by the schema", func(t *testing.T) {
		proxy := hostnameProxy("schema-tls-hostname", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.Rules[0].Backends[0].Endpoint = "https://198.51.100.10"
			p.Spec.Rules[0].Backends[0].TLS = &networkingv1alpha.HTTPProxyBackendTLS{
				Hostname: ptr.To("not a hostname"),
			}
		})

		require.NoError(t, cl.Create(ctx, proxy))
		t.Cleanup(func() { _ = cl.Delete(ctx, proxy) })
	})

	t.Run("an endpoint port is not constrained by the schema", func(t *testing.T) {
		proxy := hostnameProxy("schema-endpoint-port", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.Rules[0].Backends[0].Endpoint = "http://api.example.com:99999"
		})

		require.NoError(t, cl.Create(ctx, proxy))
		t.Cleanup(func() { _ = cl.Delete(ctx, proxy) })
	})

	t.Run("a mixed case hostname the controller can normalize is accepted", func(t *testing.T) {
		proxy := hostnameProxy("schema-mixed-case", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.Rules[0].Filters = []gatewayv1.HTTPRouteFilter{{
				Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
				RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
					Set: []gatewayv1.HTTPHeader{{Name: "Host", Value: "Coffee.Example.Com"}},
				},
			}}
		})

		require.NoError(t, cl.Create(ctx, proxy))
		t.Cleanup(func() { _ = cl.Delete(ctx, proxy) })

		var got networkingv1alpha.HTTPProxy
		require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(proxy), &got))
		assert.Equal(t, "Coffee.Example.Com", got.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value,
			"the stored value must be left alone; normalization happens where it is programmed")
	})
}

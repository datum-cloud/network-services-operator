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
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func backendProxy(name string, backend networkingv1alpha.HTTPProxyRuleBackend) *networkingv1alpha.HTTPProxy {
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{backend},
			}},
		},
	}
}

// TestHTTPProxyCRDBackendExclusivity pins the CEL rule on HTTPProxyRuleBackend
// against a real apiserver. Regression guard: the rule originally required
// exactly one of endpoint/connector/instance, which rejected every existing
// connector backend — those always carry endpoint (the tunnel's target
// address) alongside connector (which tunnel to use). Only instance is
// mutually exclusive with the other two; endpoint is required unless instance
// is set.
func TestHTTPProxyCRDBackendExclusivity(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	create := func(t *testing.T, proxy *networkingv1alpha.HTTPProxy) error {
		t.Helper()
		err := cl.Create(ctx, proxy)
		if err == nil {
			t.Cleanup(func() { _ = cl.Delete(ctx, proxy) })
		}
		return err
	}

	t.Run("endpoint alone is valid", func(t *testing.T) {
		proxy := backendProxy("backend-endpoint-only", networkingv1alpha.HTTPProxyRuleBackend{
			Endpoint: "https://api.example.com",
		})
		require.NoError(t, create(t, proxy))
	})

	t.Run("endpoint and connector together is valid", func(t *testing.T) {
		// The combination every connector-backed HTTPProxy in this repo uses:
		// endpoint carries the tunnel's target address, connector says which
		// tunnel to route it through.
		proxy := backendProxy("backend-endpoint-connector", networkingv1alpha.HTTPProxyRuleBackend{
			Endpoint:  "http://connect-proxy.default.svc.cluster.local:8080",
			Connector: &networkingv1alpha.ConnectorReference{Name: "test-connector"},
		})
		require.NoError(t, create(t, proxy))
	})

	t.Run("instance alone is valid", func(t *testing.T) {
		proxy := backendProxy("backend-instance-only", networkingv1alpha.HTTPProxyRuleBackend{
			Instance: &networkingv1alpha.InstanceBackendRef{Name: "vpc-pod-1", Port: 8080},
		})
		require.NoError(t, create(t, proxy))
	})

	t.Run("instance with endpoint is rejected", func(t *testing.T) {
		proxy := backendProxy("backend-instance-endpoint", networkingv1alpha.HTTPProxyRuleBackend{
			Endpoint: "https://api.example.com",
			Instance: &networkingv1alpha.InstanceBackendRef{Name: "vpc-pod-1", Port: 8080},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})

	t.Run("instance with connector is rejected", func(t *testing.T) {
		proxy := backendProxy("backend-instance-connector", networkingv1alpha.HTTPProxyRuleBackend{
			Connector: &networkingv1alpha.ConnectorReference{Name: "test-connector"},
			Instance:  &networkingv1alpha.InstanceBackendRef{Name: "vpc-pod-1", Port: 8080},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})

	t.Run("networkService alone is valid", func(t *testing.T) {
		proxy := backendProxy("backend-networkservice-only", networkingv1alpha.HTTPProxyRuleBackend{
			NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: "storefront", Port: "http"},
		})
		require.NoError(t, create(t, proxy))
	})

	t.Run("networkService with endpoint is rejected", func(t *testing.T) {
		proxy := backendProxy("backend-networkservice-endpoint", networkingv1alpha.HTTPProxyRuleBackend{
			Endpoint:       "https://api.example.com",
			NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: "storefront", Port: "http"},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})

	t.Run("networkService with connector is rejected", func(t *testing.T) {
		proxy := backendProxy("backend-networkservice-connector", networkingv1alpha.HTTPProxyRuleBackend{
			Connector:      &networkingv1alpha.ConnectorReference{Name: "test-connector"},
			NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: "storefront", Port: "http"},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})

	t.Run("networkService with instance is rejected", func(t *testing.T) {
		proxy := backendProxy("backend-networkservice-instance", networkingv1alpha.HTTPProxyRuleBackend{
			Instance:       &networkingv1alpha.InstanceBackendRef{Name: "vpc-pod-1", Port: 8080},
			NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: "storefront", Port: "http"},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})

	t.Run("networkService with backend tls is rejected", func(t *testing.T) {
		proxy := backendProxy("backend-networkservice-tls", networkingv1alpha.HTTPProxyRuleBackend{
			NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: "storefront", Port: "http"},
			TLS:            &networkingv1alpha.HTTPProxyBackendTLS{Hostname: ptr.To("origin.example.com")},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
		assert.Contains(t, err.Error(), "backend TLS is not supported for networkService backends")
	})

	t.Run("endpoint with backend tls stays valid", func(t *testing.T) {
		proxy := backendProxy("backend-endpoint-tls", networkingv1alpha.HTTPProxyRuleBackend{
			Endpoint: "https://203.0.113.10",
			TLS:      &networkingv1alpha.HTTPProxyBackendTLS{Hostname: ptr.To("origin.example.com")},
		})
		require.NoError(t, create(t, proxy))
	})

	t.Run("neither endpoint nor instance nor networkService is rejected", func(t *testing.T) {
		proxy := backendProxy("backend-neither", networkingv1alpha.HTTPProxyRuleBackend{
			Connector: &networkingv1alpha.ConnectorReference{Name: "test-connector"},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})
}

func ruleProxy(name string, rule networkingv1alpha.HTTPProxyRule) *networkingv1alpha.HTTPProxy {
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{rule},
		},
	}
}

// TestHTTPProxyCRDConnectorBackendExclusivity pins the rule-level CEL rule that
// a connector backend must be the only backend in its rule.
//
// Regression guard: the rule originally read
// `self.backends.exists(b, has(b.connector)) ? size(self.backends) == 1 : true`
// with no `has(self.backends)` guard. Referencing an absent optional field
// throws in CEL, so any rule that omitted backends entirely — such as the
// HTTP→HTTPS redirect rule the portal adds to every proxy — was rejected with
// this rule's message, even though it has no connector backend at all.
func TestHTTPProxyCRDConnectorBackendExclusivity(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	create := func(t *testing.T, proxy *networkingv1alpha.HTTPProxy) error {
		t.Helper()
		err := cl.Create(ctx, proxy)
		if err == nil {
			t.Cleanup(func() { _ = cl.Delete(ctx, proxy) })
		}
		return err
	}

	t.Run("redirect rule with no backends is valid", func(t *testing.T) {
		// The exact shape the portal emits for the HTTP→HTTPS redirect: matches
		// and a RequestRedirect filter, no backends field. This is what the
		// missing `has(self.backends)` guard rejected.
		proxy := ruleProxy("rule-redirect-no-backends", networkingv1alpha.HTTPProxyRule{
			Matches: []gatewayv1.HTTPRouteMatch{{
				Path: &gatewayv1.HTTPPathMatch{
					Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
					Value: ptr.To("/"),
				},
			}},
			Filters: []gatewayv1.HTTPRouteFilter{{
				Type: gatewayv1.HTTPRouteFilterRequestRedirect,
				RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
					Scheme:     ptr.To("https"),
					StatusCode: ptr.To(301),
				},
			}},
		})
		require.NoError(t, create(t, proxy))
	})

	t.Run("multiple endpoint backends is valid", func(t *testing.T) {
		proxy := ruleProxy("rule-multi-endpoint", networkingv1alpha.HTTPProxyRule{
			Backends: []networkingv1alpha.HTTPProxyRuleBackend{
				{Endpoint: "https://a.example.com"},
				{Endpoint: "https://b.example.com"},
			},
		})
		require.NoError(t, create(t, proxy))
	})

	t.Run("connector backend alone is valid", func(t *testing.T) {
		proxy := ruleProxy("rule-connector-alone", networkingv1alpha.HTTPProxyRule{
			Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
				Endpoint:  "http://connect-proxy.default.svc.cluster.local:8080",
				Connector: &networkingv1alpha.ConnectorReference{Name: "test-connector"},
			}},
		})
		require.NoError(t, create(t, proxy))
	})

	t.Run("connector backend alongside another backend is rejected", func(t *testing.T) {
		proxy := ruleProxy("rule-connector-plus-endpoint", networkingv1alpha.HTTPProxyRule{
			Backends: []networkingv1alpha.HTTPProxyRuleBackend{
				{
					Endpoint:  "http://connect-proxy.default.svc.cluster.local:8080",
					Connector: &networkingv1alpha.ConnectorReference{Name: "test-connector"},
				},
				{Endpoint: "https://b.example.com"},
			},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
		assert.Contains(t, err.Error(), "a connector backend must be the only backend in its rule")
	})
}

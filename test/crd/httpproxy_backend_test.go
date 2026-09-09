// SPDX-License-Identifier: AGPL-3.0-only

package crd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	t.Run("neither endpoint nor instance is rejected", func(t *testing.T) {
		proxy := backendProxy("backend-neither", networkingv1alpha.HTTPProxyRuleBackend{
			Connector: &networkingv1alpha.ConnectorReference{Name: "test-connector"},
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})
}

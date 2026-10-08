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

func healthCheckProxy(name string, mutate func(*networkingv1alpha.HTTPProxy)) *networkingv1alpha.HTTPProxy {
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

func TestHTTPProxyCRDPassiveHealthCheckDefaults(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	proxy := healthCheckProxy("healthcheck-defaults", func(p *networkingv1alpha.HTTPProxy) {
		p.Spec.HealthCheck = &networkingv1alpha.HTTPProxyHealthCheck{
			Passive: &networkingv1alpha.HTTPProxyPassiveHealthCheck{},
		}
	})
	require.NoError(t, cl.Create(ctx, proxy))
	t.Cleanup(func() { _ = cl.Delete(ctx, proxy) })

	var got networkingv1alpha.HTTPProxy
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(proxy), &got))
	require.NotNil(t, got.Spec.HealthCheck)
	require.NotNil(t, got.Spec.HealthCheck.Passive)
	passive := got.Spec.HealthCheck.Passive
	require.NotNil(t, passive.Consecutive5xxErrors)
	assert.Equal(t, networkingv1alpha.DefaultPassiveConsecutive5xxErrors, *passive.Consecutive5xxErrors)
	require.NotNil(t, passive.BaseEjectionTime)
	assert.Equal(t, networkingv1alpha.DefaultPassiveBaseEjectionTime, *passive.BaseEjectionTime)
	require.NotNil(t, passive.MaxEjectionPercent)
	assert.Equal(t, networkingv1alpha.DefaultPassiveMaxEjectionPercent, *passive.MaxEjectionPercent)
}

func TestHTTPProxyCRDPassiveHealthCheckBounds(t *testing.T) {
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

	t.Run("explicit values are kept", func(t *testing.T) {
		proxy := healthCheckProxy("healthcheck-explicit", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.HealthCheck = &networkingv1alpha.HTTPProxyHealthCheck{
				Passive: &networkingv1alpha.HTTPProxyPassiveHealthCheck{
					Consecutive5xxErrors: ptr.To(int32(3)),
					BaseEjectionTime:     ptr.To(gatewayv1.Duration("15s")),
					MaxEjectionPercent:   ptr.To(int32(25)),
				},
			}
		})
		require.NoError(t, create(t, proxy))

		var got networkingv1alpha.HTTPProxy
		require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(proxy), &got))
		require.NotNil(t, got.Spec.HealthCheck)
		require.NotNil(t, got.Spec.HealthCheck.Passive)
		assert.Equal(t, int32(3), *got.Spec.HealthCheck.Passive.Consecutive5xxErrors)
		assert.Equal(t, gatewayv1.Duration("15s"), *got.Spec.HealthCheck.Passive.BaseEjectionTime)
		assert.Equal(t, int32(25), *got.Spec.HealthCheck.Passive.MaxEjectionPercent)
	})

	t.Run("consecutive 5xx below one is rejected", func(t *testing.T) {
		proxy := healthCheckProxy("healthcheck-zero-5xx", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.HealthCheck = &networkingv1alpha.HTTPProxyHealthCheck{
				Passive: &networkingv1alpha.HTTPProxyPassiveHealthCheck{
					Consecutive5xxErrors: ptr.To(int32(0)),
				},
			}
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})

	t.Run("max ejection percent of zero is rejected", func(t *testing.T) {
		proxy := healthCheckProxy("healthcheck-eject-zero", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.HealthCheck = &networkingv1alpha.HTTPProxyHealthCheck{
				Passive: &networkingv1alpha.HTTPProxyPassiveHealthCheck{
					MaxEjectionPercent: ptr.To(int32(0)),
				},
			}
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})

	t.Run("max ejection percent above 100 is rejected", func(t *testing.T) {
		proxy := healthCheckProxy("healthcheck-eject-101", func(p *networkingv1alpha.HTTPProxy) {
			p.Spec.HealthCheck = &networkingv1alpha.HTTPProxyHealthCheck{
				Passive: &networkingv1alpha.HTTPProxyPassiveHealthCheck{
					MaxEjectionPercent: ptr.To(int32(101)),
				},
			}
		})
		err := create(t, proxy)
		require.Error(t, err)
		assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
	})
}

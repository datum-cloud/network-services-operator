// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

func TestHTTPProxyStatusIsSavedByAPatchOfTheStatusAlone(t *testing.T) {
	ctx := context.Background()
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

	// A missing instance backend ends the reconcile early with a status to save.
	httpProxy := newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
		controllerutil.AddFinalizer(h, httpProxyFinalizer)
		h.Spec.Rules[0].Backends[0] = networkingv1alpha.HTTPProxyRuleBackend{
			Instance: &networkingv1alpha.InstanceBackendRef{Name: "does-not-exist", Port: 8080},
		}
	})

	var patches []map[string]any
	updates := 0
	cl := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(httpProxy).WithStatusSubresource(httpProxy).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*networkingv1alpha.HTTPProxy); ok {
					data, err := patch.Data(obj)
					require.NoError(t, err)
					var body map[string]any
					require.NoError(t, json.Unmarshal(data, &body))
					patches = append(patches, body)
				}
				return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*networkingv1alpha.HTTPProxy); ok {
					updates++
				}
				return c.SubResource(subResource).Update(ctx, obj, opts...)
			},
		}).Build()

	reconciler := &HTTPProxyReconciler{
		mgr: &fakeMockManager{cl: cl},
		Config: config.NetworkServicesOperator{
			HTTPProxy: config.HTTPProxyConfig{GatewayClassName: "test-gateway-class"},
			Gateway: config.GatewayConfig{
				ControllerName: gatewayv1.GatewayController("test-gateway-class"),
				TargetDomain:   "example.com",
			},
		},
	}
	_, err := reconciler.Reconcile(ctx, mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(httpProxy)},
		ClusterName: "test-cluster",
	})
	require.NoError(t, err)

	assert.Zero(t, updates)
	require.NotEmpty(t, patches)
	for _, body := range patches {
		assert.Equal(t, []string{"status"}, slices.Collect(maps.Keys(body)), "only the status is sent, without resourceVersion")
	}
}

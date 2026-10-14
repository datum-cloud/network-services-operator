// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

// goneBeforeTheFinalizerRemoval answers a finalizer's JSON patch the way the
// API server does once an earlier reconcile released the object.
var goneBeforeTheFinalizerRemoval = interceptor.Funcs{
	Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
		if patch.Type() == types.JSONPatchType {
			return apierrors.NewNotFound(schema.GroupResource{}, obj.GetName())
		}
		return c.Patch(ctx, obj, patch, opts...)
	},
}

// The HTTPProxy controller stands for the callers that return the removal's
// error as their own; the gateway controller is one too.
func TestAnHTTPProxyGoneBeforeItsFinalizerIsRemovedIsDone(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

	httpProxy := newHTTPProxy()
	httpProxy.DeletionTimestamp = &metav1.Time{Time: metav1.Now().Time}
	httpProxy.Finalizers = append(httpProxy.Finalizers, httpProxyFinalizer)
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: httpProxy.Namespace, UID: "11111111-1111-1111-1111-111111111111"}}
	downstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("ns-%s", namespace.UID)}}

	upstream := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(httpProxy, namespace).
		WithStatusSubresource(httpProxy).WithInterceptorFuncs(goneBeforeTheFinalizerRemoval).Build()
	reconciler := &HTTPProxyReconciler{
		mgr: &fakeMockManager{cl: upstream},
		Config: config.NetworkServicesOperator{
			HTTPProxy: config.HTTPProxyConfig{GatewayClassName: "test-gateway-class"},
			Gateway: config.GatewayConfig{
				ControllerName:             gatewayv1.GatewayController("test-gateway-class"),
				DownstreamGatewayClassName: "test-downstream-gateway-class",
			},
		},
		DownstreamCluster: &fakeCluster{cl: fake.NewClientBuilder().WithScheme(testScheme).WithObjects(downstreamNamespace).Build()},
	}

	_, err := reconciler.Reconcile(context.Background(), mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(httpProxy)},
		ClusterName: "test-cluster",
	})
	require.NoError(t, err)
}

// The replicator stands for the callers that kept their own NotFound check.
func TestAReplicatedObjectGoneBeforeItsFinalizerIsRemovedIsDone(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(testScheme))

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-suite", UID: "ns-uid"}}
	replicated := newUnstructuredObject(namespace.Name, "example", map[string]string{"replicate": "true"}, map[string]any{"foo": "bar"})
	replicated.SetUID("policy-uid")
	replicated.SetFinalizers([]string{gatewayResourceReplicatorFinalizer})
	replicated.SetDeletionTimestamp(&metav1.Time{Time: metav1.Now().Time})

	upstream := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(namespace, replicated).
		WithInterceptorFuncs(goneBeforeTheFinalizerRemoval).Build()
	reconciler := newReplicatorForTest(upstream, fake.NewClientBuilder().WithScheme(testScheme).Build(), testScheme)

	_, err := reconciler.Reconcile(context.Background(), gvkRequestFor(replicated))
	require.NoError(t, err)
}

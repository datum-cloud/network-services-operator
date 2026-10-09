// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// TestEnsureDownstreamHTTPRouteDeletesOnlyUnneededObjects verifies that a
// route's reconcile deletes exactly the downstream objects the route controls
// and no longer produces (#580). A delete of an object that does not exist is a
// 404 on the hub, and the removal log line must mean that something was removed.
func TestEnsureDownstreamHTTPRouteDeletesOnlyUnneededObjects(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:            "test-suite",
			DownstreamHostnameAccountingNamespace: "default",
			TargetDomain:                          "test-suite.com",
		},
	}

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}
	downstreamNamespace := fmt.Sprintf("ns-%s", upstreamNamespace.UID)

	// A route with no backends; annotations decide whether it needs a BackendTrafficPolicy.
	newRoute := func(annotations map[string]string) *gatewayv1.HTTPRoute {
		route := newHTTPRoute(upstreamNamespace.Name, "route", func(route *gatewayv1.HTTPRoute) {
			route.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: "test"}}
			route.Annotations = annotations
		})
		route.SetCreationTimestamp(metav1.Now())
		return route
	}

	downstreamRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespace, Name: "route", UID: uuid.NewUUID()},
	}
	controlledBy := func(owner *gatewayv1.HTTPRoute) []metav1.OwnerReference {
		return []metav1.OwnerReference{{
			APIVersion: gatewayv1.GroupVersion.String(),
			Kind:       KindHTTPRoute,
			Name:       owner.Name,
			UID:        owner.UID,
			Controller: ptr.To(true),
		}}
	}
	meta := func(name string, owners []metav1.OwnerReference) metav1.ObjectMeta {
		return metav1.ObjectMeta{Namespace: downstreamNamespace, Name: name, OwnerReferences: owners}
	}

	type outcome struct {
		downstream client.Client
		deletes    []string
		removals   int
	}

	run := func(t *testing.T, upstreamRoute *gatewayv1.HTTPRoute, existing ...client.Object) outcome {
		t.Helper()
		upstreamGateway := newGateway(testConfig, upstreamNamespace.Name, "test")
		fakeUpstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(upstreamGateway, upstreamNamespace, upstreamRoute).
			WithStatusSubresource(upstreamGateway, upstreamRoute).
			Build()

		var mu sync.Mutex
		var got outcome
		downstreamGateway := newGateway(testConfig, downstreamNamespace, upstreamGateway.Name)
		got.downstream = fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(append([]client.Object{downstreamGateway, downstreamRoute.DeepCopy()}, existing...)...).
			WithStatusSubresource(downstreamGateway).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					mu.Lock()
					got.deletes = append(got.deletes, fmt.Sprintf("%T/%s", obj, obj.GetName()))
					mu.Unlock()
					return cl.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		ctx := log.IntoContext(context.Background(), funcr.New(func(_, args string) {
			if strings.Contains(args, `"msg"="stale downstream resource removed"`) {
				mu.Lock()
				got.removals++
				mu.Unlock()
			}
		}, funcr.Options{}))

		reconciler := &GatewayReconciler{
			mgr:               &fakeMockManager{cl: fakeUpstreamClient},
			DownstreamCluster: &fakeCluster{cl: got.downstream},
		}
		downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", fakeUpstreamClient, got.downstream)
		result := reconciler.ensureDownstreamGatewayHTTPRoutes(
			ctx, fakeUpstreamClient, upstreamGateway, "test", downstreamGateway, downstreamStrategy, nil, nil, nil,
		)
		require.NoError(t, result.Err)
		return got
	}

	exists := func(t *testing.T, cl client.Client, obj client.Object) bool {
		t.Helper()
		err := cl.Get(context.Background(), types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}, obj)
		if apierrors.IsNotFound(err) {
			return false
		}
		require.NoError(t, err)
		return true
	}

	t.Run("a route that never had an object sends no delete", func(t *testing.T) {
		got := run(t, newRoute(nil))
		assert.Empty(t, got.deletes, "a delete of an object that does not exist is a 404 on the hub")
		assert.Zero(t, got.removals)
	})

	t.Run("objects the route controls and no longer produces are removed", func(t *testing.T) {
		route := newRoute(nil)
		trafficPolicy := &envoygatewayv1alpha1.BackendTrafficPolicy{ObjectMeta: meta(fmt.Sprintf("route-%s-panic-threshold", route.UID), controlledBy(downstreamRoute))}
		tlsPolicy := &gatewayv1.BackendTLSPolicy{ObjectMeta: meta("route-rule-gone", controlledBy(downstreamRoute))}
		service := &corev1.Service{ObjectMeta: meta("route-backend-gone", controlledBy(downstreamRoute))}
		got := run(t, route, trafficPolicy, tlsPolicy, service)
		for _, obj := range []client.Object{trafficPolicy, tlsPolicy, service} {
			assert.False(t, exists(t, got.downstream, obj.DeepCopyObject().(client.Object)), "%T %s", obj, obj.GetName())
		}
		assert.Equal(t, 3, got.removals)
	})

	t.Run("an object the route still produces stays", func(t *testing.T) {
		route := newRoute(map[string]string{HealthCheckAnnotation: `{"passive":{}}`})
		trafficPolicy := &envoygatewayv1alpha1.BackendTrafficPolicy{ObjectMeta: meta(fmt.Sprintf("route-%s-panic-threshold", route.UID), controlledBy(downstreamRoute))}
		got := run(t, route, trafficPolicy)
		assert.True(t, exists(t, got.downstream, trafficPolicy.DeepCopy()))
		assert.Empty(t, got.deletes)
	})

	t.Run("objects another route controls, or none does, stay", func(t *testing.T) {
		otherRoute := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "other", UID: uuid.NewUUID()}}
		theirs := &gatewayv1.BackendTLSPolicy{ObjectMeta: meta("other-rule", controlledBy(otherRoute))}
		unowned := &corev1.Service{ObjectMeta: meta("unowned", nil)}
		got := run(t, newRoute(nil), theirs, unowned)
		assert.True(t, exists(t, got.downstream, theirs.DeepCopy()))
		assert.True(t, exists(t, got.downstream, unowned.DeepCopy()))
		assert.Empty(t, got.deletes)
	})
}

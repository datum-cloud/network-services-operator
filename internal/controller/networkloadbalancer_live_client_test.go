// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestNLBDataPlaneRetriesConcurrentStatus(t *testing.T) {
	ctx := context.Background()
	lb, _ := dataPlaneFixture()
	base := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(lb, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: lb.Namespace, UID: "project-ns"}}).WithStatusSubresource(lb).Build()
	hub := fake.NewClientBuilder().WithScheme(base.Scheme()).WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-project-ns"}}).Build()
	var writes int
	writer := interceptor.NewClient(base, interceptor.Funcs{SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
		writes++
		if writes == 1 {
			current := &networkingv1alpha.NetworkLoadBalancer{}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(lb), current))
			current.Status.Endpoints[0].Addresses[0].Value = "192.0.2.99"
			require.NoError(t, base.Status().Update(ctx, current))
		}
		return cl.SubResource(sub).Patch(ctx, obj, patch, opts...)
	}})
	r := NetworkLoadBalancerDataPlaneReconciler{HubCluster: &fakeCluster{cl: hub}}
	require.NoError(t, retryLoadBalancerConflict(writer, base, func(cl client.Client) error {
		return r.reconcileDataPlane(ctx, "project", cl, client.ObjectKeyFromObject(lb))
	}))
	require.GreaterOrEqual(t, writes, 2)
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(lb), lb))
	require.Equal(t, "192.0.2.99", lb.Status.Endpoints[0].Addresses[0].Value)
}

func TestNLBRouteRetriesConcurrentMetadata(t *testing.T) {
	ctx := context.Background()
	route := testLoadBalancerRoute("route", time.Unix(10, 0))
	base := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).WithObjects(route, testRouteLoadBalancer(), testRouteNetworkService()).WithStatusSubresource(route).Build()
	writes := 0
	writer := interceptor.NewClient(base, interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, cl client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
		writes++
		if writes == 1 {
			current := &networkingv1alpha.NetworkLoadBalancerRoute{}
			require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(route), current))
			current.Annotations = map[string]string{"another-controller": "preserved"}
			require.NoError(t, base.Update(ctx, current))
		}
		return cl.SubResource(sub).Update(ctx, obj, opts...)
	}})
	r := &NetworkLoadBalancerRouteReconciler{}
	require.NoError(t, retryLoadBalancerConflict(writer, base, func(cl client.Client) error { return r.reconcileRoute(ctx, cl, client.ObjectKeyFromObject(route)) }))
	require.Equal(t, 2, writes)
	require.NoError(t, base.Get(ctx, client.ObjectKeyFromObject(route), route))
	require.Equal(t, "preserved", route.Annotations["another-controller"])
	require.NotEmpty(t, route.Status.Parents)
}

// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	vpcPodTestNamespace    = "ns-downstream"
	vpcPodTestClusterLabel = "cluster-project-a"
	vpcPodTestSliceName    = "vpc-us-central-1-pod-a"
)

// vpcPodTestOwnerLabels are the labels the downstream strategy stamps on what it
// writes for an upstream Gateway or HTTPRoute in namespace "default".
func vpcPodTestOwnerLabels(name string) map[string]string {
	return map[string]string{
		downstreamclient.UpstreamOwnerClusterNameLabel: vpcPodTestClusterLabel,
		downstreamclient.UpstreamOwnerNamespaceLabel:   "default",
		downstreamclient.UpstreamOwnerNameLabel:        name,
	}
}

func vpcPodTestGateway(name string) *gatewayv1.Gateway {
	return newGateway(config.NetworkServicesOperator{}, vpcPodTestNamespace, name, func(gw *gatewayv1.Gateway) {
		gw.Labels = vpcPodTestOwnerLabels(name)
	})
}

func vpcPodTestRoute(gatewayName string, backend gatewayv1.BackendObjectReference) *gatewayv1.HTTPRoute {
	return newHTTPRoute(vpcPodTestNamespace, gatewayName, func(route *gatewayv1.HTTPRoute) {
		route.Labels = vpcPodTestOwnerLabels(gatewayName)
		route.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(gatewayName)}}
		route.Spec.Rules = []gatewayv1.HTTPRouteRule{{
			BackendRefs: []gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{BackendObjectReference: backend}}},
		}}
	})
}

// vpcPodTestSliceRef is the backendRef passThroughVPCPodBackendRef writes for a
// VPC pod's slice.
func vpcPodTestSliceRef(name string) gatewayv1.BackendObjectReference {
	return gatewayv1.BackendObjectReference{
		Group:     ptr.To(gatewayv1.Group(discoveryv1.GroupName)),
		Kind:      ptr.To(gatewayv1.Kind(KindEndpointSlice)),
		Namespace: ptr.To(gatewayv1.Namespace(vpcPodTestNamespace)),
		Name:      gatewayv1.ObjectName(name),
		Port:      ptr.To(gatewayv1.PortNumber(8080)),
	}
}

// enqueuedForVPCPodSlice runs the downstream VPC-pod EndpointSlice handler for
// one created slice and returns what it enqueued.
func enqueuedForVPCPodSlice(t *testing.T, slice *discoveryv1.EndpointSlice, objs ...client.Object) []mcreconcile.Request {
	t.Helper()

	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))

	downstream := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objs...).Build()
	reconciler := &GatewayReconciler{}
	h := reconciler.listGatewaysForDownstreamVPCPodEndpointSlice("", &fakeCluster{cl: downstream})

	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[mcreconcile.Request]())
	t.Cleanup(queue.ShutDown)

	h.Create(context.Background(), event.TypedCreateEvent[*discoveryv1.EndpointSlice]{Object: slice}, queue)

	var reqs []mcreconcile.Request
	for queue.Len() > 0 {
		req, _ := queue.Get()
		queue.Done(req)
		reqs = append(reqs, req)
	}
	return reqs
}

func vpcPodTestSlice(labels map[string]string) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: vpcPodTestNamespace,
			Name:      vpcPodTestSliceName,
			Labels:    labels,
		},
		AddressType: discoveryv1.AddressTypeIPv6,
	}
}

// TestVPCPodEndpointSliceWakesOnlyGatewaysRoutingToIt covers what a VPC pod's
// slice changing on the hub reconciles: only the Gateways whose downstream
// routes name the slice.
func TestVPCPodEndpointSliceWakesOnlyGatewaysRoutingToIt(t *testing.T) {
	serviceRef := gatewayv1.BackendObjectReference{Name: "route-uid-rule-0-backendref-0", Port: ptr.To(gatewayv1.PortNumber(8080))}
	sameNameServiceRef := gatewayv1.BackendObjectReference{Name: vpcPodTestSliceName, Port: ptr.To(gatewayv1.PortNumber(8080))}
	sameNameElsewhereRef := vpcPodTestSliceRef(vpcPodTestSliceName)
	sameNameElsewhereRef.Namespace = ptr.To(gatewayv1.Namespace("ns-other"))

	reqs := enqueuedForVPCPodSlice(t,
		vpcPodTestSlice(map[string]string{VPCPodTenantIDLabel: "vpc-a-attachment-a"}),
		vpcPodTestGateway("uses-pod"),
		vpcPodTestRoute("uses-pod", vpcPodTestSliceRef(vpcPodTestSliceName)),
		vpcPodTestGateway("uses-service"),
		vpcPodTestRoute("uses-service", serviceRef),
		vpcPodTestGateway("uses-another-pod"),
		vpcPodTestRoute("uses-another-pod", vpcPodTestSliceRef("vpc-us-central-1-pod-b")),
		vpcPodTestGateway("uses-service-of-same-name"),
		vpcPodTestRoute("uses-service-of-same-name", sameNameServiceRef),
		vpcPodTestGateway("uses-same-name-elsewhere"),
		vpcPodTestRoute("uses-same-name-elsewhere", sameNameElsewhereRef),
		vpcPodTestGateway("gone-upstream"),
	)

	assert.Equal(t, []mcreconcile.Request{{
		Request:     ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "uses-pod"}},
		ClusterName: multicluster.ClusterName("project-a"),
	}}, reqs)
}

// TestVPCPodEndpointSliceWithoutTenantLabelWakesNothing covers the slices this
// handler is not for: an EndpointSlice the operator synthesized for a Service
// backend carries no tenant label, and its route is reached through the Service.
func TestVPCPodEndpointSliceWithoutTenantLabelWakesNothing(t *testing.T) {
	reqs := enqueuedForVPCPodSlice(t,
		vpcPodTestSlice(nil),
		vpcPodTestGateway("uses-pod"),
		vpcPodTestRoute("uses-pod", vpcPodTestSliceRef(vpcPodTestSliceName)),
	)

	assert.Empty(t, reqs)
}

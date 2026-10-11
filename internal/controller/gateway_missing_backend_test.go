// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

type missingBackendRig struct {
	t          *testing.T
	ctx        context.Context
	reconciler *GatewayReconciler
	upstream   client.Client
	downstream client.Client
	strategy   downstreamclient.ResourceStrategy
	gateway    *gatewayv1.Gateway
	hubGateway *gatewayv1.Gateway
}

func newMissingBackendRig(t *testing.T, routes ...*gatewayv1.HTTPRoute) *missingBackendRig {
	t.Helper()
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))

	testConfig := config.NetworkServicesOperator{Gateway: config.GatewayConfig{
		DownstreamGatewayClassName:            "test-suite",
		DownstreamHostnameAccountingNamespace: "default",
		TargetDomain:                          "test-suite.com",
	}}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}
	gateway := newGateway(testConfig, namespace.Name, "test")
	hubGateway := newGateway(testConfig, fmt.Sprintf("ns-%s", namespace.UID), gateway.Name)

	withStatus := make([]client.Object, 0, 1+len(routes))
	withStatus = append(withStatus, gateway)
	for _, route := range routes {
		route.SetCreationTimestamp(metav1.Now())
		withStatus = append(withStatus, route)
	}
	upstream := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(append([]client.Object{namespace}, withStatus...)...).
		WithStatusSubresource(withStatus...).
		Build()
	downstream := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(hubGateway).
		WithStatusSubresource(hubGateway, &gatewayv1.HTTPRoute{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				obj.SetUID(uuid.NewUUID())
				obj.SetCreationTimestamp(metav1.Now())
				return cl.Create(ctx, obj, opts...)
			},
		}).
		Build()

	return &missingBackendRig{
		t:   t,
		ctx: context.Background(),
		reconciler: &GatewayReconciler{
			mgr:               &fakeMockManager{cl: upstream},
			Config:            testConfig,
			DownstreamCluster: &fakeCluster{cl: downstream},
		},
		upstream:   upstream,
		downstream: downstream,
		strategy:   downstreamclient.NewMappedNamespaceResourceStrategy("test", upstream, downstream),
		gateway:    gateway,
		hubGateway: hubGateway,
	}
}

func (r *missingBackendRig) reconcileRoutes() {
	r.t.Helper()
	result := r.reconciler.ensureDownstreamGatewayHTTPRoutes(r.ctx, r.upstream, r.gateway, "test", r.hubGateway, r.strategy, nil, nil, nil)
	require.NoError(r.t, result.Err)
	_, err := result.Complete(r.ctx)
	require.NoError(r.t, err)
}

func (r *missingBackendRig) hubRoute(name string) *gatewayv1.HTTPRoute {
	r.t.Helper()
	var routes gatewayv1.HTTPRouteList
	require.NoError(r.t, r.downstream.List(r.ctx, &routes, client.MatchingLabels{downstreamclient.UpstreamOwnerNameLabel: name}))
	require.Len(r.t, routes.Items, 1, "hub copies of %s", name)
	return &routes.Items[0]
}

func (r *missingBackendRig) hubService(name gatewayv1.ObjectName) error {
	return r.downstream.Get(r.ctx, client.ObjectKey{Namespace: r.hubGateway.Namespace, Name: string(name)}, &corev1.Service{})
}

func (r *missingBackendRig) hubServicePorts(name gatewayv1.ObjectName) []corev1.ServicePort {
	r.t.Helper()
	var service corev1.Service
	require.NoError(r.t, r.downstream.Get(r.ctx, client.ObjectKey{Namespace: r.hubGateway.Namespace, Name: string(name)}, &service))
	return service.Spec.Ports
}

func (r *missingBackendRig) hubSlice(name gatewayv1.ObjectName) error {
	return r.downstream.Get(r.ctx, client.ObjectKey{Namespace: r.hubGateway.Namespace, Name: string(name)}, &discoveryv1.EndpointSlice{})
}

func (r *missingBackendRig) resolvedRefs(name string) *metav1.Condition {
	r.t.Helper()
	var route gatewayv1.HTTPRoute
	require.NoError(r.t, r.upstream.Get(r.ctx, client.ObjectKey{Namespace: r.gateway.Namespace, Name: name}, &route))
	for _, parent := range route.Status.Parents {
		if string(parent.ParentRef.Name) == r.gateway.Name {
			return apimeta.FindStatusCondition(parent.Conditions, string(gatewayv1.RouteConditionResolvedRefs))
		}
	}
	return nil
}

func routeToEndpointSlice(name, slice string) *gatewayv1.HTTPRoute {
	return newHTTPRoute("test", name, func(route *gatewayv1.HTTPRoute) {
		route.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: "test"}}
		route.Spec.Rules = []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Group: ptr.To(gatewayv1.Group(discoveryv1.GroupName)),
				Kind:  ptr.To(gatewayv1.Kind(KindEndpointSlice)),
				Name:  gatewayv1.ObjectName(slice),
				Port:  ptr.To(gatewayv1.PortNumber(8080)),
			},
		}}}}}
	})
}

func backendSlice(name string) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: "test", Name: name},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"192.0.2.10"}}},
		Ports: []discoveryv1.EndpointPort{
			{Name: ptr.To("http"), Protocol: ptr.To(corev1.ProtocolTCP), AppProtocol: ptr.To(SchemeHTTP), Port: ptr.To(int32(8080))},
		},
	}
}

func TestAMissingBackendLeavesTheRestOfTheGatewayConverging(t *testing.T) {
	rig := newMissingBackendRig(t, routeToEndpointSlice("missing", "gone"), routeToEndpointSlice("served", "present"))
	require.NoError(t, rig.upstream.Create(rig.ctx, backendSlice("present")))

	rig.reconcileRoutes()

	hubMissing := rig.hubRoute("missing")
	require.Len(t, hubMissing.Spec.Rules, 1)
	require.Len(t, hubMissing.Spec.Rules[0].BackendRefs, 1)
	unresolvedRef := hubMissing.Spec.Rules[0].BackendRefs[0]
	assert.Equal(t, KindService, string(ptr.Deref(unresolvedRef.Kind, "")))
	assert.NoError(t, rig.hubService(unresolvedRef.Name), "the missing backend lands on a Service")
	err := rig.hubSlice(unresolvedRef.Name)
	assert.Truef(t, apierrors.IsNotFound(err), "the Service has no endpoints, so the edge reads EndpointsNotFound, got %v", err)

	servedRef := rig.hubRoute("served").Spec.Rules[0].BackendRefs[0]
	assert.NoError(t, rig.hubService(servedRef.Name), "the route on the same Gateway still gets its backend")

	condition := rig.resolvedRefs("missing")
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, string(gatewayv1.RouteReasonBackendNotFound), condition.Reason)
	assert.Contains(t, condition.Message, "test/gone")
	assert.Nil(t, rig.resolvedRefs("served"), "a route whose slices exist gets no condition from this controller")

	require.NoError(t, rig.upstream.Create(rig.ctx, backendSlice("gone")))
	hubMissing = rig.hubRoute("missing")
	hubMissing.Status.Parents = []gatewayv1.RouteParentStatus{{
		ParentRef:      gatewayv1.ParentReference{Name: gatewayv1.ObjectName(rig.hubGateway.Name)},
		ControllerName: "gateway.envoyproxy.io/gatewayclass-controller",
		Conditions: []metav1.Condition{{
			Type: string(gatewayv1.RouteConditionResolvedRefs), Status: metav1.ConditionTrue,
			Reason: string(gatewayv1.RouteReasonResolvedRefs), LastTransitionTime: metav1.Now(),
		}},
	}}
	require.NoError(t, rig.downstream.Status().Update(rig.ctx, hubMissing))

	rig.reconcileRoutes()

	assert.NoError(t, rig.hubSlice(unresolvedRef.Name), "once the slice exists, the same Service gets its endpoints")
	ports := rig.hubServicePorts(unresolvedRef.Name)
	require.Len(t, ports, 1, "the placeholder port gives way to the slice's port")
	assert.Equal(t, "http", ports[0].Name)
	assert.NoError(t, rig.hubService(servedRef.Name))
	condition = rig.resolvedRefs("missing")
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status, "the route's references are reported resolved again")
}

func sliceRef(slice string, weight int32) gatewayv1.HTTPBackendRef {
	return gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{
		Weight: ptr.To(weight),
		BackendObjectReference: gatewayv1.BackendObjectReference{
			Group: ptr.To(gatewayv1.Group(discoveryv1.GroupName)),
			Kind:  ptr.To(gatewayv1.Kind(KindEndpointSlice)),
			Name:  gatewayv1.ObjectName(slice),
			Port:  ptr.To(gatewayv1.PortNumber(8080)),
		},
	}}
}

func TestMissingBackendsInARuleKeepTheirShares(t *testing.T) {
	route := newHTTPRoute("test", "split", func(route *gatewayv1.HTTPRoute) {
		route.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: "test"}}
		route.Spec.Rules = []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{
			sliceRef("present", 3), sliceRef("gone-a", 1), sliceRef("gone-b", 1),
		}}}
	})
	rig := newMissingBackendRig(t, route)
	require.NoError(t, rig.upstream.Create(rig.ctx, backendSlice("present")))

	rig.reconcileRoutes()

	refs := rig.hubRoute("split").Spec.Rules[0].BackendRefs
	require.Len(t, refs, 3, "every backend keeps its place in the rule")
	for i, weight := range []int32{3, 1, 1} {
		assert.Equal(t, weight, ptr.Deref(refs[i].Weight, 0), "backend %d keeps its weight", i)
		assert.Equal(t, KindService, string(ptr.Deref(refs[i].Kind, "")))
	}
	assert.NoError(t, rig.hubSlice(refs[0].Name))
	for _, ref := range refs[1:] {
		assert.NoError(t, rig.hubService(ref.Name))
		err := rig.hubSlice(ref.Name)
		assert.Truef(t, apierrors.IsNotFound(err), "a missing backend's Service has no endpoints, got %v", err)
	}

	condition := rig.resolvedRefs("split")
	require.NotNil(t, condition)
	assert.Equal(t, "EndpointSlice not found: test/gone-a, test/gone-b", condition.Message)
}

func TestASliceThatGoesAwayBecomesUnresolved(t *testing.T) {
	rig := newMissingBackendRig(t, routeToEndpointSlice("served", "present"))
	slice := backendSlice("present")
	require.NoError(t, rig.upstream.Create(rig.ctx, slice))
	rig.reconcileRoutes()
	ref := rig.hubRoute("served").Spec.Rules[0].BackendRefs[0]
	require.NoError(t, rig.hubService(ref.Name))

	require.NoError(t, rig.upstream.Get(rig.ctx, client.ObjectKeyFromObject(slice), slice))
	slice.Finalizers = nil
	require.NoError(t, rig.upstream.Update(rig.ctx, slice))
	require.NoError(t, rig.upstream.Delete(rig.ctx, slice))
	rig.reconcileRoutes()

	assert.Equal(t, ref, rig.hubRoute("served").Spec.Rules[0].BackendRefs[0], "the route keeps the same backendRef")
	err := rig.hubSlice(ref.Name)
	assert.Truef(t, apierrors.IsNotFound(err), "the gone slice's endpoints are removed, got %v", err)
	ports := rig.hubServicePorts(ref.Name)
	require.Len(t, ports, 1, "the slice's port gives way to the placeholder")
	assert.EqualValues(t, 8080, ports[0].Port)
	condition := rig.resolvedRefs("served")
	require.NotNil(t, condition)
	assert.Equal(t, string(gatewayv1.RouteReasonBackendNotFound), condition.Reason)
	assert.Contains(t, condition.Message, "test/present")
}

func TestALookupErrorOtherThanNotFoundStillFails(t *testing.T) {
	route := routeToEndpointSlice("unreadable", "present")
	rig := newMissingBackendRig(t, route)
	require.NoError(t, rig.upstream.Create(rig.ctx, backendSlice("present")))
	timeout := apierrors.NewServerTimeout(discoveryv1.Resource("endpointslices"), "get", 1)
	upstream := interceptor.NewClient(rig.upstream.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*discoveryv1.EndpointSlice); ok {
				return timeout
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	})

	_, _, unresolved, err := rig.reconciler.processDownstreamHTTPRouteRules(rig.ctx, upstream, rig.gateway, *route, rig.hubGateway, rig.strategy)
	assert.Truef(t, apierrors.IsServerTimeout(err), "expected the lookup's error, got %v", err)
	assert.Empty(t, unresolved)
}

func TestBackendNotFoundIsRetractedWhenTheEdgeHasNotReported(t *testing.T) {
	rig := newMissingBackendRig(t, routeToEndpointSlice("missing", "gone"))
	rig.reconcileRoutes()
	require.NotNil(t, rig.resolvedRefs("missing"))

	require.NoError(t, rig.upstream.Create(rig.ctx, backendSlice("gone")))
	rig.reconcileRoutes()

	assert.Nil(t, rig.resolvedRefs("missing"), "a BackendNotFound no source backs any more is removed")
}

func TestAPassThroughBackendIsWrittenBeforeItsHubSlice(t *testing.T) {
	rig := newMissingBackendRig(t, routeToEndpointSlice("vpc", "vpc-pod-1"))
	slice := backendSlice("vpc-pod-1")
	slice.Labels = map[string]string{VPCPodTenantIDLabel: "tenant-1"}
	require.NoError(t, rig.upstream.Create(rig.ctx, slice))

	rig.reconcileRoutes()

	ref := rig.hubRoute("vpc").Spec.Rules[0].BackendRefs[0]
	assert.Equal(t, KindEndpointSlice, string(ptr.Deref(ref.Kind, "")))
	assert.Equal(t, rig.hubGateway.Namespace, string(ptr.Deref(ref.Namespace, "")))
	assert.Equal(t, "vpc-pod-1", string(ref.Name), "the route names the hub slice the VPC pod mapper wakes it for")
	assert.Nil(t, rig.resolvedRefs("vpc"), "the route's own slice exists; the edge reports a hub slice it cannot find")
}

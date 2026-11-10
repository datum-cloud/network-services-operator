// SPDX-License-Identifier: AGPL-3.0-only

package retrigger

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

const (
	testTenant     = "vpc1-attach1"
	testMember     = "fd00::10"
	testOtherProxy = "proxy-2"
)

func vpcScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := testScheme(t)
	require.NoError(t, discoveryv1.AddToScheme(s))
	return s
}

func networkServiceProxy(name string) *networkingv1alpha.HTTPProxy {
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
					NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: "svc"},
				}},
			}},
		},
	}
}

func backendCopy(proxy string, addresses ...string) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "copy-" + proxy,
			Namespace: testNS,
			Labels:    map[string]string{downstreamclient.UpstreamOwnerNameLabel: proxy + "-0-0"},
		},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: addresses}},
	}
}

func tenantSlice(name string, addresses ...string) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "galactic-system",
			Labels:    map[string]string{extcache.VPCPodTenantIDLabel: testTenant},
		},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: addresses}},
	}
}

func reconcileBinding(t *testing.T, cl client.Client) {
	t.Helper()
	r := &VPCBindingReconciler{Client: cl}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testProxy},
	})
	require.NoError(t, err)
}

func gatewayBinding(t *testing.T, cl client.Client) string {
	t.Helper()
	var gw gatewayv1.Gateway
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testProxy}, &gw))
	return gw.Annotations[VPCBindingAnnotationKey]
}

func TestVPCBinding_LateTenantSliceChangesGatewayAnnotation(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(vpcScheme(t)).
		WithObjects(networkServiceProxy(testProxy), gateway(), backendCopy(testProxy, testMember)).
		Build()

	reconcileBinding(t, cl)
	unbound := gatewayBinding(t, cl)
	require.NotEmpty(t, unbound)

	require.NoError(t, cl.Create(context.Background(), tenantSlice("vpc-us-east-1-pod", "fd00:0::10")))
	reconcileBinding(t, cl)
	bound := gatewayBinding(t, cl)
	assert.NotEqual(t, unbound, bound)

	reconcileBinding(t, cl)
	assert.Equal(t, bound, gatewayBinding(t, cl))
}

func TestVPCBinding_InstanceBackendResolvesFromNamedSlice(t *testing.T) {
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: testProxy, Namespace: testNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
					Instance: &networkingv1alpha.InstanceBackendRef{Name: "pod-slice", Port: 8080},
				}},
			}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(vpcScheme(t)).WithObjects(proxy, gateway()).Build()

	tenants, err := extcache.VPCBackendTenants(context.Background(), cl, proxy)
	require.NoError(t, err)
	assert.Equal(t, map[extcache.VPCPodBackendKey]string{
		{Namespace: testNS, HTTPProxyName: testProxy}: "",
	}, tenants)

	slice := tenantSlice("pod-slice", testMember)
	slice.Namespace = testNS
	require.NoError(t, cl.Create(context.Background(), slice))
	tenants, err = extcache.VPCBackendTenants(context.Background(), cl, proxy)
	require.NoError(t, err)
	assert.Equal(t, map[extcache.VPCPodBackendKey]string{
		{Namespace: testNS, HTTPProxyName: testProxy}: testTenant,
	}, tenants)

	r := &VPCBindingReconciler{Client: cl}
	requests := r.proxiesForEndpointSlice(context.Background(), slice)
	assert.Equal(t, []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: testNS, Name: testProxy}}}, requests)
}

func TestVPCBinding_TenantSliceMapsOnlyToProxiesSharingAMember(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(vpcScheme(t)).
		WithObjects(
			networkServiceProxy(testProxy), backendCopy(testProxy, testMember),
			networkServiceProxy(testOtherProxy), backendCopy(testOtherProxy, "fd00::99"),
		).
		Build()

	r := &VPCBindingReconciler{Client: cl}
	requests := r.proxiesForEndpointSlice(context.Background(), tenantSlice("vpc-us-east-1-pod", testMember))
	assert.Equal(t, []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: testNS, Name: testProxy}}}, requests)

	requests = r.proxiesForEndpointSlice(context.Background(), backendCopy(testOtherProxy, "fd00::99"))
	assert.Equal(t, []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: testNS, Name: testOtherProxy}}}, requests)
}

func TestVPCBinding_ProxyWithoutVPCBackendIgnored(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(vpcScheme(t)).
		WithObjects(proxyRefersConnector(), gateway()).
		Build()

	assert.False(t, vpcBackendPredicate().Create(event.CreateEvent{Object: proxyRefersConnector()}))
	reconcileBinding(t, cl)
	assert.Empty(t, gatewayBinding(t, cl))
}

func TestVPCBinding_NoGateway_NoError(t *testing.T) {
	cl := fake.NewClientBuilder().WithScheme(vpcScheme(t)).
		WithObjects(networkServiceProxy(testProxy)).
		Build()
	reconcileBinding(t, cl)
}

func TestVPCBinding_EndpointSlicePredicate(t *testing.T) {
	p := endpointSlicePredicate()
	plain := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: "svc-abc", Namespace: testNS}}
	tenant := tenantSlice("vpc-us-east-1-pod", testMember)

	assert.False(t, p.Create(event.CreateEvent{Object: plain}))
	assert.True(t, p.Create(event.CreateEvent{Object: tenant}))
	assert.True(t, p.Create(event.CreateEvent{Object: backendCopy(testProxy, testMember)}))
	assert.True(t, p.Delete(event.DeleteEvent{Object: tenant}))

	conditionsOnly := tenant.DeepCopy()
	ready := true
	conditionsOnly.Endpoints[0].Conditions.Ready = &ready
	assert.False(t, p.Update(event.UpdateEvent{ObjectOld: tenant, ObjectNew: conditionsOnly}))

	moved := tenant.DeepCopy()
	moved.Endpoints[0].Addresses = []string{"fd00::11"}
	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: tenant, ObjectNew: moved}))

	relabelled := tenant.DeepCopy()
	relabelled.Labels[extcache.VPCPodTenantIDLabel] = "vpc2-attach1"
	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: tenant, ObjectNew: relabelled}))

	assert.False(t, p.Update(event.UpdateEvent{ObjectOld: plain, ObjectNew: plain.DeepCopy()}))
}

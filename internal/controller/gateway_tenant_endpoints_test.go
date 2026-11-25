// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func tenantSlice(addressType discoveryv1.AddressType, labels map[string]string, addresses ...string) *discoveryv1.EndpointSlice {
	slice := backendSlice("tenant")
	slice.Labels = labels
	slice.AddressType = addressType
	slice.Endpoints = []discoveryv1.Endpoint{{Addresses: addresses}}
	return slice
}

func networkInterfaceHolding(namespace, name, address string) *networkingv1alpha.NetworkInterface {
	return &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Status:     networkingv1alpha.NetworkInterfaceStatus{Phase: networkingv1alpha.NetworkInterfacePhaseBound},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network: networkingv1alpha.LocalNetworkRef{Name: "default"},
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{
				{Family: networkingv1alpha.IPv6Protocol, Address: address + "/128", Primary: true},
			},
		},
	}
}

func TestTenantEndpointSliceAddressesAreChecked(t *testing.T) {
	ipv4, ipv6, fqdn := discoveryv1.AddressTypeIPv4, discoveryv1.AddressTypeIPv6, discoveryv1.AddressTypeFQDN

	tests := []struct {
		name       string
		slice      *discoveryv1.EndpointSlice
		interfaces []client.Object
		refused    string
	}{
		{name: "loopback", slice: tenantSlice(ipv4, nil, "127.0.0.1"), refused: "127.0.0.1"},
		{name: "private", slice: tenantSlice(ipv4, nil, "10.1.2.3"), refused: "10.1.2.3"},
		{name: "link-local", slice: tenantSlice(ipv4, nil, "169.254.169.254"), refused: "169.254.169.254"},
		{name: "carrier-grade NAT", slice: tenantSlice(ipv4, nil, "100.64.0.1"), refused: "100.64.0.1"},
		{name: "IPv6 loopback", slice: tenantSlice(ipv6, nil, "::1"), refused: "::1"},
		{name: "IPv6 unique local", slice: tenantSlice(ipv6, nil, "fd00::1"), refused: "fd00::1"},
		{name: "IPv4-mapped loopback", slice: tenantSlice(ipv6, nil, "::ffff:127.0.0.1"), refused: "::ffff:127.0.0.1"},
		{name: "cluster name", slice: tenantSlice(fqdn, nil, "kubernetes.default.svc.cluster.local"), refused: "kubernetes.default.svc.cluster.local"},
		{name: "short name", slice: tenantSlice(fqdn, nil, "kubernetes.default"), refused: "kubernetes.default"},
		{name: "one internal address among public ones", slice: tenantSlice(ipv4, nil, "8.8.8.8", "10.0.0.1"), refused: "10.0.0.1"},
		{
			name:       "an address held by an interface in another project",
			slice:      tenantSlice(ipv6, nil, "fd20:0:2::7"),
			interfaces: []client.Object{networkInterfaceHolding("other", "vm-1", "fd20:0:2::7")},
			refused:    "fd20:0:2::7",
		},
		{
			name:       "an address held by a released interface",
			slice:      tenantSlice(ipv6, nil, "fd20:0:2::7"),
			interfaces: []client.Object{releasedNetworkInterfaceHolding("test", "vm-1", "fd20:0:2::7")},
			refused:    "fd20:0:2::7",
		},
		{name: "public address", slice: tenantSlice(ipv4, nil, "8.8.8.8")},
		{name: "public IPv6 address", slice: tenantSlice(ipv6, nil, "2001:4860:4860::8888")},
		{name: "public name", slice: tenantSlice(fqdn, nil, "origin.example.com")},
		{
			name:       "an address in another spelling than the held one",
			slice:      tenantSlice(ipv6, nil, "FD20:0:2:0:0:0:0:7"),
			interfaces: []client.Object{networkInterfaceHolding("test", "vm-1", "fd20:0:2::7")},
		},
		{name: "connector placeholder", slice: tenantSlice(fqdn, nil, connectorPlaceholderHost)},
		{
			name:       "network service member address",
			slice:      tenantSlice(ipv6, map[string]string{NetworkServiceBackendLabel: "storefront"}, "fd20:0:2::7"),
			interfaces: []client.Object{networkInterfaceHolding("test", "vm-1", "fd20:0:2::7")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newMissingBackendRig(t, routeToEndpointSlice("route", "tenant"))
			for _, iface := range tt.interfaces {
				require.NoError(t, rig.upstream.Create(rig.ctx, iface))
			}
			require.NoError(t, rig.upstream.Create(rig.ctx, tt.slice))

			rig.reconcileRoutes()

			ref := rig.hubRoute("route").Spec.Rules[0].BackendRefs[0]
			assert.NoError(t, rig.hubService(ref.Name), "the backend keeps its place on a Service")
			condition := rig.resolvedRefs("route")

			if tt.refused == "" {
				var slice discoveryv1.EndpointSlice
				require.NoError(t, rig.downstream.Get(rig.ctx, client.ObjectKey{Namespace: rig.hubGateway.Namespace, Name: string(ref.Name)}, &slice))
				assert.Equal(t, tt.slice.Endpoints, slice.Endpoints)
				assert.Nil(t, condition)
				return
			}

			err := rig.hubSlice(ref.Name)
			assert.Truef(t, apierrors.IsNotFound(err), "a refused slice's addresses never reach the edge, got %v", err)
			require.NotNil(t, condition)
			assert.Equal(t, metav1.ConditionFalse, condition.Status)
			assert.Equal(t, string(gatewayv1.RouteReasonRefNotPermitted), condition.Reason)
			assert.Contains(t, condition.Message, "test/tenant")
			assert.Contains(t, condition.Message, tt.refused)
		})
	}
}

func TestATenantIDLabelCarriesNoAddressesToTheEdge(t *testing.T) {
	rig := newMissingBackendRig(t, routeToEndpointSlice("route", "tenant"))
	require.NoError(t, rig.upstream.Create(rig.ctx, tenantSlice(discoveryv1.AddressTypeIPv4, map[string]string{VPCPodTenantIDLabel: "tenant-1"}, "8.8.8.8")))

	rig.reconcileRoutes()

	ref := rig.hubRoute("route").Spec.Rules[0].BackendRefs[0]
	assert.Equal(t, KindEndpointSlice, string(ptr.Deref(ref.Kind, "")))
	assert.Equal(t, rig.hubGateway.Namespace, string(ptr.Deref(ref.Namespace, "")), "the reference stays in the project's own namespace")
	var slices discoveryv1.EndpointSliceList
	require.NoError(t, rig.downstream.List(rig.ctx, &slices))
	assert.Empty(t, slices.Items, "nothing from the tenant's slice is copied to the hub")
}

func TestARefusedSliceRecoversOnceItsAddressesAreFixed(t *testing.T) {
	rig := newMissingBackendRig(t, routeToEndpointSlice("route", "tenant"))
	slice := tenantSlice(discoveryv1.AddressTypeIPv4, nil, "8.8.8.8")
	require.NoError(t, rig.upstream.Create(rig.ctx, slice))
	rig.reconcileRoutes()
	ref := rig.hubRoute("route").Spec.Rules[0].BackendRefs[0]
	require.NoError(t, rig.hubSlice(ref.Name))

	require.NoError(t, rig.upstream.Get(rig.ctx, client.ObjectKeyFromObject(slice), slice))
	slice.Endpoints = []discoveryv1.Endpoint{{Addresses: []string{"127.0.0.1"}}}
	require.NoError(t, rig.upstream.Update(rig.ctx, slice))
	rig.reconcileRoutes()

	err := rig.hubSlice(ref.Name)
	assert.Truef(t, apierrors.IsNotFound(err), "the edge copy is withdrawn once the slice turns internal, got %v", err)
	require.NotNil(t, rig.resolvedRefs("route"))

	require.NoError(t, rig.upstream.Get(rig.ctx, client.ObjectKeyFromObject(slice), slice))
	slice.Endpoints = []discoveryv1.Endpoint{{Addresses: []string{"8.8.4.4"}}}
	require.NoError(t, rig.upstream.Update(rig.ctx, slice))
	rig.reconcileRoutes()

	assert.NoError(t, rig.hubSlice(ref.Name))
	assert.Nil(t, rig.resolvedRefs("route"), "the refusal is retracted")
}

func TestALabelledSliceWithAnInternalAddressIsRefused(t *testing.T) {
	rig := newMissingBackendRig(t, routeToEndpointSlice("route", "tenant"))
	require.NoError(t, rig.upstream.Create(rig.ctx, tenantSlice(discoveryv1.AddressTypeIPv4, map[string]string{VPCPodTenantIDLabel: "tenant-1"}, "127.0.0.1")))

	rig.reconcileRoutes()

	ref := rig.hubRoute("route").Spec.Rules[0].BackendRefs[0]
	assert.Equal(t, KindService, string(ptr.Deref(ref.Kind, "")), "a refused slice is not passed through")
	condition := rig.resolvedRefs("route")
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, string(gatewayv1.RouteReasonRefNotPermitted), condition.Reason)
	assert.Contains(t, condition.Message, "127.0.0.1")
}

func releasedNetworkInterfaceHolding(namespace, name, address string) *networkingv1alpha.NetworkInterface {
	iface := networkInterfaceHolding(namespace, name, address)
	iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseAvailable
	return iface
}

func TestAFailedInterfaceLookupRefusesOnlyThatBackend(t *testing.T) {
	rig := newMissingBackendRig(t, routeToEndpointSlice("refused", "tenant"), routeToEndpointSlice("served", "present"))
	require.NoError(t, rig.upstream.Create(rig.ctx, tenantSlice(discoveryv1.AddressTypeIPv6, nil, "fd20:0:2::7")))
	require.NoError(t, rig.upstream.Create(rig.ctx, backendSlice("present")))
	rig.upstream = interceptor.NewClient(rig.upstream.(client.WithWatch), interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*networkingv1alpha.NetworkInterfaceList); ok {
				return errors.New("injected list failure")
			}
			return cl.List(ctx, list, opts...)
		},
	})

	rig.reconcileRoutes()

	refused := rig.resolvedRefs("refused")
	require.NotNil(t, refused)
	assert.Equal(t, metav1.ConditionFalse, refused.Status)
	assert.Equal(t, string(gatewayv1.RouteReasonRefNotPermitted), refused.Reason)
	assert.Contains(t, refused.Message, "injected list failure")
	assert.Nil(t, rig.resolvedRefs("served"))
	servedRef := rig.hubRoute("served").Spec.Rules[0].BackendRefs[0]
	assert.NoError(t, rig.hubSlice(servedRef.Name), "the other route is still programmed")
}

func TestAMalformedTenantIDLabelIsRefused(t *testing.T) {
	for _, tenantID := range []string{"nodash", "-attachment", "vpc-"} {
		t.Run(tenantID, func(t *testing.T) {
			rig := newMissingBackendRig(t, routeToEndpointSlice("route", "tenant"))
			require.NoError(t, rig.upstream.Create(rig.ctx, tenantSlice(discoveryv1.AddressTypeIPv4, map[string]string{VPCPodTenantIDLabel: tenantID}, "8.8.8.8")))

			rig.reconcileRoutes()

			ref := rig.hubRoute("route").Spec.Rules[0].BackendRefs[0]
			assert.Equal(t, KindService, string(ptr.Deref(ref.Kind, "")), "a refused slice is not passed through")
			condition := rig.resolvedRefs("route")
			require.NotNil(t, condition)
			assert.Equal(t, metav1.ConditionFalse, condition.Status)
			assert.Equal(t, string(gatewayv1.RouteReasonRefNotPermitted), condition.Reason)
			assert.Contains(t, condition.Message, "test/tenant")
		})
	}
}

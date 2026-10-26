// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"testing"

	"github.com/stretchr/testify/require"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

func TestNetworkLoadBalancerCapabilityValidation(t *testing.T) {
	lb := &networkingv1alpha.NetworkLoadBalancer{}
	lb.Spec.Exposure.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressRequest{{
		Name: "regional", Families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
	}}
	lb.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{{
		Name: "game", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, Port: 5001, AddressRefs: []string{"regional"},
	}}
	require.NoError(t, validateNetworkLoadBalancerCapabilities(lb))
	lb.Spec.Listeners[0].AddressRefs = []string{"regional", "second"}
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb), "multi-VIP serving")
	lb.Spec.Listeners[0].AddressRefs = []string{"regional"}
	lb.Spec.Exposure.Addresses[0].Families = append(lb.Spec.Exposure.Addresses[0].Families, networkingv1alpha.IPv4Protocol)
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb), "UnsupportedCapability")
	lb.Spec.Exposure.Addresses[0].Families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	lb.Spec.Listeners[0].Protocol = networkingv1alpha.NetworkLoadBalancerProtocolTLS
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb), "proxy-capable")
	lb.Spec.Listeners[0].Protocol = networkingv1alpha.NetworkLoadBalancerProtocolTCP
	lb.Spec.Listeners = append(lb.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{
		Name: "other", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, Port: 4443, AddressRefs: []string{"regional"},
	})
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb), "shared")
}

func TestNetworkLoadBalancerRouteCapabilityValidation(t *testing.T) {
	route := &networkingv1alpha.NetworkLoadBalancerRoute{}
	route.Spec.Backends = []networkingv1alpha.NetworkLoadBalancerRouteBackend{{
		NetworkService: networkingv1alpha.NetworkLoadBalancerNetworkServiceRef{Name: "game", Port: "game"},
	}}
	require.NoError(t, validateNetworkLoadBalancerRouteCapabilities(route))
	interval := int32(5)
	route.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "TCP", IntervalSeconds: &interval}
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route), "custom health timing")
	route.Spec.Backends[0].HealthCheck = nil
	route.Spec.Backends = append(route.Spec.Backends, route.Spec.Backends[0])
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route), "multiple backends")
	route.Spec.Backends = route.Spec.Backends[:1]
	route.Spec.Backends[0].Transport = &networkingv1alpha.NetworkLoadBalancerBackendTransport{Protocol: "TLS"}
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route), "backend TLS")
}

func TestNetworkLoadBalancerRouteRejectsDuplicateParents(t *testing.T) {
	route := &networkingv1alpha.NetworkLoadBalancerRoute{}
	route.Namespace = "games"
	route.Spec.ParentRefs = []networkingv1alpha.NetworkLoadBalancerRouteParentRef{
		{Name: "edge", Listener: "tcp"},
		{Name: "edge", Listener: "tcp", Namespace: "games"},
	}
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route), "Duplicate value")
	route.Spec.ParentRefs[1].Listener = "udp"
	require.NoError(t, validateNetworkLoadBalancerRouteCapabilities(route))
}

func TestNLBSharedRangeCapabilityAndQuota(t *testing.T) {
	lb := &networkingv1alpha.NetworkLoadBalancer{}
	lb.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{{Name: "web", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"public"}}, {Name: "media", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, PortRange: &networkingv1alpha.PortRange{Start: 10000, End: 10127}, AddressRefs: []string{"public"}}}
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb), "not enabled")
	cfg := config.NetworkLoadBalancerConfig{SharedAddressListeners: true, UDPPortRanges: true, MaxExposedPorts: 129}
	require.NoError(t, validateNetworkLoadBalancerCapabilities(lb, cfg))
	cfg.MaxExposedPorts = 128
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb, cfg), "capacity")
	cfg.MaxExposedPorts = 129
	cfg.UDPPortRanges = false
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb, cfg), "UDP port ranges")
}

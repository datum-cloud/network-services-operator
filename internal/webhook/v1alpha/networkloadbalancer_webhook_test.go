// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
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
	validator := &NetworkLoadBalancerValidator{}
	validateNetworkLoadBalancerCapabilities := validator.validateCapabilities
	require.NoError(t, validateNetworkLoadBalancerCapabilities(lb))
	lb.Spec.Listeners[0].AddressRefs = []string{"regional", "second"}
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb), "multi-VIP serving")
	lb.Spec.Listeners[0].AddressRefs = []string{"regional"}
	lb.Spec.Exposure.Addresses[0].Families = append(lb.Spec.Exposure.Addresses[0].Families, networkingv1alpha.IPv4Protocol)
	require.ErrorContains(t, validateNetworkLoadBalancerCapabilities(lb), "UnsupportedCapability")
	validator.IPv4Enabled = true
	require.NoError(t, validateNetworkLoadBalancerCapabilities(lb))
	validator.IPv4Enabled = false
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
	require.NoError(t, validateNetworkLoadBalancerRouteCapabilities(route, nil))
	interval := int32(5)
	route.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "TCP", IntervalSeconds: &interval}
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route, nil), "custom health timing")
	for _, protocol := range []string{"HTTP", "HTTPS"} {
		route.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: protocol, Port: "health", Path: "/"}
		require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route, nil), "TCP health checks only")
	}
	route.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Port: "health"}
	require.NoError(t, validateNetworkLoadBalancerRouteCapabilities(route, nil))
	route.Spec.Backends[0].HealthCheck = nil
	route.Spec.Backends = append(route.Spec.Backends, route.Spec.Backends[0])
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route, nil), "multiple backends")
	route.Spec.Backends = route.Spec.Backends[:1]
	route.Spec.Backends[0].Transport = &networkingv1alpha.NetworkLoadBalancerBackendTransport{Protocol: "TLS"}
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route, nil), "backend TLS")
}

func TestNetworkLoadBalancerRouteRejectsDuplicateParents(t *testing.T) {
	route := &networkingv1alpha.NetworkLoadBalancerRoute{}
	route.Namespace = "games"
	route.Spec.ParentRefs = []networkingv1alpha.NetworkLoadBalancerRouteParentRef{
		{Name: "edge", Listener: "tcp"},
		{Name: "edge", Listener: "tcp", Namespace: "games"},
	}
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route, nil), "Duplicate value")
	route.Spec.ParentRefs[1].Listener = "udp"
	require.NoError(t, validateNetworkLoadBalancerRouteCapabilities(route, nil))
}

func TestNetworkLoadBalancerRouteKeepsExistingHealthCheckProtocol(t *testing.T) {
	route := &networkingv1alpha.NetworkLoadBalancerRoute{}
	route.Spec.Backends = []networkingv1alpha.NetworkLoadBalancerRouteBackend{{
		NetworkService: networkingv1alpha.NetworkLoadBalancerNetworkServiceRef{Name: "game", Port: "game"},
		HealthCheck:    &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "HTTP", Port: "health", Path: "/"},
	}}
	old := route.DeepCopy()
	route.Finalizers = []string{"networking.datumapis.com/test"}
	require.NoError(t, validateNetworkLoadBalancerRouteCapabilities(route, old))

	old.Spec.Backends[0].HealthCheck.Protocol = "TCP"
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route, old), "TCP health checks only")

	route.Spec.Backends[0].HealthCheck.Protocol = "HTTPS"
	old.Spec.Backends[0].HealthCheck.Protocol = "HTTP"
	require.ErrorContains(t, validateNetworkLoadBalancerRouteCapabilities(route, old), "TCP health checks only")
}

func TestNLBSharedRangeCapabilityAndQuota(t *testing.T) {
	lb := &networkingv1alpha.NetworkLoadBalancer{}
	lb.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{{Name: "web", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"public"}}, {Name: "media", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, PortRange: &networkingv1alpha.PortRange{Start: 10000, End: 10127}, AddressRefs: []string{"public"}}}
	validator := &NetworkLoadBalancerValidator{}
	require.ErrorContains(t, validator.validateCapabilities(lb), "not enabled")
	cfg := config.NetworkLoadBalancerConfig{SharedAddressListeners: true, UDPPortRanges: true, MaxExposedPorts: 129}
	validator.Config = cfg
	require.NoError(t, validator.validateCapabilities(lb))
	cfg.MaxExposedPorts = 128
	validator.Config = cfg
	require.ErrorContains(t, validator.validateCapabilities(lb), "capacity")
	cfg.MaxExposedPorts = 129
	cfg.UDPPortRanges = false
	validator.Config = cfg
	require.ErrorContains(t, validator.validateCapabilities(lb), "UDP port ranges")
}

func TestNLBDisabledGatesAllowOnlyNonExpandingUpdates(t *testing.T) {
	old := &networkingv1alpha.NetworkLoadBalancer{}
	old.Name = "media"
	old.Finalizers = []string{"networking.datumapis.com/networkloadbalancer-release"}
	old.Spec.Exposure.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressRequest{{Name: "public", Families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, Locations: &networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceNames, Names: []string{"iad", "fra"}}}}
	old.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{
		{Name: "control", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"public"}},
		{Name: "media", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, PortRange: &networkingv1alpha.PortRange{Start: 10000, End: 10127}, AddressRefs: []string{"public"}},
	}
	on := &NetworkLoadBalancerValidator{Config: config.NetworkLoadBalancerConfig{SharedAddressListeners: true, UDPPortRanges: true}}
	_, err := on.ValidateCreate(context.Background(), old)
	require.NoError(t, err)
	off := &NetworkLoadBalancerValidator{Config: config.NetworkLoadBalancerConfig{MaxExposedPorts: 64}}
	_, err = off.ValidateCreate(context.Background(), old)
	require.Error(t, err)
	for _, tc := range []struct {
		name    string
		change  func(*networkingv1alpha.NetworkLoadBalancer)
		allowed bool
	}{
		{"FinalizerRemoval", func(n *networkingv1alpha.NetworkLoadBalancer) { n.Finalizers = nil }, true},
		{"Metadata", func(n *networkingv1alpha.NetworkLoadBalancer) { n.Labels = map[string]string{"drain": "true"} }, true},
		{"RemoveListener", func(n *networkingv1alpha.NetworkLoadBalancer) { n.Spec.Listeners = n.Spec.Listeners[1:] }, true},
		{"NarrowRange", func(n *networkingv1alpha.NetworkLoadBalancer) { n.Spec.Listeners[1].PortRange.End-- }, true},
		{"RangeToSingleton", func(n *networkingv1alpha.NetworkLoadBalancer) {
			n.Spec.Listeners[1].Port = 10000
			n.Spec.Listeners[1].PortRange = nil
		}, true},
		{"RemoveLocation", func(n *networkingv1alpha.NetworkLoadBalancer) {
			n.Spec.Exposure.Addresses[0].Locations.Names = []string{"iad"}
		}, true},
		{"WidenRange", func(n *networkingv1alpha.NetworkLoadBalancer) { n.Spec.Listeners[1].PortRange.End++ }, false},
		{"MoveRange", func(n *networkingv1alpha.NetworkLoadBalancer) {
			n.Spec.Listeners[1].PortRange.Start = 20000
			n.Spec.Listeners[1].PortRange.End = 20001
		}, false},
		{"NewListener", func(n *networkingv1alpha.NetworkLoadBalancer) { n.Spec.Listeners[1].Name = "new-media" }, false},
		{"NewLocation", func(n *networkingv1alpha.NetworkLoadBalancer) {
			n.Spec.Exposure.Addresses[0].Locations.Names = append(n.Spec.Exposure.Addresses[0].Locations.Names, "ord")
		}, false},
		{"BroaderLocationMode", func(n *networkingv1alpha.NetworkLoadBalancer) {
			n.Spec.Exposure.Addresses[0].Locations = &networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll}
		}, false},
		{"NewSharedAddress", func(n *networkingv1alpha.NetworkLoadBalancer) {
			n.Spec.Exposure.Addresses[0].Name = "other"
			for i := range n.Spec.Listeners {
				n.Spec.Listeners[i].AddressRefs = []string{"other"}
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := old.DeepCopy()
			tc.change(next)
			_, err := off.ValidateUpdate(context.Background(), old, next)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestNLBIPv4AdmissionKeepsCoordinationIPv6Only(t *testing.T) {
	validator := &NetworkLoadBalancerValidator{
		IPv4Enabled: true,
		Config:      config.NetworkLoadBalancerConfig{SharedAddressListeners: true, UDPPortRanges: true},
	}
	lb := &networkingv1alpha.NetworkLoadBalancer{}
	lb.Spec.Exposure.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressRequest{{
		Name: "public", Families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol},
	}}
	lb.Spec.Listeners = []networkingv1alpha.NetworkLoadBalancerListener{{
		Name: "media", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, Port: 10000, AddressRefs: []string{"public"},
	}}
	_, err := validator.ValidateCreate(context.Background(), lb)
	require.NoError(t, err)
	old := lb.DeepCopy()
	lb.Spec.Listeners[0].Port = 0
	lb.Spec.Listeners[0].PortRange = &networkingv1alpha.PortRange{Start: 10000, End: 10127}
	_, err = validator.ValidateUpdate(context.Background(), old, lb)
	require.ErrorContains(t, err, "IPv6-only")
	lb.Spec.Listeners[0].Port, lb.Spec.Listeners[0].PortRange = 10000, nil
	lb.Spec.Listeners = append(lb.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{
		Name: "control", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"public"},
	})
	_, err = validator.ValidateCreate(context.Background(), lb)
	require.ErrorContains(t, err, "IPv6-only")
	lb.Spec.Exposure.Addresses[0].Families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}
	_, err = validator.ValidateCreate(context.Background(), lb)
	require.NoError(t, err)
}

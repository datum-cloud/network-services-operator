// SPDX-License-Identifier: AGPL-3.0-only
package spec

import (
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUDPRangeBackend(t *testing.T) {
	l, err := Listener("media", "udp", 0, &api.PortRange{Start: 10000, End: 10127}, []string{"public"})
	require.NoError(t, err)
	b, err := Backend("media", "media", "tcp", "health", "")
	require.NoError(t, err)
	s := &api.NetworkService{Spec: api.NetworkServiceSpec{Ports: []api.NetworkServicePort{{Name: "media", Protocol: "UDP", PortRange: &api.PortRange{Start: 10000, End: 10200}}, {Name: "health", Port: 8080, Protocol: "TCP"}}}}
	require.NoError(t, ValidateBackend(l, b, s))
	s.Spec.Ports[0].PortRange.Start = 10001
	require.ErrorContains(t, ValidateBackend(l, b, s), "cover")
	s.Spec.Ports[0].PortRange.Start = 10000
	b.HealthCheck = nil
	require.ErrorContains(t, ValidateBackend(l, b, s), "health check")
	b.HealthCheck = &api.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Port: "media"}
	require.ErrorContains(t, ValidateBackend(l, b, s), "single TCP")
}
func TestOverlapAndBoundaries(t *testing.T) {
	for _, value := range []string{"0-1", "2-1", "1-65536", "abc", "1-2-3"} {
		_, err := ParseRange(value)
		require.Error(t, err)
	}
	r, err := ParseRange("1-65535")
	require.NoError(t, err)
	require.Equal(t, int32(65535), r.End)
	a, _ := Listener("a", "udp", 0, &api.PortRange{Start: 10, End: 20}, []string{"public"})
	b, _ := Listener("b", "udp", 20, nil, []string{"public"})
	n := &api.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: api.NetworkLoadBalancerSpec{NetworkRef: api.LocalNetworkRef{Name: "default"}, Exposure: api.NetworkLoadBalancerExposure{Addresses: []api.NetworkLoadBalancerAddressRequest{{Name: "public"}}}, Listeners: []api.NetworkLoadBalancerListener{a, b}}}
	require.ErrorContains(t, ValidateNLB(n), "overlap")
	n.Spec.Listeners[1].Protocol = "TCP"
	require.NoError(t, ValidateNLB(n))
}
func condition(kind string, g int64) metav1.Condition {
	return metav1.Condition{Type: kind, Status: metav1.ConditionTrue, ObservedGeneration: g}
}
func TestReadinessRequiresEachListenerFamilyAndGeneration(t *testing.T) {
	n := &api.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{Name: "media", Generation: 2}}
	n.Spec.Exposure.Addresses = []api.NetworkLoadBalancerAddressRequest{{Name: "public", Families: []api.IPFamily{"IPv4", "IPv6"}}}
	n.Spec.Listeners = []api.NetworkLoadBalancerListener{{Name: "media", AddressRefs: []string{"public"}}}
	n.Status.Conditions = []metav1.Condition{condition("Ready", 2)}
	n.Status.AddressRequests = []api.NetworkLoadBalancerAddressRequestStatus{{Name: "public", ResolvedLocations: []string{"dfw"}}}
	ep := api.NetworkLoadBalancerEndpointStatus{RequestName: "public", Location: "dfw", CanonicalHostname: "dfw.example.com"}
	ep.Listeners = []api.NetworkLoadBalancerListenerStatus{{Name: "media", Conditions: []metav1.Condition{condition("Programmed", 2), condition("Announced", 2)}}}
	ep.Addresses = []api.NetworkLoadBalancerAddressStatus{
		{Family: "IPv4", Value: "192.0.2.1", Announced: true, DNSPublished: true, Conditions: []metav1.Condition{condition("Announced", 2)}},
		{Family: "IPv6", Value: "2001:db8::1", Announced: true, DNSPublished: true, Conditions: []metav1.Condition{condition("Announced", 2)}},
	}
	n.Status.Endpoints = []api.NetworkLoadBalancerEndpointStatus{ep}
	r := api.NetworkLoadBalancerRoute{ObjectMeta: metav1.ObjectMeta{Generation: 3}}
	r.Spec.ParentRefs = []api.NetworkLoadBalancerRouteParentRef{{Name: "media", Listener: "media"}}
	r.Status.Parents = []api.NetworkLoadBalancerRouteParentStatus{{Name: "media", Listener: "media", Effective: true, Conditions: []metav1.Condition{condition("Accepted", 3), condition("ResolvedRefs", 3), condition("Programmed", 3)}}}
	ready, _ := Ready(n, []api.NetworkLoadBalancerRoute{r}, "media")
	require.True(t, ready)
	n.Status.Endpoints[0].Addresses[0].DNSPublished = false
	ready, _ = Ready(n, []api.NetworkLoadBalancerRoute{r}, "media")
	require.False(t, ready)
	n.Status.Endpoints[0].Addresses[0].DNSPublished = true
	n.Status.Endpoints[0].Listeners[0].Conditions[0].ObservedGeneration = 1
	ready, _ = Ready(n, []api.NetworkLoadBalancerRoute{r}, "media")
	require.False(t, ready)
	n.Status.Endpoints[0].Listeners[0].Conditions[0].ObservedGeneration = 2
	r.Status.Parents[0].Effective = false
	ready, _ = Ready(n, []api.NetworkLoadBalancerRoute{r}, "media")
	require.False(t, ready)
}

func TestHealthyNegativeCondition(t *testing.T) {
	conditions := []metav1.Condition{condition("Ready", 1), {Type: "Degraded", Status: metav1.ConditionFalse, ObservedGeneration: 1}}
	require.Equal(t, "Ready", ConditionText(conditions, 1))
	conditions[1].Status = metav1.ConditionTrue
	conditions[1].Reason = "Partial"
	require.Contains(t, ConditionText(conditions, 1), "Partial")
}

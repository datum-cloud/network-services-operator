// SPDX-License-Identifier: AGPL-3.0-only
package spec

import (
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func readyFixture() (*api.NetworkLoadBalancer, *api.NetworkLoadBalancerRoute) {
	n := &api.NetworkLoadBalancer{ObjectMeta: metav1.ObjectMeta{Name: "media", Namespace: Namespace, UID: "nlb-uid", Generation: 2}}
	n.Spec.Listeners = []api.NetworkLoadBalancerListener{{Name: "media", AddressRefs: []string{"public"}}}
	n.Spec.Exposure.Addresses = []api.NetworkLoadBalancerAddressRequest{{Name: "public", Families: []api.IPFamily{"IPv6"}}}
	n.Status.AddressRequests = []api.NetworkLoadBalancerAddressRequestStatus{{Name: "public", ResolvedLocations: []string{"dfw"}}}
	n.Status.Endpoints = []api.NetworkLoadBalancerEndpointStatus{{RequestName: "public", Location: "dfw", CanonicalHostname: "dfw.example.com", Listeners: []api.NetworkLoadBalancerListenerStatus{{Name: "media", Conditions: []metav1.Condition{condition("Programmed", 2), condition("Announced", 2)}}}, Addresses: []api.NetworkLoadBalancerAddressStatus{{Family: "IPv6", Value: "2001:db8::1", Announced: true, DNSPublished: true, WithdrawalPending: true, Conditions: []metav1.Condition{condition("Announced", 2)}}}}}
	r := &api.NetworkLoadBalancerRoute{ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: Namespace, UID: "route-uid", Generation: 3}}
	r.Spec.ParentRefs = []api.NetworkLoadBalancerRouteParentRef{{Name: n.Name, Listener: "media"}}
	r.Status.Parents = []api.NetworkLoadBalancerRouteParentStatus{{Name: n.Name, Listener: "media", Effective: true, Conditions: []metav1.Condition{condition("Accepted", 3), condition("ResolvedRefs", 3), condition("Programmed", 3)}}}
	return n, r
}

func TestPublicationHoldDoesNotPreventReadiness(t *testing.T) {
	for _, coordinated := range []bool{false, true} {
		n, r := readyFixture()
		n.Status.Endpoints[0].Addresses[0].Coordinated = coordinated
		ready, msg := ReadyForRoute(n, []api.NetworkLoadBalancerRoute{*r}, "media", r)
		require.True(t, ready, msg)
		n.Status.Endpoints[0].Addresses[0].Retiring = true
		ready, _ = ReadyForRoute(n, []api.NetworkLoadBalancerRoute{*r}, "media", r)
		require.False(t, ready)
	}
}

func TestReadinessRequiresWrittenRoute(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*api.NetworkLoadBalancerRoute)
		message string
	}{
		{"superseded", func(r *api.NetworkLoadBalancerRoute) { r.Status.Parents[0].Effective = false }, "not effective"},
		{"replaced", func(r *api.NetworkLoadBalancerRoute) { r.UID = "replacement" }, "changed while waiting"},
		{"edited again", func(r *api.NetworkLoadBalancerRoute) { r.Generation++ }, "changed while waiting"},
		{"old read", func(r *api.NetworkLoadBalancerRoute) { r.Generation-- }, "changed while waiting"},
		{"stale status", func(r *api.NetworkLoadBalancerRoute) { r.Status.Parents[0].Conditions[2].ObservedGeneration-- }, "Waiting for route"},
		{"deleting", func(r *api.NetworkLoadBalancerRoute) { now := metav1.Now(); r.DeletionTimestamp = &now }, "being removed"},
		{"detached", func(r *api.NetworkLoadBalancerRoute) { r.Spec.ParentRefs = nil }, "no longer attached"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, r := readyFixture()
			expected := r.DeepCopy()
			other := r.DeepCopy()
			other.Name = "older-route"
			other.UID = "older-route"
			tc.mutate(r)
			ready, message := ReadyForRoute(n, []api.NetworkLoadBalancerRoute{*other, *r}, "media", expected)
			require.False(t, ready)
			require.Contains(t, message, tc.message)
		})
	}
}

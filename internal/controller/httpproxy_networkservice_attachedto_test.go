// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func memberWithAttachedTo(name string, ref *networkingv1alpha.AttachedToRef) *networkingv1alpha.NetworkInterface {
	iface := &networkingv1alpha.NetworkInterface{}
	iface.Namespace = "proj"
	iface.Name = name
	iface.Spec.AttachedTo = ref
	return iface
}

func TestNetworkServiceEndpoint_SetsTargetRefFromAttachedTo(t *testing.T) {
	member := memberWithAttachedTo("web-0", &networkingv1alpha.AttachedToRef{
		APIGroup: "compute.datumapis.com",
		Kind:     "Instance",
		Name:     "web-0",
	})

	ep := networkServiceEndpoint(member, "10.0.0.5")

	require.NotNil(t, ep.TargetRef)
	assert.Equal(t, "compute.datumapis.com", ep.TargetRef.APIVersion)
	assert.Equal(t, "Instance", ep.TargetRef.Kind)
	assert.Equal(t, "web-0", ep.TargetRef.Name)
	assert.Equal(t, "proj", ep.TargetRef.Namespace)
}

func TestNetworkServiceEndpoint_NoTargetRefWithoutAttachedTo(t *testing.T) {
	ep := networkServiceEndpoint(memberWithAttachedTo("web-0", nil), "10.0.0.5")
	assert.Nil(t, ep.TargetRef)
}

func sliceLabels(t *testing.T, refs []*networkingv1alpha.AttachedToRef) map[string]string {
	t.Helper()

	resolved := &resolvedNetworkService{addressType: discoveryv1.AddressTypeIPv4}
	for i, ref := range refs {
		member := memberWithAttachedTo("m", ref)
		resolved.endpoints = append(resolved.endpoints, networkServiceEndpoint(member, "10.0.0."+string(rune('1'+i))))
	}

	slices := networkServiceEndpointSlices("proj", "svc-0-0", "http", "svc", resolved)
	require.Len(t, slices, 1)
	return slices[0].Labels
}

func TestNetworkServiceEndpointSlices_StampsWhenMembersAgree(t *testing.T) {
	ref := &networkingv1alpha.AttachedToRef{APIGroup: "compute.datumapis.com", Kind: "Instance", Name: "web-0"}
	labels := sliceLabels(t, []*networkingv1alpha.AttachedToRef{ref, ref})

	assert.Equal(t, "compute.datumapis.com", labels[AttachedToGroupLabel])
	assert.Equal(t, "Instance", labels[AttachedToKindLabel])
	assert.Equal(t, "web-0", labels[AttachedToNameLabel])
}

func TestNetworkServiceEndpointSlices_OmitsWhenMembersDisagree(t *testing.T) {
	labels := sliceLabels(t, []*networkingv1alpha.AttachedToRef{
		{APIGroup: "compute.datumapis.com", Kind: "Instance", Name: "web-0"},
		{APIGroup: "compute.datumapis.com", Kind: "Instance", Name: "web-1"},
	})

	assert.NotContains(t, labels, AttachedToGroupLabel)
	assert.NotContains(t, labels, AttachedToKindLabel)
	assert.NotContains(t, labels, AttachedToNameLabel)
}

func TestNetworkServiceEndpointSlices_OmitsWhenMemberLacksAttachedTo(t *testing.T) {
	ref := &networkingv1alpha.AttachedToRef{APIGroup: "compute.datumapis.com", Kind: "Instance", Name: "web-0"}
	labels := sliceLabels(t, []*networkingv1alpha.AttachedToRef{ref, nil})

	assert.NotContains(t, labels, AttachedToGroupLabel)
}

func TestNetworkServiceEndpointSlices_OmitsWhenNoMembers(t *testing.T) {
	labels := sliceLabels(t, nil)
	assert.NotContains(t, labels, AttachedToGroupLabel)
}

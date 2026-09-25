// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// declareEgress puts an internet egress declaration on the scenario's network,
// which is where a consumer writes it.
func (s *presenceScenario) declareEgress(internet *networkingv1alpha.NetworkInternetEgress) {
	s.t.Helper()
	s.network.Spec.Egress = &networkingv1alpha.NetworkEgress{Internet: internet}
	require.NoError(s.t, s.hub.Update(s.ctx, s.network))
}

func (s *presenceScenario) projectedEgress() *networkingv1alpha.NetworkContextInternetEgress {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, ok, "the presence controller created no network context")
	if networkContext.Spec.Egress == nil {
		return nil
	}
	return networkContext.Spec.Egress.Internet
}

func (s *presenceScenario) egressCondition() *metav1.Condition {
	s.t.Helper()
	networkContext, ok := s.networkContext()
	require.True(s.t, ok)
	return apimeta.FindStatusCondition(networkContext.Status.Conditions,
		networkingv1alpha.NetworkContextInternetEgressReady)
}

func TestNetworkPresenceProjectsDeclaredEgressIntent(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
	})
	s.createBinding("consumer-a")
	s.reconcile()

	egress := s.projectedEgress()
	require.NotNil(t, egress, "a location was given no egress instruction to act on")
	require.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, egress.Mode)
	require.Equal(t, []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, egress.Reach)

	require.Nil(t, s.egressCondition(),
		"the result belongs to the location that realizes it, not to the controller that instructs it")
}

// Disabled is projected, not omitted. A location that reads no instruction
// cannot tell a network that reaches nothing from one written before the field
// was carried, and the two mean opposite things.
func TestNetworkPresenceProjectsDisabledEgressExplicitly(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressDisabled,
	})
	s.createBinding("consumer-a")
	s.reconcile()

	egress := s.projectedEgress()
	require.NotNil(t, egress, "a network that reaches nothing still instructs the location it reaches nothing")
	require.Equal(t, networkingv1alpha.NetworkInternetEgressDisabled, egress.Mode)
	require.Empty(t, egress.Reach)
	require.Nil(t, s.egressCondition())
}

func TestNetworkPresenceProjectsNoEgressForANetworkDeclaringNone(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.createBinding("consumer-a")
	s.reconcile()

	require.Nil(t, s.projectedEgress())
	require.Nil(t, s.egressCondition())
}

// A declaration a consumer changes reaches the location on the next pass, and
// one they withdraw is carried as Disabled rather than dropped.
func TestNetworkPresenceFollowsAChangedEgressDeclaration(t *testing.T) {
	s := newPresenceScenario(t, presenceOptions{})
	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode:  networkingv1alpha.NetworkInternetEgressEnabled,
		Reach: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
	})
	s.createBinding("consumer-a")
	s.reconcile()
	require.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, s.projectedEgress().Mode)

	s.declareEgress(&networkingv1alpha.NetworkInternetEgress{
		Mode: networkingv1alpha.NetworkInternetEgressDisabled,
	})
	s.reconcile()

	egress := s.projectedEgress()
	require.NotNil(t, egress)
	require.Equal(t, networkingv1alpha.NetworkInternetEgressDisabled, egress.Mode)
	require.Empty(t, egress.Reach)
}

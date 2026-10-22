// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestNetworkWithoutAModeIsStampedWithThePlatformDefault(t *testing.T) {
	s := newNetworkScenario(t, nil)
	s.reconciler.DefaultInternetEgressMode = networkingv1alpha.NetworkInternetEgressEnabled
	s.createNetwork(networkingv1alpha.IPv6Protocol)

	s.reconcile()

	network := s.get()
	require.NotNil(t, network.Spec.Egress)
	require.NotNil(t, network.Spec.Egress.Internet)
	require.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, network.Spec.Egress.Internet.Mode)
}

func TestNetworkStampNeverOverwritesAMode(t *testing.T) {
	s := newNetworkScenario(t, nil)
	s.reconciler.DefaultInternetEgressMode = networkingv1alpha.NetworkInternetEgressEnabled
	network := s.createNetwork(networkingv1alpha.IPv6Protocol)
	network.Spec.Egress = &networkingv1alpha.NetworkEgress{
		Internet: &networkingv1alpha.NetworkInternetEgress{Mode: networkingv1alpha.NetworkInternetEgressDisabled},
	}
	require.NoError(t, s.client.Update(s.ctx, network))

	s.reconcile()
	s.reconcile()

	require.Equal(t, networkingv1alpha.NetworkInternetEgressDisabled, s.get().Spec.Egress.Internet.Mode)
}

func TestNetworkStampIsSafeToRepeat(t *testing.T) {
	s := newNetworkScenario(t, nil)
	s.reconciler.DefaultInternetEgressMode = networkingv1alpha.NetworkInternetEgressEnabled
	s.createNetwork(networkingv1alpha.IPv6Protocol)

	s.reconcile()
	stamped := s.get()

	s.reconciler.DefaultInternetEgressMode = networkingv1alpha.NetworkInternetEgressDisabled
	s.reconcile()

	after := s.get()
	require.Equal(t, networkingv1alpha.NetworkInternetEgressEnabled, after.Spec.Egress.Internet.Mode,
		"changing the platform default must not move a network already stamped")
	require.Equal(t, stamped.Generation, after.Generation)
}

func TestNetworkWithoutAPlatformDefaultIsNotStamped(t *testing.T) {
	s := newNetworkScenario(t, nil)
	s.createNetwork(networkingv1alpha.IPv6Protocol)

	s.reconcile()

	require.Nil(t, s.get().Spec.Egress)
}

func TestEffectiveInternetEgressMode(t *testing.T) {
	enabled := networkingv1alpha.NetworkInternetEgressEnabled
	disabled := networkingv1alpha.NetworkInternetEgressDisabled
	withMode := func(mode networkingv1alpha.NetworkInternetEgressMode) *networkingv1alpha.Network {
		network := &networkingv1alpha.Network{}
		network.Spec.Egress = &networkingv1alpha.NetworkEgress{
			Internet: &networkingv1alpha.NetworkInternetEgress{Mode: mode},
		}
		return network
	}

	tests := []struct {
		name     string
		network  *networkingv1alpha.Network
		platform networkingv1alpha.NetworkInternetEgressMode
		want     networkingv1alpha.NetworkInternetEgressMode
	}{
		{name: "the network's mode wins", network: withMode(disabled), platform: enabled, want: disabled},
		{name: "an unstamped network gets the platform default", network: &networkingv1alpha.Network{}, platform: disabled, want: disabled},
		{name: "an empty mode gets the platform default", network: withMode(""), platform: disabled, want: disabled},
		{name: "no default never means disabled", network: &networkingv1alpha.Network{}, want: enabled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, projectInternetEgress(test.network, test.platform).Internet.Mode)
		})
	}
}

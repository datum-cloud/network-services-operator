// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPublicUDPStatusSurvivesMembershipReconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	s := &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "udp", Namespace: "default"}}
	s.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{"app": "edge"}
	s.Status.PublicAddresses = []string{"192.0.2.44"}
	meta.SetStatusCondition(&s.Status.Conditions, metav1.Condition{Type: "PublicReady", Status: metav1.ConditionTrue, Reason: "Programmed"})
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(s).WithObjects(s).Build()
	require.NoError(t, (&NetworkServiceReconciler{}).reconcileService(context.Background(), cl, client.ObjectKeyFromObject(s)))
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(s), s))
	require.Equal(t, []string{"192.0.2.44"}, s.Status.PublicAddresses)
	require.True(t, meta.IsStatusConditionTrue(s.Status.Conditions, "PublicReady"))
}

func TestHTTPProxyRejectsUDPBackend(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, api.AddToScheme(scheme))
	s := &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "udp", Namespace: "default"}}
	s.Spec.Ports = []api.NetworkServicePort{{Name: "quic", Port: 443, Protocol: api.NetworkServiceProtocolUDP}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(s).Build()
	_, err := resolveNetworkServiceBackend(context.Background(), cl, s.Namespace, &api.NetworkServiceBackendRef{Name: s.Name, Port: "quic"})
	require.IsType(t, &errNetworkServiceBackendNotFound{}, err)
}

func TestPublicUDPAdmission(t *testing.T) {
	scenario := newNetworkServiceScenario(t)
	for _, test := range []struct {
		name                   string
		protocol               api.NetworkServiceProtocol
		extraPort, appProtocol bool
	}{
		{name: "tcp", protocol: api.NetworkServiceProtocolTCP},
		{name: "multiple", protocol: api.NetworkServiceProtocolUDP, extraPort: true},
		{name: "app-protocol", protocol: api.NetworkServiceProtocolUDP, appProtocol: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: test.name, Namespace: scenario.namespace}}
			s.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{"app": "edge"}
			s.Spec.PublicUDP = &api.NetworkServicePublicUDP{Location: "iad"}
			s.Spec.Ports = []api.NetworkServicePort{{Name: "quic", Port: 443, Protocol: test.protocol}}
			if test.extraPort {
				s.Spec.Ports = append(s.Spec.Ports, api.NetworkServicePort{Name: "second", Port: 444, Protocol: api.NetworkServiceProtocolUDP})
			}
			if test.appProtocol {
				app := api.NetworkServiceAppProtocolH2C
				s.Spec.Ports[0].AppProtocol = &app
			}
			require.Error(t, scenario.client.Create(scenario.ctx, s))
		})
	}
	s := &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "valid", Namespace: scenario.namespace}}
	s.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{"app": "edge"}
	s.Spec.PublicUDP = &api.NetworkServicePublicUDP{Location: "iad"}
	s.Spec.Ports = []api.NetworkServicePort{{Name: "quic", Port: 443, Protocol: api.NetworkServiceProtocolUDP}}
	require.NoError(t, scenario.client.Create(scenario.ctx, s))
	require.Equal(t, "public-ipv4", s.Spec.PublicUDP.AddressClass)
	original := s.DeepCopy()
	s.Spec.PublicUDP.Location = "other"
	require.Error(t, scenario.client.Update(scenario.ctx, s))
	s = original.DeepCopy()
	s.Spec.PublicUDP = nil
	require.Error(t, scenario.client.Update(scenario.ctx, s))
	s = original.DeepCopy()
	s.Spec.Ports[0].Port = 444
	require.NoError(t, scenario.client.Update(scenario.ctx, s))
}

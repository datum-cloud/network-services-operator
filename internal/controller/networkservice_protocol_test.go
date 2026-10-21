// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHTTPProxyNetworkServiceTransport(t *testing.T) {
	for _, protocol := range []api.NetworkServiceProtocol{"", api.NetworkServiceProtocolTCP, api.NetworkServiceProtocolUDP} {
		t.Run(string(protocol), func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, api.AddToScheme(scheme))
			s := &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "default"}}
			s.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{"app": "backend"}
			s.Spec.Ports = []api.NetworkServicePort{{Name: "service", Port: 443, Protocol: protocol}}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(s).Build()
			resolved, err := resolveNetworkServiceBackend(context.Background(), cl, s.Namespace, &api.NetworkServiceBackendRef{Name: s.Name, Port: "service"})
			if protocol == api.NetworkServiceProtocolUDP {
				require.IsType(t, &errNetworkServiceBackendNotFound{}, err)
				require.Nil(t, resolved)
			} else {
				require.NoError(t, err)
				require.Equal(t, int32(443), resolved.port)
			}
		})
	}
}

func TestNetworkServiceProtocolAdmission(t *testing.T) {
	s := newNetworkServiceScenario(t)
	service := s.createService("protocols", map[string]string{"app": "backend"})
	require.Equal(t, api.NetworkServiceProtocolTCP, service.Spec.Ports[0].Protocol)
	service.Spec.Ports = append(service.Spec.Ports,
		api.NetworkServicePort{Name: "quic", Port: 443, Protocol: api.NetworkServiceProtocolUDP},
		api.NetworkServicePort{Name: "dns", Port: 53, Protocol: api.NetworkServiceProtocolUDP})
	require.NoError(t, s.client.Update(s.ctx, service))
	require.NoError(t, s.client.Get(s.ctx, client.ObjectKeyFromObject(service), service))
	require.Equal(t, api.NetworkServiceProtocolTCP, service.Spec.Ports[0].Protocol)
	require.Equal(t, api.NetworkServiceProtocolUDP, service.Spec.Ports[1].Protocol)
	require.Equal(t, api.NetworkServiceProtocolUDP, service.Spec.Ports[2].Protocol)
	service.Spec.Ports[1].Protocol = "SCTP"
	require.Error(t, s.client.Update(s.ctx, service))
}

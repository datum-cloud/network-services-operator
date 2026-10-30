// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
				require.IsType(t, &errNetworkServiceBackendUnsupportedProtocol{}, err)
				require.EqualError(t, err, `NetworkService "backend" port "service" uses UDP. HTTPProxy backends require TCP. Update the HTTPProxy backend to reference a NetworkService port that serves HTTP over TCP.`)
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

func TestHTTPProxyUnsupportedBackendProtocolCondition(t *testing.T) {
	condition := &metav1.Condition{Type: api.HTTPProxyConditionProgrammed, Status: metav1.ConditionTrue, Reason: api.HTTPProxyReasonProgrammed}
	cause := &errNetworkServiceBackendUnsupportedProtocol{service: "storefront", port: "http", protocol: api.NetworkServiceProtocolUDP}
	result, err, done := collectDesiredResourcesErrorResult(fmt.Errorf("resolve backend: %w", cause), condition)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, retryAfterInvalid, result.RequeueAfter)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, api.HTTPProxyReasonUnsupportedBackendProtocol, condition.Reason)
	require.Equal(t, `NetworkService "storefront" port "http" uses UDP. HTTPProxy backends require TCP. Update the HTTPProxy backend to reference a NetworkService port that serves HTTP over TCP.`, condition.Message)
}

func TestHTTPProxyReportsUDPBackendAndRecoversWithTCP(t *testing.T) {
	ctx := context.Background()
	proxy := newNetworkServiceProxy()
	controllerutil.AddFinalizer(proxy, httpProxyFinalizer)
	service := newNetworkService()
	service.Spec.Ports[0].Protocol = api.NetworkServiceProtocolUDP
	cl := fake.NewClientBuilder().WithScheme(fullHTTPProxyTestScheme(t)).WithObjects(proxy, service).WithStatusSubresource(proxy).Build()
	updated, err := reconcileHTTPProxyOnce(t, cl, proxy)
	require.NoError(t, err)
	condition := meta.FindStatusCondition(updated.Status.Conditions, api.HTTPProxyConditionProgrammed)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, api.HTTPProxyReasonUnsupportedBackendProtocol, condition.Reason)
	require.Equal(t, `NetworkService "storefront" port "http" uses UDP. HTTPProxy backends require TCP. Update the HTTPProxy backend to reference a NetworkService port that serves HTTP over TCP.`, condition.Message)
	var slices discoveryv1.EndpointSliceList
	require.NoError(t, cl.List(ctx, &slices))
	require.Empty(t, slices.Items)
	service.Spec.Ports[0].Protocol = api.NetworkServiceProtocolTCP
	require.NoError(t, cl.Update(ctx, service))
	updated, err = reconcileHTTPProxyOnce(t, cl, proxy)
	require.NoError(t, err)
	condition = meta.FindStatusCondition(updated.Status.Conditions, api.HTTPProxyConditionProgrammed)
	require.NotNil(t, condition)
	require.NotEqual(t, api.HTTPProxyReasonUnsupportedBackendProtocol, condition.Reason)
	require.NoError(t, cl.List(ctx, &slices))
	require.Len(t, slices.Items, 1)
}

func TestHTTPProxyRejectsRangeAndNormalizesEqualEndpoints(t *testing.T) {
	for _, end := range []int32{443, 444} {
		s := &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "default"}, Spec: api.NetworkServiceSpec{NetworkInterfaces: api.NetworkServiceInterfaceSelector{Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "backend"}}}, Ports: []api.NetworkServicePort{{Name: "service", PortRange: &api.PortRange{Start: 443, End: end}}}}}
		scheme := runtime.NewScheme()
		require.NoError(t, api.AddToScheme(scheme))
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(s).Build()
		resolved, err := resolveNetworkServiceBackend(context.Background(), cl, s.Namespace, &api.NetworkServiceBackendRef{Name: s.Name, Port: "service"})
		if end == 443 {
			require.NoError(t, err)
			require.EqualValues(t, 443, resolved.port)
			continue
		}
		require.IsType(t, &errNetworkServiceBackendUnsupportedRange{}, err)
		condition := &metav1.Condition{}
		_, reported, done := collectDesiredResourcesErrorResult(err, condition)
		require.NoError(t, reported)
		require.True(t, done)
		require.Equal(t, "UnsupportedBackendPortRange", condition.Reason)
		require.Equal(t, metav1.ConditionFalse, condition.Status)
	}
}

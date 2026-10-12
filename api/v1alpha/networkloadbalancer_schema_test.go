// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

func TestNetworkLoadBalancerAdmission(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("envtest assets are required")
	}

	crds := make([]*apiextensionsv1.CustomResourceDefinition, 0, 2)
	for _, name := range []string{"networkloadbalancers", "networkloadbalancerroutes"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "networking.datumapis.com_"+name+".yaml"))
		require.NoError(t, err)
		var crd apiextensionsv1.CustomResourceDefinition
		require.NoError(t, yaml.Unmarshal(data, &crd))
		crds = append(crds, &crd)
	}

	env := &envtest.Environment{CRDs: crds}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })

	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)

	base := &networkingv1alpha.NetworkLoadBalancer{
		ObjectMeta: metav1.ObjectMeta{Name: "valid", Namespace: "default"},
		Spec: networkingv1alpha.NetworkLoadBalancerSpec{
			NetworkRef: networkingv1alpha.LocalNetworkRef{Name: "default"},
			Exposure: networkingv1alpha.NetworkLoadBalancerExposure{Addresses: []networkingv1alpha.NetworkLoadBalancerAddressRequest{{
				Name: "regional", Mode: networkingv1alpha.NetworkLoadBalancerAddressModeUnicast,
				Family:    networkingv1alpha.IPFamily("IPv6"),
				Locations: networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll},
			}, {
				Name: "global", Mode: networkingv1alpha.NetworkLoadBalancerAddressModeAnycast,
				Family:    networkingv1alpha.IPFamily("IPv6"),
				Locations: networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll},
			}}},
			Listeners: []networkingv1alpha.NetworkLoadBalancerListener{
				{Name: "tcp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"regional"}},
				{Name: "udp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, Port: 443, AddressRefs: []string{"regional"}},
			},
		},
	}

	tests := []struct {
		name   string
		mutate func(*networkingv1alpha.NetworkLoadBalancer)
		valid  bool
	}{
		{name: "TCP and UDP share a port", valid: true},
		{name: "IPv4 denied", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Exposure.Addresses[0].Family = networkingv1alpha.IPFamily("IPv4")
		}},
		{name: "missing address reference", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners[0].AddressRefs = []string{"missing"}
		}},
		{name: "same transport and port conflict", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners = append(lb.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{Name: "second-tcp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"regional"}})
		}},
		{name: "Names requires location names", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Exposure.Addresses[0].Locations.From = networkingv1alpha.NetworkLoadBalancerLocationSourceNames
		}},
		{name: "All excludes location names", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Exposure.Addresses[0].Locations.Names = []string{"us-east-1"}
		}},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := base.DeepCopy()
			lb.Name = fmt.Sprintf("case-%d", i)
			if tt.mutate != nil {
				tt.mutate(lb)
			}
			err := k8sClient.Create(context.Background(), lb)
			if tt.valid {
				require.NoError(t, err)
				lb.Status.Addresses = []networkingv1alpha.NetworkLoadBalancerAddressStatus{
					{RequestName: "regional", Location: "us-east-1", Family: networkingv1alpha.IPFamily("IPv6"), Value: "2001:db8::10"},
					{RequestName: "global", Location: "", Family: networkingv1alpha.IPFamily("IPv6"), Value: ""},
				}
				require.NoError(t, k8sClient.Status().Update(context.Background(), lb))
			} else {
				require.Error(t, err)
			}
		})
	}

	route := &networkingv1alpha.NetworkLoadBalancerRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
		Spec: networkingv1alpha.NetworkLoadBalancerRouteSpec{
			ParentRefs: []networkingv1alpha.NetworkLoadBalancerRouteParentRef{{Name: "valid", Listener: "tcp"}},
			Backends: []networkingv1alpha.NetworkLoadBalancerRouteBackend{{
				NetworkService: networkingv1alpha.NetworkLoadBalancerNetworkServiceRef{Name: "app", Port: "main"},
			}},
		},
	}

	routeTests := []struct {
		name   string
		mutate func(*networkingv1alpha.NetworkLoadBalancerRoute)
		valid  bool
	}{
		{name: "valid route", valid: true},
		{name: "negative weight", mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute) {
			weight := int32(-1)
			r.Spec.Backends[0].Weight = &weight
		}},
		{name: "TCP path rejected", mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute) {
			r.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "TCP", Path: "/health"}
		}},
		{name: "HTTP probe requires port", mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute) {
			r.Spec.Backends[0].HealthCheck = &networkingv1alpha.NetworkLoadBalancerHealthCheck{Protocol: "HTTP", Path: "/health"}
		}},
	}
	for i, tt := range routeTests {
		t.Run(tt.name, func(t *testing.T) {
			r := route.DeepCopy()
			r.Name = fmt.Sprintf("route-%d", i)
			if tt.mutate != nil {
				tt.mutate(r)
			}
			err := k8sClient.Create(context.Background(), r)
			if tt.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

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
	for _, name := range []string{"networkloadbalancers", "networkloadbalancerroutes", "networkservices", "networkloadbalancercellintents"} {
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
				Name:     "regional",
				Families: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
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
		{name: "UDP range", valid: true, mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners[1].Port = 0
			lb.Spec.Listeners[1].PortRange = &networkingv1alpha.PortRange{Start: 10000, End: 10127}
		}},
		{name: "equal TCP range", valid: true, mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners[0].Port = 0
			lb.Spec.Listeners[0].PortRange = &networkingv1alpha.PortRange{Start: 443, End: 443}
		}},
		{name: "TCP true range rejected", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners[0].Port = 0
			lb.Spec.Listeners[0].PortRange = &networkingv1alpha.PortRange{Start: 443, End: 444}
		}},
		{name: "both port forms rejected", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners[1].PortRange = &networkingv1alpha.PortRange{Start: 443, End: 444}
		}},
		{name: "neither port form rejected", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) { lb.Spec.Listeners[1].Port = 0 }},
		{name: "reversed range rejected", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners[1].Port = 0
			lb.Spec.Listeners[1].PortRange = &networkingv1alpha.PortRange{Start: 444, End: 443}
		}},
		{name: "out of bounds range rejected", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners[1].Port = 0
			lb.Spec.Listeners[1].PortRange = &networkingv1alpha.PortRange{Start: 1, End: 65536}
		}},
		{name: "range singleton overlap rejected", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners = append(lb.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{Name: "other", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, PortRange: &networkingv1alpha.PortRange{Start: 400, End: 443}, AddressRefs: []string{"regional"}})
		}},
		{name: "adjacent ranges", valid: true, mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners = append(lb.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{Name: "other", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, PortRange: &networkingv1alpha.PortRange{Start: 444, End: 500}, AddressRefs: []string{"regional"}})
		}},

		{name: "IPv4 retained in API for capability gating", valid: true, mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Exposure.Addresses[0].Families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol, networkingv1alpha.IPv6Protocol}
		}},
		{name: "duplicate family rejected", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Exposure.Addresses[0].Families = []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol, networkingv1alpha.IPv6Protocol}
		}},
		{name: "missing address reference", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners[0].AddressRefs = []string{"missing"}
		}},
		{name: "same transport and port conflict", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Listeners = append(lb.Spec.Listeners, networkingv1alpha.NetworkLoadBalancerListener{Name: "second-tcp", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, Port: 443, AddressRefs: []string{"regional"}})
		}},
		{name: "Names requires location names", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Exposure.Addresses[0].Locations = &networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceNames}
		}},
		{name: "All excludes location names", mutate: func(lb *networkingv1alpha.NetworkLoadBalancer) {
			lb.Spec.Exposure.Addresses[0].Locations = &networkingv1alpha.NetworkLoadBalancerLocationSelection{From: networkingv1alpha.NetworkLoadBalancerLocationSourceAll, Names: []string{"us-east-1"}}
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
				lb.Status.Endpoints = []networkingv1alpha.NetworkLoadBalancerEndpointStatus{
					{RequestName: "regional", Location: "us-east-1", Addresses: []networkingv1alpha.NetworkLoadBalancerAddressStatus{{Family: networkingv1alpha.IPv6Protocol, Value: "2001:db8::10"}}},
					{RequestName: "regional", Location: "us-west-1", Addresses: []networkingv1alpha.NetworkLoadBalancerAddressStatus{{Family: networkingv1alpha.IPv6Protocol, Value: "2001:db8::20"}}},
				}
				require.NoError(t, k8sClient.Status().Update(context.Background(), lb))
			} else {
				require.Error(t, err)
			}
		})
	}

	for i, tc := range []struct {
		name  string
		ports []networkingv1alpha.NetworkServicePort
		valid bool
	}{
		{"same numeric port distinct protocol", []networkingv1alpha.NetworkServicePort{{Name: "tcp", Port: 443}, {Name: "udp", Port: 443, Protocol: networkingv1alpha.NetworkServiceProtocolUDP}}, true},
		{"overlapping UDP intervals", []networkingv1alpha.NetworkServicePort{{Name: "first", Protocol: networkingv1alpha.NetworkServiceProtocolUDP, PortRange: &networkingv1alpha.PortRange{Start: 10000, End: 10127}}, {Name: "second", Protocol: networkingv1alpha.NetworkServiceProtocolUDP, Port: 10127}}, false},
		{"adjacent UDP intervals", []networkingv1alpha.NetworkServicePort{{Name: "first", Protocol: networkingv1alpha.NetworkServiceProtocolUDP, PortRange: &networkingv1alpha.PortRange{Start: 10000, End: 10127}}, {Name: "second", Protocol: networkingv1alpha.NetworkServiceProtocolUDP, Port: 10128}}, true},
		{"TCP true range", []networkingv1alpha.NetworkServicePort{{Name: "tcp", PortRange: &networkingv1alpha.PortRange{Start: 10000, End: 10127}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &networkingv1alpha.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("service-%d", i), Namespace: "default"}, Spec: networkingv1alpha.NetworkServiceSpec{NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "media"}}}, Ports: tc.ports}}
			err := k8sClient.Create(context.Background(), service)
			if tc.valid {
				require.NoError(t, err)
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
		{name: "multiple backends retained in API", valid: true, mutate: func(r *networkingv1alpha.NetworkLoadBalancerRoute) {
			r.Spec.Backends = append(r.Spec.Backends, networkingv1alpha.NetworkLoadBalancerRouteBackend{NetworkService: networkingv1alpha.NetworkLoadBalancerNetworkServiceRef{Name: "other", Port: "main"}})
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
	t.Run("coordinated serving requires the registration-aware wire state", func(t *testing.T) {
		ctx := context.Background()
		intent := &networkingv1alpha.NetworkLoadBalancerCellIntent{ObjectMeta: metav1.ObjectMeta{Name: "wire-state", Namespace: "default"}, Spec: networkingv1alpha.NetworkLoadBalancerCellIntentSpec{
			Source:   networkingv1alpha.NetworkLoadBalancerCellIntentSource{ProjectCluster: "project", Namespace: "project", ParentName: "nlb", ParentUID: "parent", RouteName: "route", RouteUID: "route", ListenerName: "web", AddressRequest: "public"},
			Location: "iad", NetworkName: "net", VIPFamily: networkingv1alpha.IPv6Protocol, VIP: "2001:db8::1", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, ListenerPort: 80,
			State:        networkingv1alpha.NetworkLoadBalancerCellIntentValidateAllocation,
			AddressGroup: &networkingv1alpha.NetworkLoadBalancerCellIntentAddressGroup{UID: "allocation", Generation: 1, Revision: "one", ListenerName: "web", Listeners: []networkingv1alpha.NetworkLoadBalancerCellIntentAddressListener{{Name: "web", Protocol: networkingv1alpha.NetworkLoadBalancerProtocolUDP, Port: 80}}},
			Backends:     []networkingv1alpha.NetworkLoadBalancerCellIntentBackend{{SourceNamespace: "project", SourceName: "interface", SourceUID: "interface", Family: networkingv1alpha.IPv6Protocol, Address: "fd00::1", Port: 80}},
		}}
		require.NoError(t, k8sClient.Create(ctx, intent))
		legacy := intent.DeepCopy()
		legacy.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentValidate
		require.Error(t, k8sClient.Update(ctx, legacy))
		missingInventory := intent.DeepCopy()
		missingInventory.Spec.AddressGroup = nil
		require.Error(t, k8sClient.Update(ctx, missingInventory))
		missingBackends := intent.DeepCopy()
		missingBackends.Spec.Backends = nil
		require.Error(t, k8sClient.Update(ctx, missingBackends))
		intent.Spec.State = networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn
		intent.Spec.Backends = nil
		require.NoError(t, k8sClient.Update(ctx, intent))
	})
	t.Run("allocation retirement is a terminal non-serving union", func(t *testing.T) {
		ctx := context.Background()
		retirement := &networkingv1alpha.NetworkLoadBalancerCellIntent{ObjectMeta: metav1.ObjectMeta{Name: "retired-allocation", Namespace: "default"}, Spec: networkingv1alpha.NetworkLoadBalancerCellIntentSpec{
			Allocation: &networkingv1alpha.NetworkLoadBalancerAllocationAuthority{UID: "allocation-uid", ClaimUID: "claim-uid"},
			Source:     networkingv1alpha.NetworkLoadBalancerCellIntentSource{ProjectCluster: "project", Namespace: "project", ParentName: "nlb", ParentUID: "parent-uid", ParentGeneration: 1, AddressRequest: "public"},
			Location:   "iad", NetworkName: "default", VIPFamily: networkingv1alpha.IPv6Protocol, VIP: "2001:db8::1", State: networkingv1alpha.NetworkLoadBalancerCellIntentWithdrawn,
		}}
		require.NoError(t, k8sClient.Create(ctx, retirement))
		retirement.Labels = map[string]string{"retained": "true"}
		require.NoError(t, k8sClient.Update(ctx, retirement))
		retirement.Spec.Allocation.Participants = []string{"cell-a"}
		require.NoError(t, k8sClient.Update(ctx, retirement))
		dropped := retirement.DeepCopy()
		dropped.Spec.Allocation.Participants = nil
		require.Error(t, k8sClient.Update(ctx, dropped))
		retirement.Spec.Allocation.Participants = append(retirement.Spec.Allocation.Participants, "cell-b")
		require.NoError(t, k8sClient.Update(ctx, retirement))
		retirement.Spec.Allocation.Retired = true
		require.NoError(t, k8sClient.Update(ctx, retirement))
		reopened := retirement.DeepCopy()
		reopened.Spec.Allocation.Retired = false
		require.Error(t, k8sClient.Update(ctx, reopened))
		late := retirement.DeepCopy()
		late.Spec.Allocation.Participants = append(late.Spec.Allocation.Participants, "cell-c")
		require.Error(t, k8sClient.Update(ctx, late))
		retirement.Status.AllocationRetirements = []networkingv1alpha.NetworkLoadBalancerAllocationRetirementReceipt{{CellUID: "cell-a", ObservedGeneration: retirement.Generation}}
		require.NoError(t, k8sClient.Status().Update(ctx, retirement))
		for _, change := range []func(*networkingv1alpha.NetworkLoadBalancerCellIntent){
			func(i *networkingv1alpha.NetworkLoadBalancerCellIntent) { i.Spec.Allocation.UID = "other" },
			func(i *networkingv1alpha.NetworkLoadBalancerCellIntent) { i.Spec.Source.ParentGeneration++ },
			func(i *networkingv1alpha.NetworkLoadBalancerCellIntent) {
				i.Spec.Allocation = nil
				i.Spec.Protocol = networkingv1alpha.NetworkLoadBalancerProtocolTCP
				i.Spec.ListenerPort = 80
				i.Spec.Source.RouteName = "route"
				i.Spec.Source.RouteUID = "route-uid"
				i.Spec.Source.ListenerName = "web"
			},
		} {
			next := retirement.DeepCopy()
			change(next)
			require.Error(t, k8sClient.Update(ctx, next))
		}
		invalid := retirement.DeepCopy()
		invalid.ResourceVersion = ""
		invalid.Name = "retirement-with-listener"
		invalid.Spec.ListenerPort = 80
		require.Error(t, k8sClient.Create(ctx, invalid))
		normal := retirement.DeepCopy()
		normal.ResourceVersion = ""
		normal.Name = "ordinary-intent"
		normal.Spec.Allocation = nil
		normal.Spec.Protocol = networkingv1alpha.NetworkLoadBalancerProtocolTCP
		normal.Spec.ListenerPort = 80
		normal.Spec.Source.RouteName = "route"
		normal.Spec.Source.RouteUID = "route-uid"
		normal.Spec.Source.ListenerName = "web"
		require.NoError(t, k8sClient.Create(ctx, normal))
		normal.Spec.ListenerPort = 81
		require.NoError(t, k8sClient.Update(ctx, normal))
		normal.Spec = retirement.Spec
		require.Error(t, k8sClient.Update(ctx, normal))
	})

}

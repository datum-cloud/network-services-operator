// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

type fakeReader struct {
	networks       []networkingv1alpha.Network
	contexts       []networkingv1alpha.NetworkContext
	bindings       []networkingv1alpha.NetworkBinding
	subnets        []networkingv1alpha.Subnet
	interfaces     []networkingv1alpha.NetworkInterface
	claims         []networkingv1alpha.NetworkInterfaceClaim
	services       []networkingv1alpha.NetworkService
	claimsReadable error
}

func notFound(kind, name string) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "networking.datumapis.com", Resource: kind}, name)
}

func (f *fakeReader) ListNetworks(context.Context, string) ([]networkingv1alpha.Network, error) {
	return f.networks, nil
}

func (f *fakeReader) GetNetwork(_ context.Context, _, name string) (*networkingv1alpha.Network, error) {
	for i := range f.networks {
		if f.networks[i].Name == name {
			return &f.networks[i], nil
		}
	}
	return nil, fmt.Errorf("getting network %s: %w", name, notFound("networks", name))
}

func (f *fakeReader) ListNetworkContexts(context.Context, string) ([]networkingv1alpha.NetworkContext, error) {
	return f.contexts, nil
}

func (f *fakeReader) ListNetworkBindings(context.Context, string) ([]networkingv1alpha.NetworkBinding, error) {
	return f.bindings, nil
}

func (f *fakeReader) ListSubnets(context.Context, string) ([]networkingv1alpha.Subnet, error) {
	return f.subnets, nil
}

func (f *fakeReader) ListNetworkInterfaces(context.Context, string) ([]networkingv1alpha.NetworkInterface, error) {
	return f.interfaces, nil
}

func (f *fakeReader) ListNetworkInterfaceClaims(context.Context, string) ([]networkingv1alpha.NetworkInterfaceClaim, error) {
	return f.claims, f.claimsReadable
}

func (f *fakeReader) ListNetworkServices(context.Context, string) ([]networkingv1alpha.NetworkService, error) {
	return f.services, nil
}

func (f *fakeReader) GetNetworkService(_ context.Context, _, name string) (*networkingv1alpha.NetworkService, error) {
	for i := range f.services {
		if f.services[i].Name == name {
			return &f.services[i], nil
		}
	}
	return nil, fmt.Errorf("getting network service %s: %w", name, notFound("networkservices", name))
}

func ago(d time.Duration) metav1.Time { return metav1.NewTime(testNow.Add(-d)) }

func cond(t, reason string, status metav1.ConditionStatus, since time.Duration) metav1.Condition {
	return metav1.Condition{Type: t, Reason: reason, Status: status, LastTransitionTime: ago(since), Message: reason + " message"}
}

func meta(name string, labels map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: "default", Labels: labels, CreationTimestamp: ago(24 * time.Hour)}
}

func readyNetwork(name string) networkingv1alpha.Network {
	return networkingv1alpha.Network{
		ObjectMeta: meta(name, nil),
		Spec: networkingv1alpha.NetworkSpec{
			IPAM:       networkingv1alpha.NetworkIPAM{Mode: networkingv1alpha.NetworkIPAMModeAuto},
			IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol},
			MTU:        1440,
		},
		Status: networkingv1alpha.NetworkStatus{
			IPAM: &networkingv1alpha.NetworkIPAMStatus{IPv6Prefix: "fd20:1::/48"},
			Conditions: []metav1.Condition{
				cond("IPAMAllocated", "Allocated", metav1.ConditionTrue, time.Hour),
				cond("Ready", "Ready", metav1.ConditionTrue, time.Hour),
			},
		},
	}
}

func readyContext(network, location string) networkingv1alpha.NetworkContext {
	return networkingv1alpha.NetworkContext{
		ObjectMeta: meta(network+"-"+location, nil),
		Spec: networkingv1alpha.NetworkContextSpec{
			Network:  networkingv1alpha.LocalNetworkRef{Name: network},
			Location: locationRef(location),
		},
		Status: networkingv1alpha.NetworkContextStatus{Conditions: []metav1.Condition{
			cond("IPAMAllocated", "Allocated", metav1.ConditionTrue, time.Hour),
			cond("Ready", "Ready", metav1.ConditionTrue, time.Hour),
		}},
	}
}

func boundInterface(name, location, workload string, serving bool) networkingv1alpha.NetworkInterface {
	holder := cond("HolderAvailable", "HolderAvailable", metav1.ConditionTrue, time.Hour)
	if !serving {
		holder = cond("HolderAvailable", "HolderUnavailable", metav1.ConditionFalse, time.Minute)
	}
	return networkingv1alpha.NetworkInterface{
		ObjectMeta: meta(name, map[string]string{
			WorkloadNameLabel: workload,
			networkingv1alpha.NetworkInterfaceLocationLabel: location,
		}),
		Spec: networkingv1alpha.NetworkInterfaceSpec{Network: networkingv1alpha.LocalNetworkRef{Name: "default"}},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			Phase: networkingv1alpha.NetworkInterfacePhaseBound,
			Conditions: []metav1.Condition{
				cond("Allocated", "Allocated", metav1.ConditionTrue, time.Hour),
				cond("Prepared", "Prepared", metav1.ConditionTrue, time.Hour),
				cond("Programmed", "Programmed", metav1.ConditionTrue, time.Hour),
				holder,
			},
		},
	}
}

func service(name, workload string, conditions ...metav1.Condition) networkingv1alpha.NetworkService {
	return networkingv1alpha.NetworkService{
		ObjectMeta: meta(name, nil),
		Spec: networkingv1alpha.NetworkServiceSpec{
			NetworkInterfaces: networkingv1alpha.NetworkServiceInterfaceSelector{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{WorkloadNameLabel: workload}},
			},
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
		},
		Status: networkingv1alpha.NetworkServiceStatus{Conditions: conditions},
	}
}

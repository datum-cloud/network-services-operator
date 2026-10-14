// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"context"
	"fmt"
	"slices"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const WorkloadNameLabel = "compute.datumapis.com/workload-name"

type evidence struct {
	contexts   []networkingv1alpha.NetworkContext
	bindings   []networkingv1alpha.NetworkBinding
	subnets    []networkingv1alpha.Subnet
	interfaces []networkingv1alpha.NetworkInterface
	claims     []networkingv1alpha.NetworkInterfaceClaim
	services   []networkingv1alpha.NetworkService
	unreadable []string
}

func readAll(ctx context.Context, r Reader, ns string) *evidence {
	e := &evidence{}
	collect := func(what string, err error) {
		if err != nil {
			e.unreadable = append(e.unreadable, fmt.Sprintf("%s: %v", what, err))
		}
	}
	var err error
	e.contexts, err = r.ListNetworkContexts(ctx, ns)
	collect("network locations", err)
	e.bindings, err = r.ListNetworkBindings(ctx, ns)
	collect("network attachments", err)
	e.subnets, err = r.ListSubnets(ctx, ns)
	collect("subnets", err)
	e.interfaces, err = r.ListNetworkInterfaces(ctx, ns)
	collect("network interfaces", err)
	e.claims, err = r.ListNetworkInterfaceClaims(ctx, ns)
	collect("network interface requests", err)
	e.services, err = r.ListNetworkServices(ctx, ns)
	collect("network services", err)
	return e
}

type networkEvidence struct {
	network    *networkingv1alpha.Network
	contexts   []networkingv1alpha.NetworkContext
	bindings   []networkingv1alpha.NetworkBinding
	subnets    []networkingv1alpha.Subnet
	interfaces []networkingv1alpha.NetworkInterface
	claims     []networkingv1alpha.NetworkInterfaceClaim
	services   []networkingv1alpha.NetworkService
	unreadable []string
}

func (e *evidence) forNetwork(n *networkingv1alpha.Network) *networkEvidence {
	out := &networkEvidence{network: n, unreadable: e.unreadable}
	contextNames := map[string]bool{}
	for _, c := range e.contexts {
		if c.Spec.Network.Name == n.Name {
			out.contexts = append(out.contexts, c)
			contextNames[c.Name] = true
		}
	}
	for _, b := range e.bindings {
		if b.Spec.Network.Name == n.Name && (b.Spec.Network.Namespace == "" || b.Spec.Network.Namespace == n.Namespace) {
			out.bindings = append(out.bindings, b)
		}
	}
	for _, s := range e.subnets {
		if contextNames[s.Spec.NetworkContext.Name] {
			out.subnets = append(out.subnets, s)
		}
	}
	for _, i := range e.interfaces {
		if i.Spec.Network.Name == n.Name {
			out.interfaces = append(out.interfaces, i)
		}
	}
	for _, c := range e.claims {
		if c.Spec.Network.Name == n.Name {
			out.claims = append(out.claims, c)
		}
	}
	for _, s := range e.services {
		if slices.Contains(serviceNetworks(&s, e.interfaces), n.Name) {
			out.services = append(out.services, s)
		}
	}
	return out
}

func serviceSelector(s *networkingv1alpha.NetworkService) (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(&s.Spec.NetworkInterfaces.Selector)
}

func serviceMembers(s *networkingv1alpha.NetworkService, interfaces []networkingv1alpha.NetworkInterface) []networkingv1alpha.NetworkInterface {
	sel, err := serviceSelector(s)
	if err != nil || sel.Empty() {
		return nil
	}
	var out []networkingv1alpha.NetworkInterface
	for _, i := range interfaces {
		if i.Namespace == s.Namespace && i.Status.Phase == networkingv1alpha.NetworkInterfacePhaseBound &&
			sel.Matches(labels.Set(i.Labels)) {
			out = append(out, i)
		}
	}
	return out
}

func serviceNetworks(s *networkingv1alpha.NetworkService, interfaces []networkingv1alpha.NetworkInterface) []string {
	var out []string
	for _, m := range serviceMembers(s, interfaces) {
		if !slices.Contains(out, m.Spec.Network.Name) {
			out = append(out, m.Spec.Network.Name)
		}
	}
	sort.Strings(out)
	return out
}

func interfaceLocation(i *networkingv1alpha.NetworkInterface) string {
	return i.Labels[networkingv1alpha.NetworkInterfaceLocationLabel]
}

func workloadOf(obj metav1.Object) string {
	return obj.GetLabels()[WorkloadNameLabel]
}

func workloadsOn(interfaces []networkingv1alpha.NetworkInterface) []string {
	var out []string
	for i := range interfaces {
		if w := workloadOf(&interfaces[i]); w != "" && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

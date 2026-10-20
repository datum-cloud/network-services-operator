// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/agenttime"
)

type NetworkView struct {
	Name            string         `json:"name"`
	Ready           bool           `json:"ready"`
	Deleting        bool           `json:"deleting,omitempty"`
	IPFamilies      []string       `json:"ipFamilies"`
	CarriesIPv4     bool           `json:"carriesIPv4,omitempty"`
	MTU             int32          `json:"mtu,omitempty"`
	AddressMode     string         `json:"addressMode,omitempty"`
	IPv6Prefix      string         `json:"ipv6Prefix,omitempty"`
	Age             string         `json:"age,omitempty"`
	Locations       []LocationView `json:"locations,omitempty"`
	Workloads       []string       `json:"workloads,omitempty"`
	Services        []string       `json:"services,omitempty"`
	KeptInterfaces  []string       `json:"keptInterfaces,omitempty"`
	Diagnosis       string         `json:"diagnosis"`
	RootCauseReason string         `json:"rootCauseReason,omitempty"`
	RootCauseObject string         `json:"rootCauseObject,omitempty"`
	RootCauseActor  Actionability  `json:"rootCauseActionability,omitempty"`
	Unreadable      []string       `json:"unreadable,omitempty"`
}

func buildNetworkView(ne *networkEvidence, now time.Time) NetworkView {
	n := ne.network
	d := diagnoseNetwork(ne, now)
	v := NetworkView{
		Name:        n.Name,
		Ready:       d.Ready,
		Deleting:    d.Deleting,
		MTU:         n.Spec.MTU,
		AddressMode: string(n.Spec.IPAM.Mode),
		Age:         agenttime.HumanDuration(sinceCreation(n, now)),
		Locations:   d.Locations,
		Workloads:   workloadsOn(boundInterfaces(ne.interfaces)),
		Diagnosis:   d.Summary,
		Unreadable:  d.Unreadable,
	}
	for _, f := range n.Spec.IPFamilies {
		v.IPFamilies = append(v.IPFamilies, string(f))
		if f == networkingv1alpha.IPv4Protocol {
			v.CarriesIPv4 = true
		}
	}
	if n.Status.IPAM != nil {
		v.IPv6Prefix = n.Status.IPAM.IPv6Prefix
	}
	for _, s := range ne.services {
		v.Services = append(v.Services, s.Name)
	}
	sort.Strings(v.Services)
	for _, i := range ne.interfaces {
		if i.Status.Phase == networkingv1alpha.NetworkInterfacePhaseAvailable {
			v.KeptInterfaces = append(v.KeptInterfaces, i.Name)
		}
	}
	sort.Strings(v.KeptInterfaces)
	if d.RootCause != nil {
		v.RootCauseReason = d.RootCause.Reason
		v.RootCauseObject = d.RootCause.Object
		v.RootCauseActor = d.RootCause.Actionability
	}
	return v
}

func boundInterfaces(in []networkingv1alpha.NetworkInterface) []networkingv1alpha.NetworkInterface {
	return slices.DeleteFunc(slices.Clone(in), func(i networkingv1alpha.NetworkInterface) bool {
		return i.Status.Phase != networkingv1alpha.NetworkInterfacePhaseBound
	})
}

func sinceCreation(n *networkingv1alpha.Network, now time.Time) time.Duration {
	if !agenttime.Plausible(n.CreationTimestamp) {
		return 0
	}
	return now.Sub(n.CreationTimestamp.Time)
}

type NetworkSummary struct {
	Name          string        `json:"name"`
	Ready         bool          `json:"ready"`
	Deleting      bool          `json:"deleting,omitempty"`
	IPv6Prefix    string        `json:"ipv6Prefix,omitempty"`
	Locations     string        `json:"locations,omitempty"`
	Instances     int           `json:"instances"`
	Workloads     int           `json:"workloads"`
	Services      int           `json:"services"`
	Scope         Scope         `json:"scope,omitempty"`
	Actionability Actionability `json:"actionability,omitempty"`
	Reason        string        `json:"rootCauseReason,omitempty"`
	Object        string        `json:"rootCauseObject,omitempty"`
	For           string        `json:"rootCauseFor,omitempty"`
	Age           string        `json:"age,omitempty"`
}

func summarizeNetwork(ne *networkEvidence, now time.Time) NetworkSummary {
	d := diagnoseNetwork(ne, now)
	s := NetworkSummary{
		Name:      d.Network,
		Ready:     d.Ready,
		Deleting:  d.Deleting,
		Workloads: len(workloadsOn(boundInterfaces(ne.interfaces))),
		Services:  len(ne.services),
		Age:       agenttime.HumanDuration(sinceCreation(ne.network, now)),
	}
	if ne.network.Status.IPAM != nil {
		s.IPv6Prefix = ne.network.Status.IPAM.IPv6Prefix
	}
	ready := 0
	for _, l := range d.Locations {
		s.Instances += l.Instances
		if l.Ready {
			ready++
		}
	}
	if len(d.Locations) > 0 {
		s.Locations = fmt.Sprintf("%d/%d ready", ready, len(d.Locations))
	}
	if c := d.RootCause; c != nil {
		s.Scope, s.Actionability, s.Reason, s.Object, s.For = c.Scope, c.Actionability, c.Reason, c.Object, c.InStateFor
	}
	return s
}

type ServiceSummary struct {
	Name          string        `json:"name"`
	Ready         bool          `json:"ready"`
	Networks      []string      `json:"networks,omitempty"`
	Ports         string        `json:"ports"`
	Selects       string        `json:"selects"`
	Members       string        `json:"members"`
	Scope         Scope         `json:"scope,omitempty"`
	Actionability Actionability `json:"actionability,omitempty"`
	Reason        string        `json:"rootCauseReason,omitempty"`
	For           string        `json:"rootCauseFor,omitempty"`
}

func summarizeService(s *networkingv1alpha.NetworkService, e *evidence, now time.Time) ServiceSummary {
	d := diagnoseService(s, e, now)
	ports := make([]string, 0, len(d.Ports))
	for _, p := range d.Ports {
		port := fmt.Sprintf("%s:%d", p.Name, p.Port)
		if p.AppProtocol != "" {
			port += " (" + AppProtocolLabel(p.AppProtocol) + ")"
		}
		ports = append(ports, port)
	}
	out := ServiceSummary{
		Name:     d.Service,
		Ready:    apimeta.IsStatusConditionTrue(s.Status.Conditions, networkingv1alpha.NetworkServiceReady),
		Networks: d.Networks,
		Ports:    strings.Join(ports, ", "),
		Selects:  d.Selects,
		Members:  fmt.Sprintf("%d/%d serving", d.Members.Healthy, d.Members.Members),
	}
	if c := d.RootCause; c != nil {
		out.Scope, out.Actionability, out.Reason, out.For = c.Scope, c.Actionability, c.Reason, c.InStateFor
	}
	return out
}

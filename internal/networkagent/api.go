// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"context"
	"errors"
	"sort"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/agenttime"
)

func Networks(ctx context.Context, r Reader, ns string) ([]NetworkSummary, error) {
	networks, err := r.ListNetworks(ctx, ns)
	if err != nil {
		return nil, err
	}
	e := readAll(ctx, r, ns)
	now := nowFunc()
	out := make([]NetworkSummary, 0, len(networks))
	for i := range networks {
		out = append(out, summarizeNetwork(e.forNetwork(&networks[i]), now))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.SliceStable(out, func(i, j int) bool { return scopeRank(out[i].Scope) < scopeRank(out[j].Scope) })
	return out, nil
}

func Network(ctx context.Context, r Reader, ns, name string) (NetworkView, error) {
	ne, err := evidenceForNetwork(ctx, r, ns, name)
	if err != nil {
		return NetworkView{}, err
	}
	return buildNetworkView(ne, nowFunc()), nil
}

func DiagnoseNetwork(ctx context.Context, r Reader, ns, name string) (*NetworkDiagnosis, error) {
	ne, err := evidenceForNetwork(ctx, r, ns, name)
	if err != nil {
		return nil, err
	}
	return diagnoseNetwork(ne, nowFunc()), nil
}

func evidenceForNetwork(ctx context.Context, r Reader, ns, name string) (*networkEvidence, error) {
	if name == "" {
		return nil, errors.New("name is required")
	}
	n, err := r.GetNetwork(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	return readAll(ctx, r, ns).forNetwork(n), nil
}

func Services(ctx context.Context, r Reader, ns string) ([]ServiceSummary, error) {
	services, err := r.ListNetworkServices(ctx, ns)
	if err != nil {
		return nil, err
	}
	e := readAll(ctx, r, ns)
	now := nowFunc()
	out := make([]ServiceSummary, 0, len(services))
	for i := range services {
		out = append(out, summarizeService(&services[i], e, now))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.SliceStable(out, func(i, j int) bool { return scopeRank(out[i].Scope) < scopeRank(out[j].Scope) })
	return out, nil
}

func DiagnoseService(ctx context.Context, r Reader, ns, name string) (*ServiceDiagnosis, error) {
	if name == "" {
		return nil, errors.New("name is required")
	}
	s, err := r.GetNetworkService(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	return diagnoseService(s, readAll(ctx, r, ns), nowFunc()), nil
}

type InterfaceSummary struct {
	Name      string   `json:"name"`
	Network   string   `json:"network"`
	Location  string   `json:"location,omitempty"`
	Workload  string   `json:"workload,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Kept      bool     `json:"kept"`
	Serving   bool     `json:"serving"`
	Age       string   `json:"age,omitempty"`
}

func Interfaces(ctx context.Context, r Reader, ns string) ([]InterfaceSummary, error) {
	interfaces, err := r.ListNetworkInterfaces(ctx, ns)
	if err != nil {
		return nil, err
	}
	now := nowFunc()
	out := make([]InterfaceSummary, 0, len(interfaces))
	for i := range interfaces {
		iface := &interfaces[i]
		s := InterfaceSummary{
			Name:     iface.Name,
			Network:  iface.Spec.Network.Name,
			Location: interfaceLocation(iface),
			Workload: workloadOf(iface),
			Kept:     iface.Status.Phase == networkingv1alpha.NetworkInterfacePhaseAvailable,
			Serving: iface.Status.Phase == networkingv1alpha.NetworkInterfacePhaseBound &&
				apimeta.IsStatusConditionTrue(iface.Status.Conditions, networkingv1alpha.NetworkInterfaceHolderAvailable),
		}
		for _, a := range iface.Spec.Addresses {
			s.Addresses = append(s.Addresses, a.Address)
		}
		for _, a := range iface.Spec.ExternalAddresses {
			s.Addresses = append(s.Addresses, a.Address)
		}
		if agenttime.Plausible(iface.CreationTimestamp) {
			s.Age = agenttime.HumanDuration(now.Sub(iface.CreationTimestamp.Time))
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Network != out[j].Network {
			return out[i].Network < out[j].Network
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func Render(in RenderInput) (map[string]any, []string, error) {
	switch in.Kind {
	case KindNetwork:
		return renderNetwork(in)
	case KindNetworkService:
		return renderService(in)
	default:
		return nil, nil, errors.New("kind must be " + strings.Join([]string{KindNetwork, KindNetworkService}, " or ") +
			", not \"" + in.Kind + "\"")
	}
}

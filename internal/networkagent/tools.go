// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ToolList            = "network_list"
	ToolGet             = "network_get"
	ToolDiagnose        = "network_diagnose"
	ToolServiceList     = "network_service_list"
	ToolServiceDiagnose = "network_service_diagnose"
	ToolReasonExplain   = "network_reason_explain"
	ToolRender          = "network_render"
)

type ToolDeps struct {
	Reader    Reader
	Namespace string
}

type DepsFor func(context.Context) (ToolDeps, error)

func readOnly() *mcp.ToolAnnotations {
	notDestructive := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &notDestructive, IdempotentHint: true}
}

func RegisterTools(s *mcp.Server, deps DepsFor) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolList,
		Title:       "List networks",
		Annotations: readOnly(),
		Description: "List every network in the project: whether it is ready, its IPv6 prefix, how many " +
			"locations it has reached and how many of those are ready, how many instances, workloads and " +
			"network services use it, and — when something is wrong — the root-cause reason, what it is on, " +
			"how much it affects, who has to act and for how long it has held. Worst first. Start here. Read-only.",
	}, networkList(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGet,
		Title:       "Get one network",
		Annotations: readOnly(),
		Description: "Get one network as the product rather than as the objects behind it: its address " +
			"families, MTU, IPv6 prefix, every location it has reached with that location's address range and " +
			"how many instances are attached and serving, the workloads and network services on it, network " +
			"interfaces kept after their instance went away, and a one-line diagnosis. Use for the state, and " +
			"before changing or deleting a network. Read-only.",
	}, networkGet(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolDiagnose,
		Title:       "Diagnose a network",
		Annotations: readOnly(),
		Description: "Diagnose why a network, or a workload's use of it, is not working. Walks the network, " +
			"every location it has reached, each location's address range and every instance's network " +
			"interface, and returns the cause that names something — never a summary like \"waiting for the " +
			"location\" when the location itself says why. Returns it with the location and workload it " +
			"affects, how much stops working, how long it has held, who has to act, what to do next, and which " +
			"skill has the procedure. Also returns a confidence: partial means some of the evidence could not " +
			"be read, so a clean result is not a clean bill of health. Read-only.",
	}, networkDiagnose(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolServiceList,
		Title:       "List network services",
		Annotations: readOnly(),
		Description: "List every network service in the project: whether it is serving, the network it is on, " +
			"its ports, what it selects, how many of its instances are serving, and the root cause when it is " +
			"not. Worst first. Read-only.",
	}, serviceList(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolServiceDiagnose,
		Title:       "Diagnose a network service",
		Annotations: readOnly(),
		Description: "Diagnose why a network service is not sending traffic anywhere, or only to some " +
			"locations. Returns what it selects, the network and ports, instances and serving counts per " +
			"location, which instances are not serving and what they report, and the root cause with who has " +
			"to act. When nothing matches, it also lists the workloads that do exist in the project so a " +
			"mismatched selector is easy to spot. Instances that are not serving are usually a workload " +
			"problem, not a network one. Read-only.",
	}, serviceDiagnose(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolReasonExplain,
		Title:       "Explain a network condition reason",
		Annotations: readOnly(),
		Description: "Explain a networking condition reason: what it means in the customer's terms, whether " +
			"it is user-actionable, a platform fault, transient or informational, how long a transient one " +
			"should take, and what to do. The same reason means different things on different kinds — " +
			"\"Pending\" and \"RangeOccupied\" each have several meanings — so pass kind and conditionType " +
			"whenever you have them; a reason on its own returns every meaning and you must pick. Call with no " +
			"arguments to list everything. Read-only.",
	}, reasonExplain(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolRender,
		Title:       "Render a network or network service manifest",
		Annotations: readOnly(),
		Description: "Turn a short description into a complete Network or NetworkService manifest, checked " +
			"against the rules Datum applies when it is created, and report what rendering it settled. Nothing " +
			"is read and nothing is changed, so render as often as it takes. For a network, only the name and " +
			"optionally the MTU are choices: networks are IPv6-only and addressed automatically. For a network " +
			"service, give the workload whose instances answer and the named ports they listen on. Read the " +
			"notes aloud where they say something is final. The manifest then goes to resources_plan and, once " +
			"the person agrees, resources_apply. Load the network-create or network-service-create skill " +
			"before using this. Writes nothing.",
	}, networkRender(deps))
}

type ListInput struct{}

type ListOutput struct {
	Networks []NetworkSummary `json:"networks"`
}

func networkList(deps DepsFor) mcp.ToolHandlerFor[ListInput, ListOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, ListOutput, error) {
		d, err := deps(ctx)
		if err != nil {
			return nil, ListOutput{}, err
		}
		networks, err := d.Reader.ListNetworks(ctx, d.Namespace)
		if err != nil {
			return nil, ListOutput{}, err
		}
		e := readAll(ctx, d.Reader, d.Namespace)
		now := nowFunc()
		out := ListOutput{Networks: make([]NetworkSummary, 0, len(networks))}
		for i := range networks {
			out.Networks = append(out.Networks, summarizeNetwork(e.forNetwork(&networks[i]), now))
		}
		sort.SliceStable(out.Networks, func(i, j int) bool {
			return scopeRank(out.Networks[i].Scope) < scopeRank(out.Networks[j].Scope)
		})
		return nil, out, nil
	}
}

type NameInput struct {
	Name string `json:"name" jsonschema:"the network's name"`
}

type GetOutput struct {
	Network NetworkView `json:"network"`
}

func networkGet(deps DepsFor) mcp.ToolHandlerFor[NameInput, GetOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in NameInput) (*mcp.CallToolResult, GetOutput, error) {
		ne, err := networkEvidenceFor(ctx, deps, in.Name)
		if err != nil {
			return nil, GetOutput{}, err
		}
		return nil, GetOutput{Network: buildNetworkView(ne, nowFunc())}, nil
	}
}

type DiagnoseOutput struct {
	Diagnosis *NetworkDiagnosis `json:"diagnosis"`
}

func networkDiagnose(deps DepsFor) mcp.ToolHandlerFor[NameInput, DiagnoseOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in NameInput) (*mcp.CallToolResult, DiagnoseOutput, error) {
		ne, err := networkEvidenceFor(ctx, deps, in.Name)
		if err != nil {
			return nil, DiagnoseOutput{}, err
		}
		return nil, DiagnoseOutput{Diagnosis: diagnoseNetwork(ne, nowFunc())}, nil
	}
}

func networkEvidenceFor(ctx context.Context, deps DepsFor, name string) (*networkEvidence, error) {
	d, err := deps(ctx)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errors.New("name is required")
	}
	n, err := d.Reader.GetNetwork(ctx, d.Namespace, name)
	if err != nil {
		return nil, err
	}
	return readAll(ctx, d.Reader, d.Namespace).forNetwork(n), nil
}

type ServiceListOutput struct {
	Services []ServiceSummary `json:"services"`
}

func serviceList(deps DepsFor) mcp.ToolHandlerFor[ListInput, ServiceListOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, ServiceListOutput, error) {
		d, err := deps(ctx)
		if err != nil {
			return nil, ServiceListOutput{}, err
		}
		services, err := d.Reader.ListNetworkServices(ctx, d.Namespace)
		if err != nil {
			return nil, ServiceListOutput{}, err
		}
		e := readAll(ctx, d.Reader, d.Namespace)
		now := nowFunc()
		out := ServiceListOutput{Services: make([]ServiceSummary, 0, len(services))}
		for i := range services {
			out.Services = append(out.Services, summarizeService(&services[i], e, now))
		}
		sort.SliceStable(out.Services, func(i, j int) bool {
			return scopeRank(out.Services[i].Scope) < scopeRank(out.Services[j].Scope)
		})
		return nil, out, nil
	}
}

type ServiceNameInput struct {
	Name string `json:"name" jsonschema:"the network service's name"`
}

type ServiceDiagnoseOutput struct {
	Diagnosis *ServiceDiagnosis `json:"diagnosis"`
}

func serviceDiagnose(deps DepsFor) mcp.ToolHandlerFor[ServiceNameInput, ServiceDiagnoseOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ServiceNameInput) (*mcp.CallToolResult, ServiceDiagnoseOutput, error) {
		d, err := deps(ctx)
		if err != nil {
			return nil, ServiceDiagnoseOutput{}, err
		}
		if in.Name == "" {
			return nil, ServiceDiagnoseOutput{}, errors.New("name is required")
		}
		s, err := d.Reader.GetNetworkService(ctx, d.Namespace, in.Name)
		if err != nil {
			return nil, ServiceDiagnoseOutput{}, err
		}
		e := readAll(ctx, d.Reader, d.Namespace)
		return nil, ServiceDiagnoseOutput{Diagnosis: diagnoseService(s, e, nowFunc())}, nil
	}
}

type ReasonExplainInput struct {
	Reason        string `json:"reason,omitempty" jsonschema:"the condition reason to explain; omit to list every reason"`
	Kind          string `json:"kind,omitempty" jsonschema:"the kind that reported it, e.g. Network, NetworkContext, NetworkBinding, Subnet, NetworkInterface, NetworkInterfaceClaim, NetworkService"`
	ConditionType string `json:"conditionType,omitempty" jsonschema:"the condition the reason appeared on, e.g. Ready"`
}

type ReasonExplainOutput struct {
	Reason    *ReasonInfo  `json:"reason,omitempty"`
	Reasons   []ReasonInfo `json:"reasons,omitempty"`
	Ambiguous bool         `json:"ambiguous,omitempty"`
	Note      string       `json:"note,omitempty"`
}

func reasonExplain(deps DepsFor) mcp.ToolHandlerFor[ReasonExplainInput, ReasonExplainOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in ReasonExplainInput) (*mcp.CallToolResult, ReasonExplainOutput, error) {
		if _, err := deps(ctx); err != nil {
			return nil, ReasonExplainOutput{}, err
		}
		if in.Reason == "" {
			return nil, ReasonExplainOutput{Reasons: AllReasons()}, nil
		}

		var matches []ReasonInfo
		for _, info := range ExplainAnyReason(in.Reason) {
			if (in.Kind == "" || info.Kind == in.Kind) && (in.ConditionType == "" || info.ConditionType == in.ConditionType) {
				matches = append(matches, info)
			}
		}
		switch {
		case len(matches) == 0:
			return nil, ReasonExplainOutput{}, fmt.Errorf("no explanation for reason %q on kind %q condition %q",
				in.Reason, in.Kind, in.ConditionType)
		case len(matches) == 1:
			return nil, ReasonExplainOutput{Reason: &matches[0]}, nil
		case sameMeaning(matches):
			return nil, ReasonExplainOutput{Reason: &matches[0], Note: fmt.Sprintf(
				"%q means the same on every condition it appears on here.", in.Reason)}, nil
		default:
			return nil, ReasonExplainOutput{
				Reasons:   matches,
				Ambiguous: true,
				Note: fmt.Sprintf("%q means different things on different kinds and conditions. Pick the entry "+
					"whose kind and conditionType match what you are looking at; do not assume the first.", in.Reason),
			}, nil
		}
	}
}

func sameMeaning(infos []ReasonInfo) bool {
	for _, i := range infos[1:] {
		if i.Explanation != infos[0].Explanation || i.Kind != infos[0].Kind {
			return false
		}
	}
	return true
}

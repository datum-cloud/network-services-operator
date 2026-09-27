// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// The tools this service publishes to an assistant.
//
// There is deliberately no mutating tool. Changing a load balancer goes through
// the assistant's own plan and apply tools, which hold the confirmation step
// for every service at once: the plan returns the exact manifests and a token
// standing for them, and apply refuses if either has moved. A write tool here
// would duplicate that and bypass the token.
//
// Every name carries the alb_ prefix, so an assistant can compose tools from
// several services in one conversation without them colliding. The capability
// document registers the prefixed names.
const (
	ToolList           = "alb_list"
	ToolGet            = "alb_get"
	ToolDiagnose       = "alb_diagnose"
	ToolReasonExplain  = "alb_reason_explain"
	ToolTrafficSummary = "alb_traffic_summary"
)

// ToolDeps is what one request's tool calls operate over: where to read from,
// and which namespace they are confined to.
type ToolDeps struct {
	Reader    Reader
	Namespace string
	// Logs reads access logs, and may be nil. A project entitled to load
	// balancers is not necessarily entitled to observability, so its absence is
	// reported as a capability the project lacks rather than as an error.
	Logs LogReader
}

// DepsFor resolves the dependencies for a tool call. A function rather than a
// value so the caller decides how identity and project are established: the
// server derives both from the HTTP request, tests supply them directly.
type DepsFor func(context.Context) (ToolDeps, error)

// RegisterTools adds every tool to an MCP server.
func RegisterTools(s *mcp.Server, deps DepsFor) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolList,
		Title: "List load balancers",
		Description: "List every Application Load Balancer in the project: whether it is serving, its " +
			"generated hostname, how many custom hostnames are attached and how many of those are " +
			"fully working, whether traffic protection is attached and in which mode, and — when " +
			"something is wrong — the root-cause reason, which hostname it affects, whether it is " +
			"user-actionable, a platform fault, transient or stalled, and how long it has held. " +
			"Worst first. Start here. Read-only.",
	}, albList(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolGet,
		Title: "Get one load balancer",
		Description: "Get one Application Load Balancer assembled as the product rather than as the " +
			"objects behind it: its generated hostname, every custom hostname with its progress " +
			"(claimed, ownership proven, DNS record written, certificate issued), the routes and the " +
			"origins behind each, Force HTTPS, any Host override, traffic protection mode and " +
			"paranoia level, and whether basic auth is on and which usernames it accepts — never " +
			"passwords or hashes. Use when you need the state rather than a diagnosis. Read-only.",
	}, albGet(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolDiagnose,
		Title: "Diagnose a load balancer",
		Description: "Diagnose why an Application Load Balancer is not working. Walks the load " +
			"balancer, every hostname, and the domain behind each, and returns the deepest condition " +
			"that names a real cause — never an aggregate like PartialFailure or CertificatesPending, " +
			"which say \"one or more hostnames\" and name none. Returns that cause with the hostname " +
			"it affects, how much stops working, how long it has held, who has to act, what to do " +
			"next, and which skill has the procedure. Also returns a confidence: a load balancer can " +
			"report every condition true and still not be reachable, because nothing publishes " +
			"whether the edge can serve it yet, so a clean result comes back as unverified rather " +
			"than as working. Read-only.",
	}, albDiagnose(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolReasonExplain,
		Title: "Explain a condition reason",
		Description: "Explain a load balancer condition reason: what it means in the customer's terms, " +
			"whether it is user-actionable, a platform fault, transient or informational, how long a " +
			"transient one should take, and what to do about it. Pass the condition type as well as " +
			"the reason wherever you have it: several reasons here mean different things on different " +
			"conditions — \"Pending\" alone is three different answers — so a reason on its own " +
			"returns every meaning it has and you must pick by condition type. Call with no arguments " +
			"to list everything. Read-only.",
	}, albReasonExplain(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:  ToolTrafficSummary,
		Title: "Summarise traffic to a load balancer",
		Description: "Summarise the requests that actually reached an Application Load Balancer " +
			"over a time window: how many, the response-code breakdown, the edge's own response " +
			"flags, which hostnames were asked for, and a sample of recent lines. This is the only " +
			"evidence that a load balancer is really serving — its status reports configuration, " +
			"not reachability, so use this to settle a diagnosis that came back unverified. " +
			"**No traffic is not a fault**: a load balancer nobody has called looks exactly like " +
			"one that is broken, so never report an empty result as a diagnosis. Filters by " +
			"method, response code and hostname. Read-only.",
	}, albTrafficSummary(deps))
}

// ---------------------------------------------------------------- alb_list

// LoadBalancerSummary is one row of the fleet view.
type LoadBalancerSummary struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	// Serving is what the platform reports, which is not the same as reachable;
	// read Confidence alongside it.
	Serving           bool       `json:"serving"`
	Confidence        Confidence `json:"confidence"`
	GeneratedHostname string     `json:"generatedHostname,omitempty"`
	// CustomHostnames renders "2/3": how many are fully working out of how many
	// are attached.
	CustomHostnames string         `json:"customHostnames,omitempty"`
	Protection      ProtectionView `json:"protection"`
	Origin          string         `json:"origin,omitempty"`

	RootCauseReason        string        `json:"rootCauseReason,omitempty"`
	RootCauseConditionType string        `json:"rootCauseConditionType,omitempty"`
	RootCauseHostname      string        `json:"rootCauseHostname,omitempty"`
	Scope                  Scope         `json:"scope,omitempty"`
	Actionability          Actionability `json:"actionability,omitempty"`
	RootCauseFor           string        `json:"rootCauseFor,omitempty"`
	Age                    string        `json:"age,omitempty"`
}

type ListInput struct{}

type ListOutput struct {
	LoadBalancers []LoadBalancerSummary `json:"loadBalancers"`
}

func albList(deps DepsFor) mcp.ToolHandlerFor[ListInput, ListOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ ListInput) (*mcp.CallToolResult, ListOutput, error) {
		d, err := deps(ctx)
		if err != nil {
			return nil, ListOutput{}, err
		}

		proxies, err := d.Reader.ListProxies(ctx, d.Namespace)
		if err != nil {
			return nil, ListOutput{}, err
		}

		out := ListOutput{LoadBalancers: make([]LoadBalancerSummary, 0, len(proxies))}
		for i := range proxies {
			diag := diagnoseProxy(ctx, d.Reader, d.Namespace, &proxies[i], nowFunc())
			out.LoadBalancers = append(out.LoadBalancers, summaryOf(&proxies[i], diag))
		}

		// Worst first: the point of a fleet view is what needs attention.
		sort.SliceStable(out.LoadBalancers, func(i, j int) bool {
			return scopeRank(out.LoadBalancers[i].Scope) < scopeRank(out.LoadBalancers[j].Scope)
		})
		return nil, out, nil
	}
}

func summaryOf(proxy *networkingv1alpha.HTTPProxy, d *Diagnosis) LoadBalancerSummary {
	s := LoadBalancerSummary{
		Name:              d.LoadBalancer,
		DisplayName:       d.DisplayName,
		Serving:           d.Serving,
		Confidence:        d.Confidence,
		GeneratedHostname: d.GeneratedHostname,
		Protection:        d.Protection,
		Origin:            spec.OriginSummary(proxy),
		Age:               d.ObjectAge,
	}

	attached, working := 0, 0
	for _, h := range d.Hostnames {
		if h.Generated {
			continue
		}
		attached++
		if h.Working {
			working++
		}
	}
	if attached > 0 {
		s.CustomHostnames = fmt.Sprintf("%d/%d", working, attached)
	}

	if d.RootCause != nil {
		s.RootCauseReason = d.RootCause.Reason
		s.RootCauseConditionType = d.RootCause.ConditionType
		s.RootCauseHostname = d.RootCause.Hostname
		s.Scope = d.RootCause.Scope
		s.Actionability = d.RootCause.Actionability
		s.RootCauseFor = d.RootCause.InStateFor
	}
	return s
}

// ----------------------------------------------------------------- alb_get

type GetInput struct {
	Name string `json:"name" jsonschema:"the load balancer's name"`
}

type GetOutput struct {
	LoadBalancer LoadBalancerView `json:"loadBalancer"`
}

func albGet(deps DepsFor) mcp.ToolHandlerFor[GetInput, GetOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in GetInput) (*mcp.CallToolResult, GetOutput, error) {
		d, err := deps(ctx)
		if err != nil {
			return nil, GetOutput{}, err
		}
		if in.Name == "" {
			return nil, GetOutput{}, fmt.Errorf("name is required")
		}

		proxy, err := d.Reader.GetProxy(ctx, d.Namespace, in.Name)
		if err != nil {
			return nil, GetOutput{}, err
		}
		return nil, GetOutput{LoadBalancer: buildView(ctx, d.Reader, d.Namespace, proxy, nowFunc())}, nil
	}
}

// ------------------------------------------------------------ alb_diagnose

type DiagnoseInput struct {
	Name string `json:"name" jsonschema:"the load balancer's name"`
}

type DiagnoseOutput struct {
	Diagnosis *Diagnosis `json:"diagnosis"`
}

func albDiagnose(deps DepsFor) mcp.ToolHandlerFor[DiagnoseInput, DiagnoseOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in DiagnoseInput) (*mcp.CallToolResult, DiagnoseOutput, error) {
		d, err := deps(ctx)
		if err != nil {
			return nil, DiagnoseOutput{}, err
		}
		if in.Name == "" {
			return nil, DiagnoseOutput{}, fmt.Errorf("name is required")
		}

		diag, err := DiagnoseAt(ctx, d.Reader, d.Namespace, in.Name, nowFunc())
		if err != nil {
			return nil, DiagnoseOutput{}, err
		}
		return nil, DiagnoseOutput{Diagnosis: diag}, nil
	}
}

// ------------------------------------------------------- alb_reason_explain

type ReasonExplainInput struct {
	Reason        string `json:"reason,omitempty" jsonschema:"the condition reason to explain; omit to list every reason"`
	ConditionType string `json:"conditionType,omitempty" jsonschema:"the condition the reason appeared on; several reasons mean different things on different conditions"`
}

type ReasonExplainOutput struct {
	// Reason is the single answer, when the condition type made it unambiguous.
	Reason *ReasonInfo `json:"reason,omitempty"`
	// Reasons carries every meaning when the request was ambiguous, or the
	// whole catalog when nothing was named.
	Reasons []ReasonInfo `json:"reasons,omitempty"`
	// Ambiguous says the reason has more than one meaning and the caller has to
	// pick by condition type rather than assuming the first.
	Ambiguous bool   `json:"ambiguous,omitempty"`
	Note      string `json:"note,omitempty"`
}

func albReasonExplain(deps DepsFor) mcp.ToolHandlerFor[ReasonExplainInput, ReasonExplainOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in ReasonExplainInput) (*mcp.CallToolResult, ReasonExplainOutput, error) {
		if in.Reason == "" {
			return nil, ReasonExplainOutput{Reasons: AllReasons()}, nil
		}

		if in.ConditionType != "" {
			info, ok := ExplainReason(in.ConditionType, in.Reason)
			if !ok {
				return nil, ReasonExplainOutput{}, fmt.Errorf(
					"no explanation for reason %q on condition %q", in.Reason, in.ConditionType)
			}
			return nil, ReasonExplainOutput{Reason: &info}, nil
		}

		matches := ExplainAnyReason(in.Reason)
		switch len(matches) {
		case 0:
			return nil, ReasonExplainOutput{}, fmt.Errorf("no explanation for reason %q", in.Reason)
		case 1:
			return nil, ReasonExplainOutput{Reason: &matches[0]}, nil
		default:
			return nil, ReasonExplainOutput{
				Reasons:   matches,
				Ambiguous: true,
				Note: fmt.Sprintf(
					"%q means different things on different conditions. Pick the entry whose "+
						"conditionType matches the condition you are looking at; do not assume the first.",
					in.Reason),
			}, nil
		}
	}
}

// --------------------------------------------------- alb_traffic_summary

type TrafficSummaryInput struct {
	Name        string   `json:"name" jsonschema:"the load balancer's name"`
	Since       string   `json:"since,omitempty" jsonschema:"how far back to look, as a duration like 30m or 6h; defaults to 1h"`
	Method      []string `json:"method,omitempty" jsonschema:"only requests using these HTTP methods"`
	Code        []string `json:"code,omitempty" jsonschema:"only responses with these status codes"`
	Host        []string `json:"host,omitempty" jsonschema:"only requests asking for these hostnames"`
	SampleLimit int      `json:"sampleLimit,omitempty" jsonschema:"how many example lines to return alongside the counts; defaults to 20"`
}

type TrafficSummaryOutput struct {
	Summary TrafficSummary `json:"summary"`
}

const (
	defaultTrafficWindow = time.Hour
	maxTrafficWindow     = 24 * time.Hour
	defaultSampleLimit   = 20
	maxSampleLimit       = 50
)

func albTrafficSummary(deps DepsFor) mcp.ToolHandlerFor[TrafficSummaryInput, TrafficSummaryOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in TrafficSummaryInput) (*mcp.CallToolResult, TrafficSummaryOutput, error) {
		d, err := deps(ctx)
		if err != nil {
			return nil, TrafficSummaryOutput{}, err
		}
		if in.Name == "" {
			return nil, TrafficSummaryOutput{}, fmt.Errorf("name is required")
		}

		if d.Logs == nil {
			return nil, TrafficSummaryOutput{Summary: TrafficSummary{
				LoadBalancer:      in.Name,
				UnavailableReason: "This project does not have access logs available.",
			}}, nil
		}

		window := defaultTrafficWindow
		if in.Since != "" {
			parsed, err := time.ParseDuration(in.Since)
			if err != nil || parsed <= 0 {
				return nil, TrafficSummaryOutput{}, fmt.Errorf(
					"since must be a duration like 30m or 6h, not %q", in.Since)
			}
			window = min(parsed, maxTrafficWindow)
		}

		sample := defaultSampleLimit
		if in.SampleLimit > 0 {
			sample = min(in.SampleLimit, maxSampleLimit)
		}

		entries, err := d.Logs.QueryALBLogs(ctx, ALBLogQuery{
			ProxyName: in.Name,
			Since:     window,
			Limit:     util.MaxLogsLimit,
			Methods:   in.Method,
			Codes:     in.Code,
		})
		if err != nil {
			if errors.Is(err, errLogsUnavailable) {
				return nil, TrafficSummaryOutput{Summary: TrafficSummary{
					LoadBalancer:      in.Name,
					UnavailableReason: plainMessage(err),
				}}, nil
			}
			return nil, TrafficSummaryOutput{}, err
		}

		return nil, TrafficSummaryOutput{
			Summary: summariseTraffic(in.Name, entries, in.Host, sample, window),
		}, nil
	}
}

// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
)

// The tools that change a load balancer. Each is one `datumctl alb` command and
// runs the same spec function, so the assistant, datum-mcp and the CLI write a
// load balancer identically: the Host override on the route, Force HTTPS as its
// own rule, and every rule the change does not touch left as it was.
//
// None of them creates or deletes a load balancer, touches traffic protection,
// or sets a password.
const (
	ToolUpdate   = "alb_update"
	ToolHeader   = "alb_header"
	ToolHostname = "alb_hostname"
	ToolRoute    = "alb_route"
	ToolOrigin   = "alb_origin"
)

// confirmFlow ends every change tool's description, so the model meets the
// two-call rule wherever it starts.
const confirmFlow = " Call with dryRun true first and tell the person what will change; only once they agree, " +
	"repeat the call with dryRun false and the confirm value it returned. It refuses if anything moved since."

// mutating annotates a change tool for the client, which then asks the person
// before running it rather than treating it as a free read.
func mutating(destructive bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: false}
}

func registerWriteTools(s *mcp.Server, deps DepsFor) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolUpdate,
		Title:       "Change load balancer-wide settings",
		Annotations: mutating(false),
		Description: "Change settings that apply to a whole Application Load Balancer: its display name, " +
			"Force HTTPS, the load balancing algorithm, and passive health checks. Set only the fields to " +
			"change. Origins, hostnames and headers have their own tools." + confirmFlow,
	}, albUpdate(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolHeader,
		Title:       "Change a request header sent to origins",
		Annotations: mutating(true),
		Description: "Set, add or remove a request header the load balancer sends to its origins. A Host " +
			"override is a header named Host. It applies to every origin on the route, so in a pool whose " +
			"origins have different hostnames it sends some of them the wrong one." + confirmFlow,
	}, albHeader(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolHostname,
		Title:       "Attach or remove a custom hostname",
		Annotations: mutating(true),
		Description: "Attach a custom hostname to a load balancer, or remove one. A hostname can only be " +
			"attached once its domain is verified." + confirmFlow,
	}, albHostname(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolRoute,
		Title:       "Add, replace or remove a route",
		Annotations: mutating(true),
		Description: "Add a route (a path prefix and the origins serving it), replace every origin on a route, " +
			"or remove a route. To change one origin in a pool, use alb_origin instead." + confirmFlow,
	}, albRoute(deps))

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolOrigin,
		Title:       "Add, reweight or remove one origin",
		Annotations: mutating(true),
		Description: "Add one origin to a route, change its weight, or remove it. Weight 0 drains an origin: " +
			"it stays on the route and gets no requests." + confirmFlow,
	}, albOrigin(deps))
}

// Origin names one origin: a URL, or a network service and one of its declared
// port names.
type Origin struct {
	Endpoint       string `json:"endpoint,omitempty" jsonschema:"URL with scheme"`
	TLSHostname    string `json:"tlsHostname,omitempty" jsonschema:"certificate hostname, for an https endpoint given as an IP"`
	NetworkService string `json:"networkService,omitempty" jsonschema:"instead of an endpoint"`
	Port           string `json:"port,omitempty" jsonschema:"the network service's port name, not a number"`
	Weight         *int32 `json:"weight,omitempty" jsonschema:"share relative to the route's other origins; 0 drains it"`
}

func (o Origin) backend() (spec.BackendInput, error) {
	endpoint := strings.TrimSpace(o.Endpoint)
	service := strings.TrimSpace(o.NetworkService)
	switch {
	case endpoint == "" && service == "":
		return spec.BackendInput{}, fmt.Errorf("an origin needs an endpoint or a networkService")
	case endpoint != "" && service != "":
		return spec.BackendInput{}, fmt.Errorf("an origin is an endpoint or a networkService, not both")
	case endpoint != "" && !strings.Contains(endpoint, "://"):
		return spec.BackendInput{}, fmt.Errorf("endpoint %q has no scheme; use a full URL such as https://%s", endpoint, endpoint)
	case service != "" && strings.TrimSpace(o.Port) == "":
		return spec.BackendInput{}, fmt.Errorf("network service %q needs a port name", service)
	case service != "" && o.TLSHostname != "":
		return spec.BackendInput{}, fmt.Errorf("tlsHostname only applies to an endpoint")
	}
	if o.Weight != nil {
		if err := spec.ValidateWeight(*o.Weight); err != nil {
			return spec.BackendInput{}, toolError(err)
		}
	}
	return spec.BackendInput{
		Endpoint:       endpoint,
		TLSHostname:    strings.TrimSpace(o.TLSHostname),
		NetworkService: service,
		Port:           strings.TrimSpace(o.Port),
		Weight:         o.Weight,
	}, nil
}

func describeOrigin(b spec.BackendInput) string {
	return spec.FormatBackend(spec.ToBackend(b))
}

// routePath defaults to the default route, which is where a single-route load
// balancer keeps its origins.
func routePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return spec.DefaultRoutePath, nil
	}
	p, err := spec.NormalizePath(path)
	return p, toolError(err)
}

// ---------------------------------------------------------------- alb_update

type UpdateInput struct {
	Name               string  `json:"name" jsonschema:"the load balancer's name"`
	DisplayName        *string `json:"displayName,omitempty" jsonschema:"empty clears it"`
	ForceHTTPS         *bool   `json:"forceHttps,omitempty" jsonschema:"redirect HTTP requests to HTTPS"`
	Algorithm          string  `json:"algorithm,omitempty" jsonschema:"round-robin, random, least-request, consistent-hash, or default"`
	HashHeader         string  `json:"hashHeader,omitempty" jsonschema:"header to hash on; implies consistent-hash"`
	HealthChecks       *bool   `json:"healthChecks,omitempty" jsonschema:"passive health checks on or off"`
	Consecutive5xx     *int32  `json:"consecutive5xx,omitempty" jsonschema:"default 5"`
	BaseEjectionTime   *string `json:"baseEjectionTime,omitempty" jsonschema:"e.g. 30s or 2m; default 30s"`
	MaxEjectionPercent *int32  `json:"maxEjectionPercent,omitempty" jsonschema:"1-100; default 50"`
	DryRun             bool    `json:"dryRun,omitempty" jsonschema:"preview only; changes nothing"`
	Confirm            string  `json:"confirm,omitempty" jsonschema:"from the preview the person agreed to"`
}

func albUpdate(deps DepsFor) mcp.ToolHandlerFor[UpdateInput, ChangeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateInput) (*mcp.CallToolResult, ChangeOutput, error) {
		update := spec.UpdateInput{
			DisplayName:  in.DisplayName,
			ForceHTTPS:   in.ForceHTTPS,
			LoadBalancer: spec.LoadBalancerInput{Algorithm: in.Algorithm, HashHeader: in.HashHeader},
			HealthCheck: spec.HealthCheckInput{
				Enabled:              in.HealthChecks,
				Consecutive5xxErrors: in.Consecutive5xx,
				BaseEjectionTime:     in.BaseEjectionTime,
				MaxEjectionPercent:   in.MaxEjectionPercent,
			},
		}
		if update.DisplayName == nil && update.ForceHTTPS == nil &&
			!update.LoadBalancer.IsSet() && !update.HealthCheck.IsSet() {
			return nil, ChangeOutput{}, fmt.Errorf("nothing to update: set displayName, forceHttps, algorithm, hashHeader or healthChecks")
		}

		var parts []string
		if in.DisplayName != nil {
			parts = append(parts, fmt.Sprintf("display name %q", *in.DisplayName))
		}
		if in.ForceHTTPS != nil {
			parts = append(parts, map[bool]string{true: "Force HTTPS on", false: "Force HTTPS off"}[*in.ForceHTTPS])
		}
		if update.LoadBalancer.IsSet() {
			parts = append(parts, "the load balancing algorithm")
		}
		if update.HealthCheck.IsSet() {
			parts = append(parts, "passive health checks")
		}

		out, err := runChange(ctx, deps, proxyChange{
			name:    in.Name,
			summary: fmt.Sprintf("Sets %s on %q.", strings.Join(parts, ", "), in.Name),
			mutate: func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.ApplyHTTPProxyUpdate(current, update)
			},
		}, in.DryRun, in.Confirm)
		return nil, out, err
	}
}

// ---------------------------------------------------------------- alb_header

type HeaderInput struct {
	Name    string `json:"name" jsonschema:"the load balancer's name"`
	Action  string `json:"action" jsonschema:"set, add (alongside any value), or unset"`
	Header  string `json:"header" jsonschema:"e.g. Host"`
	Value   string `json:"value,omitempty" jsonschema:"required for set and add"`
	DryRun  bool   `json:"dryRun,omitempty" jsonschema:"preview only; changes nothing"`
	Confirm string `json:"confirm,omitempty" jsonschema:"from the preview the person agreed to"`
}

func albHeader(deps DepsFor) mcp.ToolHandlerFor[HeaderInput, ChangeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in HeaderInput) (*mcp.CallToolResult, ChangeOutput, error) {
		var (
			mutate  func(*networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error)
			summary string
		)
		switch in.Action {
		case "set", "add":
			if in.Value == "" {
				return nil, ChangeOutput{}, fmt.Errorf("value is required to %s a header", in.Action)
			}
			op := spec.SetRequestHeader
			summary = fmt.Sprintf("Sends %s: %s to the origins of %q, replacing any value.", in.Header, in.Value, in.Name)
			if in.Action == "add" {
				op = spec.AddRequestHeader
				summary = fmt.Sprintf("Sends %s: %s to the origins of %q, alongside any value already sent.", in.Header, in.Value, in.Name)
			}
			mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return op(current, in.Header, in.Value)
			}
		case "unset":
			summary = fmt.Sprintf("Stops overriding %s on requests to the origins of %q.", in.Header, in.Name)
			mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.UnsetRequestHeader(current, in.Header)
			}
		default:
			return nil, ChangeOutput{}, fmt.Errorf("action must be set, add or unset, not %q", in.Action)
		}

		out, err := runChange(ctx, deps, proxyChange{name: in.Name, summary: summary, mutate: mutate}, in.DryRun, in.Confirm)
		return nil, out, err
	}
}

// ---------------------------------------------------------------- alb_hostname

type HostnameInput struct {
	Name     string `json:"name" jsonschema:"the load balancer's name"`
	Action   string `json:"action" jsonschema:"add or remove"`
	Hostname string `json:"hostname" jsonschema:"e.g. app.example.com"`
	DryRun   bool   `json:"dryRun,omitempty" jsonschema:"preview only; changes nothing"`
	Confirm  string `json:"confirm,omitempty" jsonschema:"from the preview the person agreed to"`
}

func albHostname(deps DepsFor) mcp.ToolHandlerFor[HostnameInput, ChangeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in HostnameInput) (*mcp.CallToolResult, ChangeOutput, error) {
		c := proxyChange{name: in.Name}
		switch in.Action {
		case "add":
			c.summary = fmt.Sprintf("Attaches %s to %q.", in.Hostname, in.Name)
			c.hostnames = []string{in.Hostname}
			c.mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.AddHostname(current, in.Hostname)
			}
		case "remove":
			c.summary = fmt.Sprintf("Removes %s from %q. Requests for it stop reaching this load balancer.", in.Hostname, in.Name)
			c.mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.RemoveHostname(current, in.Hostname)
			}
		default:
			return nil, ChangeOutput{}, fmt.Errorf("action must be add or remove, not %q", in.Action)
		}

		out, err := runChange(ctx, deps, c, in.DryRun, in.Confirm)
		return nil, out, err
	}
}

// ---------------------------------------------------------------- alb_route

type RouteInput struct {
	Name    string   `json:"name" jsonschema:"the load balancer's name"`
	Action  string   `json:"action" jsonschema:"add, update (replace its origins), or remove"`
	Path    string   `json:"path" jsonschema:"path prefix, e.g. /api; / is the default route"`
	Origins []Origin `json:"origins,omitempty" jsonschema:"required for add and update"`
	Force   bool     `json:"force,omitempty" jsonschema:"remove / while other routes exist, leaving unmatched requests unanswered"`
	DryRun  bool     `json:"dryRun,omitempty" jsonschema:"preview only; changes nothing"`
	Confirm string   `json:"confirm,omitempty" jsonschema:"from the preview the person agreed to"`
}

func albRoute(deps DepsFor) mcp.ToolHandlerFor[RouteInput, ChangeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in RouteInput) (*mcp.CallToolResult, ChangeOutput, error) {
		path, err := spec.NormalizePath(in.Path)
		if err != nil {
			return nil, ChangeOutput{}, toolError(err)
		}
		backends := make([]spec.BackendInput, 0, len(in.Origins))
		names := make([]string, 0, len(in.Origins))
		for _, o := range in.Origins {
			b, err := o.backend()
			if err != nil {
				return nil, ChangeOutput{}, err
			}
			backends = append(backends, b)
			names = append(names, describeOrigin(b))
		}

		c := proxyChange{name: in.Name, services: backends}
		switch in.Action {
		case "add", "update":
			if len(backends) == 0 {
				return nil, ChangeOutput{}, fmt.Errorf("origins are required to %s a route", in.Action)
			}
			c.summary = fmt.Sprintf("Adds route %s on %q, sending it to %s.", path, in.Name, strings.Join(names, ", "))
			c.mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.AddRoute(current, path, backends)
			}
			if in.Action == "update" {
				c.summary = fmt.Sprintf("Replaces every origin on route %s of %q with %s.", path, in.Name, strings.Join(names, ", "))
				c.mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
					return spec.ReplaceRouteBackends(current, path, backends)
				}
			}
		case "remove":
			if len(backends) > 0 {
				return nil, ChangeOutput{}, fmt.Errorf("origins do not apply when removing a route")
			}
			c.summary = fmt.Sprintf("Removes route %s from %q. Its requests fall to whichever route matches next.", path, in.Name)
			c.mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.RemoveRoute(current, path, in.Force)
			}
		default:
			return nil, ChangeOutput{}, fmt.Errorf("action must be add, update or remove, not %q", in.Action)
		}

		out, err := runChange(ctx, deps, c, in.DryRun, in.Confirm)
		return nil, out, err
	}
}

// ---------------------------------------------------------------- alb_origin

type OriginChangeInput struct {
	Name    string `json:"name" jsonschema:"the load balancer's name"`
	Action  string `json:"action" jsonschema:"add, update (its weight), or remove"`
	Path    string `json:"path,omitempty" jsonschema:"route path prefix; default /"`
	Origin  Origin `json:"origin" jsonschema:"as on the route; for update, with the new weight"`
	DryRun  bool   `json:"dryRun,omitempty" jsonschema:"preview only; changes nothing"`
	Confirm string `json:"confirm,omitempty" jsonschema:"from the preview the person agreed to"`
}

func albOrigin(deps DepsFor) mcp.ToolHandlerFor[OriginChangeInput, ChangeOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in OriginChangeInput) (*mcp.CallToolResult, ChangeOutput, error) {
		path, err := routePath(in.Path)
		if err != nil {
			return nil, ChangeOutput{}, err
		}
		backend, err := in.Origin.backend()
		if err != nil {
			return nil, ChangeOutput{}, err
		}
		origin := describeOrigin(backend)

		c := proxyChange{name: in.Name}
		switch in.Action {
		case "add":
			c.services = []spec.BackendInput{backend}
			c.summary = fmt.Sprintf("Adds %s to route %s of %q.", origin, path, in.Name)
			c.mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.AddRouteBackend(current, path, backend)
			}
		case "update":
			if backend.Weight == nil {
				return nil, ChangeOutput{}, fmt.Errorf("give the origin's new weight to update it")
			}
			weight := *backend.Weight
			backend.Weight = nil
			c.summary = fmt.Sprintf("Gives %s on route %s of %q weight %d.", origin, path, in.Name, weight)
			c.mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.SetRouteBackendWeight(current, path, backend, weight)
			}
		case "remove":
			c.summary = fmt.Sprintf("Removes %s from route %s of %q. Its share moves to the route's other origins.", origin, path, in.Name)
			c.mutate = func(current *networkingv1alpha.HTTPProxy) (*networkingv1alpha.HTTPProxy, error) {
				return spec.RemoveRouteBackend(current, path, backend)
			}
		default:
			return nil, ChangeOutput{}, fmt.Errorf("action must be add, update or remove, not %q", in.Action)
		}

		out, err := runChange(ctx, deps, c, in.DryRun, in.Confirm)
		return nil, out, err
	}
}

// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"fmt"
	"time"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/agenttime"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
)

// nowFunc is the clock the tools read. A variable so tests can pin it.
var nowFunc = time.Now

// LoadBalancerView is one Application Load Balancer as the product, rather than
// as the objects it is stored in.
//
// Every field here is decoded through internal/cmd/alb/spec, which is the same
// package the CLI reads, so the two cannot describe the same load balancer
// differently.
type LoadBalancerView struct {
	Name              string `json:"name"`
	DisplayName       string `json:"displayName,omitempty"`
	GeneratedHostname string `json:"generatedHostname,omitempty"`
	Age               string `json:"age,omitempty"`

	ForceHTTPS bool   `json:"forceHTTPS"`
	HostHeader string `json:"hostHeader,omitempty"`

	Routes []RouteView `json:"routes,omitempty"`
	// LoadBalancing is how each route splits requests across its origins,
	// worded as the CLI's describe words it: "least request (default)" when
	// nothing is set.
	LoadBalancing string `json:"loadBalancing"`
	// HealthChecks is "off", or the passive settings with defaults filled in.
	HealthChecks string `json:"healthChecks"`

	Hostnames      []HostnameProgress `json:"hostnames,omitempty"`
	RequestHeaders []HeaderView       `json:"requestHeaders,omitempty"`
	Protection     ProtectionView     `json:"protection"`
	BasicAuth      BasicAuthView      `json:"basicAuth"`

	Conditions []ConditionView `json:"conditions,omitempty"`
}

// RouteView is a path and the origins serving it.
type RouteView struct {
	Path     string        `json:"path"`
	Backends []BackendView `json:"backends,omitempty"`
	// Advanced marks a route written outside this product's shape — an exact or
	// regex path, a header or method match. It is reported and left alone.
	Advanced bool `json:"advanced,omitempty"`
	// Drained marks a route whose every origin is at weight 0. It is published
	// and serves nothing.
	Drained bool `json:"drained,omitempty"`
}

// BackendView is one origin behind a route.
type BackendView struct {
	// Target is the origin as a person would write it: a URL, or
	// "service:port" for a network service.
	Target string `json:"target"`
	Kind   string `json:"kind"`
	// Weight is relative to the route's other origins; 1 when unset.
	Weight int32 `json:"weight"`
	// Share is this origin's part of the route's requests, as the CLI and the
	// portal show it: whole percentages that add up to 100%.
	Share string `json:"share"`
}

// HeaderView is one request header override.
type HeaderView struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Action string `json:"action"`
}

// BasicAuthView reports whether a username and password are required. It
// carries usernames and never a password or a hash.
type BasicAuthView struct {
	Enabled   bool                 `json:"enabled"`
	Usernames []string             `json:"usernames,omitempty"`
	Policies  []BasicAuthRulesView `json:"policies,omitempty"`
	Unknown   bool                 `json:"unknown,omitempty"`
	Message   string               `json:"message,omitempty"`
}

type BasicAuthRulesView struct {
	Name      string   `json:"name"`
	AllRoutes bool     `json:"allRoutes,omitempty"`
	Rules     []string `json:"rules,omitempty"`
	Listeners []string `json:"listeners,omitempty"`
	Usernames []string `json:"usernames,omitempty"`
	State     string   `json:"state"`
	Message   string   `json:"message,omitempty"`
	Secret    string   `json:"secret,omitempty"`
}

// ConditionView is the part of a condition worth spending tokens on.
type ConditionView struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// buildView assembles the product view of one load balancer.
func buildView(
	ctx context.Context,
	r Reader,
	namespace string,
	proxy *networkingv1alpha.HTTPProxy,
	now time.Time,
) LoadBalancerView {
	v := LoadBalancerView{
		Name:              proxy.Name,
		DisplayName:       spec.DisplayName(proxy),
		GeneratedHostname: proxy.Status.CanonicalHostname,
		Age:               agenttime.HumanDuration(sinceCreation(proxy.CreationTimestamp, now)),
		ForceHTTPS:        spec.ForceHTTPS(proxy),
		HostHeader:        spec.HostHeader(proxy),
		LoadBalancing:     spec.LoadBalancingSummary(proxy),
		HealthChecks:      spec.HealthCheckSummary(proxy),
	}

	for _, route := range spec.UserRoutes(proxy) {
		rv := RouteView{Path: route.Path, Advanced: route.Advanced, Drained: len(route.Backends) > 0}
		shares := spec.ShareLabels(route.Backends)
		for i, b := range route.Backends {
			weight := spec.BackendWeight(b)
			if weight > 0 {
				rv.Drained = false
			}
			rv.Backends = append(rv.Backends, BackendView{
				Target: spec.FormatBackend(b),
				Kind:   spec.BackendKind(b),
				Weight: weight,
				Share:  shares[i],
			})
		}
		v.Routes = append(v.Routes, rv)
	}

	set, add, remove := spec.ListRequestHeaders(proxy)
	v.RequestHeaders = append(v.RequestHeaders, headerViews("set", set)...)
	v.RequestHeaders = append(v.RequestHeaders, headerViews("add", add)...)
	for _, name := range remove {
		v.RequestHeaders = append(v.RequestHeaders, HeaderView{Name: name, Action: "remove"})
	}

	domains, err := r.ListDomains(ctx, namespace)
	if err != nil {
		domains = nil
	}
	v.Hostnames = hostnameProgress(proxy, domains)

	if policies, err := r.ListProtectionPolicies(ctx, namespace); err == nil {
		for i := range policies {
			if spec.TPPTargetsProxy(&policies[i], proxy.Name) {
				v.Protection = ProtectionView{
					Attached: true,
					Policy:   policies[i].Name,
					Mode:     spec.TPPMode(&policies[i]),
					Paranoia: spec.TPPParanoia(&policies[i]),
				}
				break
			}
		}
	}

	if policies, err := r.ListSecurityPolicies(ctx, namespace); err != nil {
		v.BasicAuth = BasicAuthView{Unknown: true, Message: fmt.Sprintf("could not read security policies: %v", err)}
	} else {
		v.BasicAuth = basicAuthView(ctx, r, namespace, spec.BasicAuthAttachments(proxy.Name, policies))
	}

	for _, c := range proxy.Status.Conditions {
		v.Conditions = append(v.Conditions, ConditionView{
			Type:    c.Type,
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}
	return v
}

func basicAuthView(ctx context.Context, r Reader, namespace string, attachments []spec.BasicAuthAttachment) BasicAuthView {
	var v BasicAuthView
	seen := map[string]struct{}{}
	for _, a := range attachments {
		pv := BasicAuthRulesView{
			Name:      a.Policy,
			AllRoutes: a.AllRoutes,
			Rules:     a.Rules,
			Listeners: a.Listeners,
			State:     a.State,
			Message:   a.Message,
		}
		if secret, err := r.GetSecret(ctx, namespace, a.SecretName); err != nil {
			pv.Secret = fmt.Sprintf("could not read users secret %s: %v", a.SecretName, err)
		} else {
			pv.Usernames = spec.ParseHtpasswdUsernames(secret)
		}
		for _, name := range pv.Usernames {
			if _, dup := seen[name]; !dup {
				seen[name] = struct{}{}
				v.Usernames = append(v.Usernames, name)
			}
		}
		v.Policies = append(v.Policies, pv)
	}
	v.Enabled = len(v.Policies) > 0
	return v
}

func headerViews(action string, headers []gatewayv1.HTTPHeader) []HeaderView {
	out := make([]HeaderView, 0, len(headers))
	for _, h := range headers {
		out = append(out, HeaderView{Name: string(h.Name), Value: h.Value, Action: action})
	}
	return out
}

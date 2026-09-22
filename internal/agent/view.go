// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"time"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
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

	Routes         []RouteView        `json:"routes,omitempty"`
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
}

// BackendView is one origin behind a route.
type BackendView struct {
	// Target is the origin as a person would write it: a URL, or
	// "service:port" for a network service.
	Target string `json:"target"`
	Kind   string `json:"kind"`
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
	Enabled   bool     `json:"enabled"`
	Usernames []string `json:"usernames,omitempty"`
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
		Age:               humanDuration(sinceCreation(proxy.CreationTimestamp, now)),
		ForceHTTPS:        spec.ForceHTTPS(proxy),
		HostHeader:        spec.HostHeader(proxy),
	}

	for _, route := range spec.UserRoutes(proxy) {
		rv := RouteView{Path: route.Path, Advanced: route.Advanced}
		for _, b := range route.Backends {
			rv.Backends = append(rv.Backends, BackendView{
				Target: spec.FormatBackend(b),
				Kind:   spec.BackendKind(b),
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
	v.Hostnames = hostnameProgress(proxy, domains, now)

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

	// Usernames only. The stored value is a hash and a hash is still a
	// credential's shadow; nothing here returns the Secret's data.
	if secret, err := r.GetSecret(ctx, namespace, spec.BasicAuthSecretName(proxy.Name)); err == nil {
		if names := spec.ParseHtpasswdUsernames(secret); len(names) > 0 {
			v.BasicAuth = BasicAuthView{Enabled: true, Usernames: names}
		}
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

func headerViews(action string, headers []gatewayv1.HTTPHeader) []HeaderView {
	out := make([]HeaderView, 0, len(headers))
	for _, h := range headers {
		out = append(out, HeaderView{Name: string(h.Name), Value: h.Value, Action: action})
	}
	return out
}

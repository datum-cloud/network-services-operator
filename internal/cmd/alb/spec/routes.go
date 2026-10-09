// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"strings"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

const DefaultRoutePath = "/"

type Route struct {
	Path       string
	Backends   []networkingv1alpha.HTTPProxyRuleBackend
	ForceHTTPS bool
	Advanced   bool
}

func Routes(proxy *networkingv1alpha.HTTPProxy) []Route {
	if proxy == nil {
		return nil
	}
	routes := make([]Route, 0, len(proxy.Spec.Rules))
	for _, rule := range proxy.Spec.Rules {
		path, simple := rulePath(rule)
		routes = append(routes, Route{
			Path:       path,
			Backends:   append([]networkingv1alpha.HTTPProxyRuleBackend(nil), rule.Backends...),
			ForceHTTPS: isForceHTTPSRedirectRule(rule),
			Advanced:   !simple && !isForceHTTPSRedirectRule(rule),
		})
	}
	return routes
}

func UserRoutes(proxy *networkingv1alpha.HTTPProxy) []Route {
	var out []Route
	for _, r := range Routes(proxy) {
		if !r.ForceHTTPS {
			out = append(out, r)
		}
	}
	return out
}

func FindRoute(proxy *networkingv1alpha.HTTPProxy, path string) (Route, bool) {
	idx := routeIndex(proxy, path)
	if idx < 0 {
		return Route{}, false
	}
	rule := proxy.Spec.Rules[idx]
	return Route{Path: path, Backends: append([]networkingv1alpha.HTTPProxyRuleBackend(nil), rule.Backends...)}, true
}

func NormalizePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", util.UsageErrorf("--path is required").
			WithFix("name the route by its path prefix, for example:\n       --path /api")
	}
	if !strings.HasPrefix(path, "/") {
		return "", util.UsageErrorf("path %q must start with /", path).
			WithFix("for example:\n       --path /" + path)
	}
	if strings.ContainsAny(path, " ?#") || strings.Contains(path, "//") {
		return "", util.UsageErrorf("path %q must be a plain path prefix", path).
			WithFix("use a single leading slash and no spaces, query, or fragment, for example:\n       --path /api")
	}
	if trimmed := strings.TrimRight(path, "/"); trimmed != "" {
		path = trimmed
	} else {
		path = DefaultRoutePath
	}
	return path, nil
}

func AddRoute(current *networkingv1alpha.HTTPProxy, path string, backends []BackendInput) (*networkingv1alpha.HTTPProxy, error) {
	if strings.TrimSpace(path) == "" {
		if routeIndex(current, DefaultRoutePath) >= 0 {
			return nil, util.UsageErrorf("--path is required when the load balancer already has a default route").
				WithFix("for example:\n       --path /api")
		}
		path = DefaultRoutePath
	}
	path, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}
	if len(backends) == 0 {
		return nil, errNoBackends()
	}
	if routeIndex(current, path) >= 0 {
		return nil, util.NewCLIError(util.ExitConflict, fmt.Sprintf("route %q already exists", path)).
			WithFix(fmt.Sprintf("replace its backends with:\n       datumctl alb route update %s --path %s ...", current.Name, path))
	}

	updated := current.DeepCopy()
	updated.Spec.Rules = append(updated.Spec.Rules, newRouteRule(path, toBackends(backends)))
	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func RemoveRoute(current *networkingv1alpha.HTTPProxy, path string, force bool) (*networkingv1alpha.HTTPProxy, error) {
	path, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}
	idx := routeIndex(current, path)
	if idx < 0 {
		if path == DefaultRoutePath && ForceHTTPS(current) {
			return nil, util.NewCLIError(util.ExitNotFound, "the only / rule is the Force HTTPS redirect").
				WithFix(fmt.Sprintf("turn it off with:\n       datumctl alb update %s --no-force-https", current.Name))
		}
		return nil, RouteNotFound(current, path)
	}
	if path == DefaultRoutePath && !force && len(UserRoutes(current)) > 1 {
		return nil, util.UsageErrorf("the default route cannot be removed while other routes exist").
			WithFix("remove the other routes first, or pass --force to leave the load balancer with no default route")
	}
	if len(current.Spec.Rules) == 1 {
		return nil, util.UsageErrorf("cannot remove the last route on %q", current.Name).
			WithFix(fmt.Sprintf("replace its backends with route update, or delete the load balancer:\n       datumctl alb delete %s", current.Name))
	}

	updated := current.DeepCopy()
	updated.Spec.Rules = append(updated.Spec.Rules[:idx], updated.Spec.Rules[idx+1:]...)
	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func ReplaceRouteBackends(current *networkingv1alpha.HTTPProxy, path string, backends []BackendInput) (*networkingv1alpha.HTTPProxy, error) {
	path, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}
	if len(backends) == 0 {
		return nil, errNoBackends()
	}
	idx := routeIndex(current, path)
	if idx < 0 {
		return nil, RouteNotFound(current, path)
	}

	updated := current.DeepCopy()
	updated.Spec.Rules[idx].Backends = carryBackendSettings(current.Spec.Rules[idx].Backends, toBackends(backends))
	if err := checkRouteServesTraffic(updated.Spec.Rules[idx].Backends, path); err != nil {
		return nil, err
	}
	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func AddRouteBackend(current *networkingv1alpha.HTTPProxy, path string, backend BackendInput) (*networkingv1alpha.HTTPProxy, error) {
	path, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}
	idx := routeIndex(current, path)
	if idx < 0 {
		return nil, RouteNotFound(current, path)
	}
	candidate := ToBackend(backend)
	for _, existing := range current.Spec.Rules[idx].Backends {
		if sameBackendTarget(existing, candidate) {
			return nil, util.NewCLIError(util.ExitConflict,
				fmt.Sprintf("backend %s is already on route %q", FormatBackend(candidate), path))
		}
	}
	if candidate.Weight == nil {
		candidate.Weight = suggestedWeight(current.Spec.Rules[idx].Backends)
	}

	updated := current.DeepCopy()
	updated.Spec.Rules[idx].Backends = append(updated.Spec.Rules[idx].Backends, candidate)
	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func RemoveRouteBackend(current *networkingv1alpha.HTTPProxy, path string, backend BackendInput) (*networkingv1alpha.HTTPProxy, error) {
	path, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}
	idx := routeIndex(current, path)
	if idx < 0 {
		return nil, RouteNotFound(current, path)
	}
	target := ToBackend(backend)

	updated := current.DeepCopy()
	rule := &updated.Spec.Rules[idx]
	kept := make([]networkingv1alpha.HTTPProxyRuleBackend, 0, len(rule.Backends))
	found := false
	for _, existing := range rule.Backends {
		if sameBackendTarget(existing, target) {
			found = true
			continue
		}
		kept = append(kept, existing)
	}
	if !found {
		return nil, util.NewCLIError(util.ExitNotFound,
			fmt.Sprintf("backend %s is not on route %q", FormatBackend(target), path)).
			WithFix(fmt.Sprintf("list the route's backends with:\n       datumctl alb route backend list %s --path %s", current.Name, path))
	}
	if len(kept) == 0 {
		return nil, util.UsageErrorf("cannot remove the last backend on route %q", path).
			WithFix(fmt.Sprintf("remove the route instead:\n       datumctl alb route remove %s --path %s", current.Name, path))
	}
	if err := checkRouteServesTraffic(kept, path); err != nil {
		return nil, err
	}
	rule.Backends = kept
	return updated, nil
}

// SetRouteBackendWeight changes one origin's weight and leaves the rest of
// the pool as it is.
func SetRouteBackendWeight(current *networkingv1alpha.HTTPProxy, path string, backend BackendInput, weight int32) (*networkingv1alpha.HTTPProxy, error) {
	if err := ValidateWeight(weight); err != nil {
		return nil, err
	}
	path, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}
	idx := routeIndex(current, path)
	if idx < 0 {
		return nil, RouteNotFound(current, path)
	}
	target := ToBackend(backend)

	updated := current.DeepCopy()
	rule := &updated.Spec.Rules[idx]
	found := false
	for i := range rule.Backends {
		if sameBackendTarget(rule.Backends[i], target) {
			rule.Backends[i].Weight = ptr.To(weight)
			found = true
		}
	}
	if !found {
		return nil, util.NewCLIError(util.ExitNotFound,
			fmt.Sprintf("backend %s is not on route %q", FormatBackend(target), path)).
			WithFix(fmt.Sprintf("list the route's backends with:\n       datumctl alb route backend list %s --path %s", current.Name, path))
	}
	if err := checkRouteServesTraffic(rule.Backends, path); err != nil {
		return nil, err
	}
	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// carryBackendSettings keeps what a replaced pool knew about an origin that is
// still in it: its weight, its TLS hostname and its filters, unless the new
// entry sets them itself. Replacing the origins on a route shouldn't reset a
// weight the portal or "route backend update" set.
func carryBackendSettings(previous, next []networkingv1alpha.HTTPProxyRuleBackend) []networkingv1alpha.HTTPProxyRuleBackend {
	for i := range next {
		for _, old := range previous {
			if !sameBackendTarget(old, next[i]) {
				continue
			}
			if next[i].Weight == nil {
				next[i].Weight = old.Weight
			}
			if next[i].TLS == nil && old.TLS != nil {
				next[i].TLS = old.TLS.DeepCopy()
			}
			if len(next[i].Filters) == 0 && len(old.Filters) > 0 {
				next[i].Filters = append(next[i].Filters, old.Filters...)
			}
			break
		}
	}
	return next
}

// checkRouteServesTraffic refuses a pool whose weights are all 0. The API
// accepts it, but the route then answers every request with an error.
func checkRouteServesTraffic(backends []networkingv1alpha.HTTPProxyRuleBackend, path string) error {
	for _, b := range backends {
		if BackendWeight(b) > 0 {
			return nil
		}
	}
	return util.UsageErrorf("every origin on route %q would have weight 0, so the route would serve no traffic", path).
		WithFix("give at least one origin a weight above 0, or remove the route")
}

func SetForceHTTPS(current *networkingv1alpha.HTTPProxy, enabled bool) *networkingv1alpha.HTTPProxy {
	updated := current.DeepCopy()
	if enabled == ForceHTTPS(updated) {
		return updated
	}
	if enabled {
		updated.Spec.Rules = append([]networkingv1alpha.HTTPProxyRule{forceHTTPSRule()}, updated.Spec.Rules...)
		return updated
	}
	kept := make([]networkingv1alpha.HTTPProxyRule, 0, len(updated.Spec.Rules))
	for _, rule := range updated.Spec.Rules {
		if isForceHTTPSRedirectRule(rule) {
			continue
		}
		kept = append(kept, rule)
	}
	if len(kept) == 0 {
		kept = nil
	}
	updated.Spec.Rules = kept
	return updated
}

func newRouteRule(path string, backends []networkingv1alpha.HTTPProxyRuleBackend) networkingv1alpha.HTTPProxyRule {
	rule := networkingv1alpha.HTTPProxyRule{Backends: backends}
	if path != DefaultRoutePath {
		rule.Matches = []gatewayv1.HTTPRouteMatch{{
			Path: &gatewayv1.HTTPPathMatch{
				Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
				Value: ptr.To(path),
			},
		}}
	}
	return rule
}

func rulePath(rule networkingv1alpha.HTTPProxyRule) (path string, simple bool) {
	switch len(rule.Matches) {
	case 0:
		return DefaultRoutePath, true
	case 1:
	default:
		return describeMatches(rule.Matches), false
	}

	match := rule.Matches[0]
	if len(match.Headers) > 0 || len(match.QueryParams) > 0 || match.Method != nil {
		return describeMatches(rule.Matches), false
	}
	if match.Path == nil {
		return DefaultRoutePath, true
	}
	if match.Path.Type != nil && *match.Path.Type != gatewayv1.PathMatchPathPrefix {
		return describeMatches(rule.Matches), false
	}
	if match.Path.Value == nil || *match.Path.Value == "" {
		return DefaultRoutePath, true
	}
	return *match.Path.Value, true
}

func describeMatches(matches []gatewayv1.HTTPRouteMatch) string {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		part := DefaultRoutePath
		if m.Path != nil && m.Path.Value != nil {
			part = *m.Path.Value
			if m.Path.Type != nil && *m.Path.Type != gatewayv1.PathMatchPathPrefix {
				part = strings.ToLower(string(*m.Path.Type)) + " " + part
			}
		}
		if m.Method != nil {
			part = string(*m.Method) + " " + part
		}
		if len(m.Headers) > 0 || len(m.QueryParams) > 0 {
			part += " (+conditions)"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " | ")
}

func routeIndex(proxy *networkingv1alpha.HTTPProxy, path string) int {
	if proxy == nil {
		return -1
	}
	for i, rule := range proxy.Spec.Rules {
		if isForceHTTPSRedirectRule(rule) {
			continue
		}
		if rulePath, simple := rulePath(rule); simple && rulePath == path {
			return i
		}
	}
	return -1
}

func RouteNotFound(proxy *networkingv1alpha.HTTPProxy, path string) error {
	return util.NewCLIError(util.ExitNotFound, fmt.Sprintf("route %q not found", path)).
		WithFix(fmt.Sprintf("list routes with:\n       datumctl alb route list %s", proxy.Name))
}

func errNoBackends() error {
	return util.UsageErrorf("at least one backend is required").
		WithFix("pass --endpoint URL, or --network-service NAME --port PORTNAME")
}

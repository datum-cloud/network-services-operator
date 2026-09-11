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
}

func Routes(proxy *networkingv1alpha.HTTPProxy) []Route {
	if proxy == nil {
		return nil
	}
	routes := make([]Route, 0, len(proxy.Spec.Rules))
	for _, rule := range proxy.Spec.Rules {
		routes = append(routes, Route{
			Path:       rulePath(rule),
			Backends:   rule.Backends,
			ForceHTTPS: isForceHTTPSRedirectRule(rule),
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

func NormalizePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", util.UsageErrorf("--path is required").
			WithFix("name the route by its path prefix, for example:\n       --path /api")
	}
	if !strings.HasPrefix(path, "/") {
		return "", util.UsageErrorf("path %q must start with /", path)
	}
	if strings.ContainsAny(path, " ?#") {
		return "", util.UsageErrorf("path %q must be a plain path prefix", path)
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
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
		return nil, util.UsageErrorf("at least one backend is required").
			WithFix("pass --endpoint URL, or --network-service NAME --port PORTNAME")
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
		return nil, routeNotFound(current, path)
	}
	if path == DefaultRoutePath && !force && len(UserRoutes(current)) > 1 {
		return nil, util.NewCLIError(util.ExitInvalid, "the default route cannot be removed while other routes exist").
			WithFix("remove the other routes first, or pass --force to leave the load balancer with no default route")
	}

	updated := current.DeepCopy()
	updated.Spec.Rules = append(updated.Spec.Rules[:idx], updated.Spec.Rules[idx+1:]...)
	if len(updated.Spec.Rules) == 0 {
		updated.Spec.Rules = nil
	}
	return updated, nil
}

func ReplaceRouteBackends(current *networkingv1alpha.HTTPProxy, path string, backends []BackendInput) (*networkingv1alpha.HTTPProxy, error) {
	path, err := NormalizePath(path)
	if err != nil {
		return nil, err
	}
	if len(backends) == 0 {
		return nil, util.UsageErrorf("at least one backend is required").
			WithFix("pass --endpoint URL, or --network-service NAME --port PORTNAME")
	}
	idx := routeIndex(current, path)
	if idx < 0 {
		return nil, routeNotFound(current, path)
	}

	updated := current.DeepCopy()
	updated.Spec.Rules[idx].Backends = toBackends(backends)
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
		return nil, routeNotFound(current, path)
	}
	candidate := toBackend(backend)
	for _, existing := range current.Spec.Rules[idx].Backends {
		if sameBackendTarget(existing, candidate) {
			return nil, util.NewCLIError(util.ExitConflict,
				fmt.Sprintf("backend %s is already on route %q", FormatBackend(candidate), path))
		}
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
		return nil, routeNotFound(current, path)
	}
	target := toBackend(backend)
	rule := current.Spec.Rules[idx]
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

	updated := current.DeepCopy()
	updated.Spec.Rules[idx].Backends = kept
	return updated, nil
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

func rulePath(rule networkingv1alpha.HTTPProxyRule) string {
	for _, match := range rule.Matches {
		if match.Path != nil && match.Path.Value != nil && *match.Path.Value != "" {
			return *match.Path.Value
		}
	}
	return DefaultRoutePath
}

func routeIndex(proxy *networkingv1alpha.HTTPProxy, path string) int {
	if proxy == nil {
		return -1
	}
	for i, rule := range proxy.Spec.Rules {
		if isForceHTTPSRedirectRule(rule) {
			continue
		}
		if rulePath(rule) == path {
			return i
		}
	}
	return -1
}

func routeNotFound(proxy *networkingv1alpha.HTTPProxy, path string) error {
	return util.NewCLIError(util.ExitNotFound, fmt.Sprintf("route %q not found", path)).
		WithFix(fmt.Sprintf("list routes with:\n       datumctl alb route list %s", proxy.Name))
}

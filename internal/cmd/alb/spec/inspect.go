// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func DefaultRoute(proxy *networkingv1alpha.HTTPProxy) *Route {
	rule := backendRule(proxy)
	if rule == nil {
		return nil
	}
	path, simple := rulePath(*rule)
	return &Route{Path: path, Backends: rule.Backends, Advanced: !simple}
}

func OriginSummary(proxy *networkingv1alpha.HTTPProxy) string {
	route := DefaultRoute(proxy)
	if route == nil || len(route.Backends) == 0 {
		return ""
	}
	summary := FormatBackend(route.Backends[0])
	if extra := len(route.Backends) - 1; extra > 0 {
		summary += fmt.Sprintf(" +%d", extra)
	}
	return summary
}

func ConnectorName(proxy *networkingv1alpha.HTTPProxy) string {
	for _, rule := range proxy.Spec.Rules {
		for _, backend := range rule.Backends {
			if backend.Connector != nil {
				return backend.Connector.Name
			}
		}
	}
	return ""
}

func HostHeader(proxy *networkingv1alpha.HTTPProxy) string {
	rule := backendRule(proxy)
	if rule == nil {
		return ""
	}
	for _, header := range requestHeaderModifier(rule.Filters).Set {
		if strings.EqualFold(string(header.Name), "Host") {
			return header.Value
		}
	}
	return ""
}

func ForceHTTPS(proxy *networkingv1alpha.HTTPProxy) bool {
	if proxy == nil {
		return false
	}
	for i := range proxy.Spec.Rules {
		if isForceHTTPSRedirectRule(proxy.Spec.Rules[i]) {
			return true
		}
	}
	return false
}

func Hostnames(proxy *networkingv1alpha.HTTPProxy) []string {
	if proxy == nil {
		return nil
	}
	out := make([]string, 0, len(proxy.Spec.Hostnames))
	for _, h := range proxy.Spec.Hostnames {
		out = append(out, string(h))
	}
	return out
}

func backendRule(proxy *networkingv1alpha.HTTPProxy) *networkingv1alpha.HTTPProxyRule {
	idx := backendRuleIndex(proxy)
	if idx < 0 {
		return nil
	}
	return &proxy.Spec.Rules[idx]
}

func backendRuleIndex(proxy *networkingv1alpha.HTTPProxy) int {
	if proxy == nil {
		return -1
	}
	if idx := routeIndex(proxy, DefaultRoutePath); idx >= 0 {
		return idx
	}
	for i := range proxy.Spec.Rules {
		if len(proxy.Spec.Rules[i].Backends) > 0 {
			return i
		}
	}
	return -1
}

func isForceHTTPSRedirectRule(rule networkingv1alpha.HTTPProxyRule) bool {
	if len(rule.Backends) > 0 || !matchesPlainHTTP(rule.Matches) {
		return false
	}
	for _, filter := range rule.Filters {
		if filter.Type != gatewayv1.HTTPRouteFilterRequestRedirect || filter.RequestRedirect == nil {
			continue
		}
		if filter.RequestRedirect.Scheme != nil && *filter.RequestRedirect.Scheme == "https" {
			return true
		}
	}
	return false
}

func matchesPlainHTTP(matches []gatewayv1.HTTPRouteMatch) bool {
	for _, match := range matches {
		for _, header := range match.Headers {
			if strings.EqualFold(string(header.Name), "x-forwarded-proto") && strings.EqualFold(header.Value, "http") {
				return true
			}
		}
	}
	return false
}

func requestHeaderModifier(filters []gatewayv1.HTTPRouteFilter) gatewayv1.HTTPHeaderFilter {
	for _, filter := range filters {
		if filter.Type == gatewayv1.HTTPRouteFilterRequestHeaderModifier && filter.RequestHeaderModifier != nil {
			return *filter.RequestHeaderModifier
		}
	}
	return gatewayv1.HTTPHeaderFilter{}
}

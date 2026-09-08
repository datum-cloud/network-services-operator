// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/display"
)

func DisplayName(proxy *networkingv1alpha.HTTPProxy) string {
	return display.HTTPProxyDisplayName(proxy)
}

func Endpoint(proxy *networkingv1alpha.HTTPProxy) string {
	if backend := backendOf(proxy); backend != nil {
		return backend.Endpoint
	}
	return ""
}

func TLSHostname(proxy *networkingv1alpha.HTTPProxy) string {
	backend := backendOf(proxy)
	if backend == nil || backend.TLS == nil || backend.TLS.Hostname == nil {
		return ""
	}
	return *backend.TLS.Hostname
}

func ConnectorName(proxy *networkingv1alpha.HTTPProxy) string {
	backend := backendOf(proxy)
	if backend == nil || backend.Connector == nil {
		return ""
	}
	return backend.Connector.Name
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

func backendOf(proxy *networkingv1alpha.HTTPProxy) *networkingv1alpha.HTTPProxyRuleBackend {
	rule := backendRule(proxy)
	if rule == nil || len(rule.Backends) == 0 {
		return nil
	}
	return &rule.Backends[0]
}

func backendRule(proxy *networkingv1alpha.HTTPProxy) *networkingv1alpha.HTTPProxyRule {
	if proxy == nil {
		return nil
	}
	for i := range proxy.Spec.Rules {
		if len(proxy.Spec.Rules[i].Backends) > 0 {
			return &proxy.Spec.Rules[i]
		}
	}
	return nil
}

func backendRuleIndex(proxy *networkingv1alpha.HTTPProxy) int {
	if proxy == nil {
		return -1
	}
	for i := range proxy.Spec.Rules {
		if len(proxy.Spec.Rules[i].Backends) > 0 {
			return i
		}
	}
	return -1
}

func isForceHTTPSRedirectRule(rule networkingv1alpha.HTTPProxyRule) bool {
	if len(rule.Backends) > 0 {
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

func requestHeaderModifier(filters []gatewayv1.HTTPRouteFilter) gatewayv1.HTTPHeaderFilter {
	for _, filter := range filters {
		if filter.Type == gatewayv1.HTTPRouteFilterRequestHeaderModifier && filter.RequestHeaderModifier != nil {
			return *filter.RequestHeaderModifier
		}
	}
	return gatewayv1.HTTPHeaderFilter{}
}

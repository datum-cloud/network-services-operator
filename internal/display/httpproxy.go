// SPDX-License-Identifier: AGPL-3.0-only

package display

import (
	"encoding/json"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func HTTPProxyDisplayName(proxy *networkingv1alpha.HTTPProxy) string {
	if proxy == nil {
		return ""
	}
	if name := strings.TrimSpace(proxy.Annotations[AnnotationChosenName]); name != "" {
		return name
	}
	return proxy.Name
}

func HTTPProxyDisplayValue(proxy *networkingv1alpha.HTTPProxy) string {
	if proxy == nil {
		return ""
	}
	return backendValue(proxy)
}

func EnsureHTTPProxyAnnotations(proxy, old *networkingv1alpha.HTTPProxy) bool {
	if proxy == nil {
		return false
	}
	annotations, displayChanged := stampDisplay(proxy.Annotations, HTTPProxyDisplayName(proxy), HTTPProxyDisplayValue(proxy))
	proxy.Annotations = annotations

	var diff ActivityDiff
	if old != nil {
		diff = ComputeHTTPProxyActivityDiff(old, proxy)
	}
	annotations, activityChanged := stampActivity(proxy.Annotations, diff)
	proxy.Annotations = annotations
	if len(proxy.Annotations) == 0 {
		proxy.Annotations = nil
	}
	return displayChanged || activityChanged
}

func ComputeHTTPProxyActivityDiff(oldProxy, newProxy *networkingv1alpha.HTTPProxy) ActivityDiff {
	if oldProxy == nil || newProxy == nil {
		return ActivityDiff{}
	}

	oldNames := hostnameStrings(oldProxy.Spec.Hostnames)
	newNames := hostnameStrings(newProxy.Spec.Hostnames)
	added, removed := addedRemoved(oldNames, newNames)
	hostnamesChanged := len(added) > 0 || len(removed) > 0
	backendsChanged := !equality.Semantic.DeepEqual(backendSignatures(oldProxy), backendSignatures(newProxy))
	hostHeaderChanged := hostHeaderValue(oldProxy) != hostHeaderValue(newProxy)
	forceHTTPSChanged := forceHTTPSEnabled(oldProxy) != forceHTTPSEnabled(newProxy)
	displayNameChanged := HTTPProxyDisplayName(oldProxy) != HTTPProxyDisplayName(newProxy)
	rulesChanged := !equality.Semantic.DeepEqual(ruleSignatures(oldProxy), ruleSignatures(newProxy))

	specQuiet := !hostnamesChanged && !backendsChanged && !hostHeaderChanged && !forceHTTPSChanged && !rulesChanged

	switch {
	case hostnamesChanged && !backendsChanged && !hostHeaderChanged && !forceHTTPSChanged && !rulesChanged && len(removed) == 0 && len(added) > 0:
		return ActivityDiff{
			Change: ActivityChangeAdded,
			Field:  ActivityFieldHostname,
			Name:   strings.Join(added, ", "),
			Value:  backendValue(newProxy),
		}
	case hostnamesChanged && !backendsChanged && !hostHeaderChanged && !forceHTTPSChanged && !rulesChanged && len(added) == 0 && len(removed) > 0:
		return ActivityDiff{
			Change: ActivityChangeRemoved,
			Field:  ActivityFieldHostname,
			Name:   strings.Join(removed, ", "),
			Value:  backendValue(oldProxy),
		}
	case backendsChanged && !hostnamesChanged && !hostHeaderChanged && !forceHTTPSChanged && !rulesChanged:
		return ActivityDiff{
			Change: ActivityChangeUpdated,
			Field:  ActivityFieldBackend,
			Name:   HTTPProxyDisplayName(newProxy),
			Value:  backendValue(newProxy),
		}
	case hostHeaderChanged && !hostnamesChanged && !backendsChanged && !forceHTTPSChanged && !rulesChanged:
		return hostHeaderDiff(oldProxy, newProxy)
	case forceHTTPSChanged && !hostnamesChanged && !backendsChanged && !hostHeaderChanged && !rulesChanged:
		return forceHTTPSDiff(newProxy)
	case displayNameChanged && specQuiet:
		return ActivityDiff{
			Change: ActivityChangeUpdated,
			Field:  ActivityFieldDisplayName,
			Name:   HTTPProxyDisplayName(oldProxy),
			Value:  HTTPProxyDisplayName(newProxy),
		}
	case rulesChanged && !hostnamesChanged && !backendsChanged && !hostHeaderChanged && !forceHTTPSChanged:
		return ActivityDiff{
			Change: ActivityChangeUpdated,
			Field:  ActivityFieldRule,
			Name:   HTTPProxyDisplayName(newProxy),
			Value:  backendValue(newProxy),
		}
	case specQuiet && !displayNameChanged:
		return ActivityDiff{}
	default:
		affected := append(append([]string{}, added...), removed...)
		if len(affected) == 0 {
			affected = newNames
		}
		return ActivityDiff{
			Change: ActivityChangeUpdated,
			Name:   strings.Join(dedupePreserveOrder(affected), ", "),
			Value:  backendValue(newProxy),
		}
	}
}

func hostnameStrings(hostnames []gatewayv1.Hostname) []string {
	out := make([]string, 0, len(hostnames))
	for _, h := range hostnames {
		if h != "" {
			out = append(out, string(h))
		}
	}
	return out
}

func backendValue(proxy *networkingv1alpha.HTTPProxy) string {
	var values []string
	for _, rule := range proxy.Spec.Rules {
		for _, backend := range rule.Backends {
			if v := describeBackend(backend); v != "" {
				values = append(values, v)
			}
		}
	}
	return strings.Join(values, ", ")
}

func describeBackend(backend networkingv1alpha.HTTPProxyRuleBackend) string {
	value := ""
	switch {
	case backend.Endpoint != "":
		value = backend.Endpoint
	case backend.Connector != nil && backend.Connector.Name != "":
		value = "connector " + backend.Connector.Name
	case backend.Instance != nil && backend.Instance.Name != "":
		value = "instance " + backend.Instance.Name
	}
	if backend.TLS != nil && backend.TLS.Hostname != nil && *backend.TLS.Hostname != "" {
		if value == "" {
			return "TLS " + *backend.TLS.Hostname
		}
		return value + " (TLS " + *backend.TLS.Hostname + ")"
	}
	return value
}

func backendSignatures(proxy *networkingv1alpha.HTTPProxy) []string {
	var out []string
	for _, rule := range proxy.Spec.Rules {
		for _, backend := range rule.Backends {
			out = append(out, describeBackend(backend))
		}
	}
	return out
}

func hostHeaderDiff(oldProxy, newProxy *networkingv1alpha.HTTPProxy) ActivityDiff {
	oldHeader := hostHeaderValue(oldProxy)
	newHeader := hostHeaderValue(newProxy)
	switch {
	case oldHeader == "" && newHeader != "":
		return ActivityDiff{
			Change: ActivityChangeAdded,
			Field:  ActivityFieldHostHeader,
			Name:   HTTPProxyDisplayName(newProxy),
			Value:  newHeader,
		}
	case oldHeader != "" && newHeader == "":
		return ActivityDiff{
			Change: ActivityChangeRemoved,
			Field:  ActivityFieldHostHeader,
			Name:   oldHeader,
			Value:  oldHeader,
		}
	default:
		return ActivityDiff{
			Change: ActivityChangeUpdated,
			Field:  ActivityFieldHostHeader,
			Name:   HTTPProxyDisplayName(newProxy),
			Value:  newHeader,
		}
	}
}

func hostHeaderValue(proxy *networkingv1alpha.HTTPProxy) string {
	if proxy == nil {
		return ""
	}
	for _, rule := range proxy.Spec.Rules {
		if v := hostHeaderFromFilters(rule.Filters); v != "" {
			return v
		}
		for _, backend := range rule.Backends {
			if v := hostHeaderFromFilters(backend.Filters); v != "" {
				return v
			}
		}
	}
	return ""
}

func hostHeaderFromFilters(filters []gatewayv1.HTTPRouteFilter) string {
	for _, filter := range filters {
		if filter.RequestHeaderModifier == nil {
			continue
		}
		for _, header := range filter.RequestHeaderModifier.Set {
			if strings.EqualFold(string(header.Name), "Host") {
				return header.Value
			}
		}
	}
	return ""
}

func filtersWithoutHostHeader(filters []gatewayv1.HTTPRouteFilter) []gatewayv1.HTTPRouteFilter {
	if len(filters) == 0 {
		return filters
	}
	out := make([]gatewayv1.HTTPRouteFilter, 0, len(filters))
	for _, filter := range filters {
		if filter.RequestHeaderModifier == nil {
			out = append(out, filter)
			continue
		}
		stripped := *filter.RequestHeaderModifier
		stripped.Set = headersWithoutHost(stripped.Set)
		if len(stripped.Set) == 0 && len(stripped.Add) == 0 && len(stripped.Remove) == 0 {
			continue
		}
		filter.RequestHeaderModifier = &stripped
		out = append(out, filter)
	}
	return out
}

func headersWithoutHost(headers []gatewayv1.HTTPHeader) []gatewayv1.HTTPHeader {
	out := make([]gatewayv1.HTTPHeader, 0, len(headers))
	for _, header := range headers {
		if strings.EqualFold(string(header.Name), "Host") {
			continue
		}
		out = append(out, header)
	}
	return out
}

func forceHTTPSEnabled(proxy *networkingv1alpha.HTTPProxy) bool {
	if proxy == nil {
		return false
	}
	for _, rule := range proxy.Spec.Rules {
		if isForceHTTPSRedirectRule(rule) {
			return true
		}
	}
	return false
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

func forceHTTPSDiff(newProxy *networkingv1alpha.HTTPProxy) ActivityDiff {
	if forceHTTPSEnabled(newProxy) {
		return ActivityDiff{
			Change: ActivityChangeAdded,
			Field:  ActivityFieldForceHTTPS,
			Name:   HTTPProxyDisplayName(newProxy),
			Value:  "enabled",
		}
	}
	return ActivityDiff{
		Change: ActivityChangeRemoved,
		Field:  ActivityFieldForceHTTPS,
		Name:   HTTPProxyDisplayName(newProxy),
		Value:  "disabled",
	}
}

func ruleSignatures(proxy *networkingv1alpha.HTTPProxy) []string {
	out := make([]string, 0, len(proxy.Spec.Rules))
	for _, rule := range proxy.Spec.Rules {
		if isForceHTTPSRedirectRule(rule) {
			continue
		}
		stripped := networkingv1alpha.HTTPProxyRule{
			Name:    rule.Name,
			Matches: rule.Matches,
			Filters: filtersWithoutHostHeader(rule.Filters),
		}
		b, err := json.Marshal(stripped)
		if err != nil {
			out = append(out, ruleSignatureFallback(rule))
			continue
		}
		out = append(out, string(b))
	}
	return out
}

func ruleSignatureFallback(rule networkingv1alpha.HTTPProxyRule) string {
	if rule.Name != nil {
		return string(*rule.Name)
	}
	return ""
}

func addedRemoved(oldNames, newNames []string) (added, removed []string) {
	oldSet := toSet(oldNames)
	newSet := toSet(newNames)
	for _, name := range newNames {
		if _, ok := oldSet[name]; !ok {
			added = append(added, name)
		}
	}
	for _, name := range oldNames {
		if _, ok := newSet[name]; !ok {
			removed = append(removed, name)
		}
	}
	return added, removed
}

func toSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func dedupePreserveOrder(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

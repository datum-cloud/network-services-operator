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
	rulesChanged := !equality.Semantic.DeepEqual(ruleSignatures(oldProxy), ruleSignatures(newProxy))

	switch {
	case hostnamesChanged && !backendsChanged && !rulesChanged && len(removed) == 0 && len(added) > 0:
		return ActivityDiff{
			Change: ActivityChangeAdded,
			Field:  ActivityFieldHostname,
			Name:   strings.Join(added, ", "),
			Value:  backendValue(newProxy),
		}
	case hostnamesChanged && !backendsChanged && !rulesChanged && len(added) == 0 && len(removed) > 0:
		return ActivityDiff{
			Change: ActivityChangeRemoved,
			Field:  ActivityFieldHostname,
			Name:   strings.Join(removed, ", "),
			Value:  backendValue(oldProxy),
		}
	case backendsChanged && !hostnamesChanged && !rulesChanged:
		return ActivityDiff{
			Change: ActivityChangeUpdated,
			Field:  ActivityFieldBackend,
			Name:   HTTPProxyDisplayName(newProxy),
			Value:  backendValue(newProxy),
		}
	case rulesChanged && !hostnamesChanged && !backendsChanged:
		return ActivityDiff{
			Change: ActivityChangeUpdated,
			Field:  ActivityFieldRule,
			Name:   HTTPProxyDisplayName(newProxy),
			Value:  backendValue(newProxy),
		}
	case !hostnamesChanged && !backendsChanged && !rulesChanged:
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
	if backend.Endpoint != "" {
		return backend.Endpoint
	}
	if backend.Connector != nil && backend.Connector.Name != "" {
		return "connector " + backend.Connector.Name
	}
	if backend.Instance != nil && backend.Instance.Name != "" {
		return "instance " + backend.Instance.Name
	}
	return ""
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

func ruleSignatures(proxy *networkingv1alpha.HTTPProxy) []string {
	out := make([]string, 0, len(proxy.Spec.Rules))
	for _, rule := range proxy.Spec.Rules {
		stripped := networkingv1alpha.HTTPProxyRule{
			Name:    rule.Name,
			Matches: rule.Matches,
			Filters: rule.Filters,
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

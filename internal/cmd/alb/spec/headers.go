// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

type HeaderOp string

const (
	HeaderSet HeaderOp = "set"
	HeaderAdd HeaderOp = "add"
)

func ListRequestHeaders(proxy *networkingv1alpha.HTTPProxy) (set, add []gatewayv1.HTTPHeader, remove []string) {
	rule := backendRule(proxy)
	if rule == nil {
		return nil, nil, nil
	}
	mod := requestHeaderModifier(rule.Filters)
	return mod.Set, mod.Add, mod.Remove
}

func SetRequestHeader(current *networkingv1alpha.HTTPProxy, name, value string) (*networkingv1alpha.HTTPProxy, error) {
	return mutateRequestHeader(current, HeaderSet, name, value)
}

func AddRequestHeader(current *networkingv1alpha.HTTPProxy, name, value string) (*networkingv1alpha.HTTPProxy, error) {
	return mutateRequestHeader(current, HeaderAdd, name, value)
}

func UnsetRequestHeader(current *networkingv1alpha.HTTPProxy, name string) (*networkingv1alpha.HTTPProxy, error) {
	name, err := normalizeHeaderName(name)
	if err != nil {
		return nil, err
	}
	updated := current.DeepCopy()
	idx := backendRuleIndex(updated)
	if idx < 0 {
		return nil, util.NewCLIError(util.ExitInvalid, "load balancer has no origin to attach headers to")
	}

	mod := requestHeaderModifier(updated.Spec.Rules[idx].Filters)
	mod.Set = headersWithoutName(mod.Set, name)
	mod.Add = headersWithoutName(mod.Add, name)
	mod.Remove = stringsWithoutFold(mod.Remove, name)
	updated.Spec.Rules[idx].Filters = replaceHeaderModifier(updated.Spec.Rules[idx].Filters, mod)
	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func ParseHeaderArg(arg string) (name, value string, err error) {
	arg = strings.TrimSpace(arg)
	name, value, ok := strings.Cut(arg, "=")
	if !ok || strings.TrimSpace(name) == "" {
		return "", "", util.UsageErrorf("header must be Name=value").
			WithFix("for example:\n       Host=origin.example.com")
	}
	return strings.TrimSpace(name), strings.TrimSpace(value), nil
}

func mutateRequestHeader(current *networkingv1alpha.HTTPProxy, op HeaderOp, name, value string) (*networkingv1alpha.HTTPProxy, error) {
	name, err := normalizeHeaderName(name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, util.UsageErrorf("header value is required")
	}

	updated := current.DeepCopy()
	idx := backendRuleIndex(updated)
	if idx < 0 {
		return nil, util.NewCLIError(util.ExitInvalid, "load balancer has no origin to attach headers to")
	}

	mod := requestHeaderModifier(updated.Spec.Rules[idx].Filters)
	header := gatewayv1.HTTPHeader{Name: gatewayv1.HTTPHeaderName(name), Value: value}
	switch op {
	case HeaderSet:
		mod.Set = upsertHeader(mod.Set, header)
		mod.Add = headersWithoutName(mod.Add, name)
		mod.Remove = stringsWithoutFold(mod.Remove, name)
	case HeaderAdd:
		mod.Add = upsertHeader(mod.Add, header)
		mod.Set = headersWithoutName(mod.Set, name)
		mod.Remove = stringsWithoutFold(mod.Remove, name)
	default:
		return nil, fmt.Errorf("unknown header operation %q", op)
	}
	updated.Spec.Rules[idx].Filters = replaceHeaderModifier(updated.Spec.Rules[idx].Filters, mod)
	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func normalizeHeaderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", util.UsageErrorf("header name is required")
	}
	if strings.EqualFold(name, "Host") {
		return "Host", nil
	}
	return name, nil
}

func upsertHeader(headers []gatewayv1.HTTPHeader, header gatewayv1.HTTPHeader) []gatewayv1.HTTPHeader {
	for i, existing := range headers {
		if strings.EqualFold(string(existing.Name), string(header.Name)) {
			headers[i] = header
			return headers
		}
	}
	return append(headers, header)
}

func headersWithoutName(headers []gatewayv1.HTTPHeader, name string) []gatewayv1.HTTPHeader {
	out := headers[:0]
	for _, header := range headers {
		if strings.EqualFold(string(header.Name), name) {
			continue
		}
		out = append(out, header)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringsWithoutFold(values []string, name string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.EqualFold(value, name) {
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func replaceHeaderModifier(filters []gatewayv1.HTTPRouteFilter, mod gatewayv1.HTTPHeaderFilter) []gatewayv1.HTTPRouteFilter {
	empty := len(mod.Set) == 0 && len(mod.Add) == 0 && len(mod.Remove) == 0
	out := make([]gatewayv1.HTTPRouteFilter, 0, len(filters)+1)
	replaced := false
	for _, filter := range filters {
		if filter.Type != gatewayv1.HTTPRouteFilterRequestHeaderModifier {
			out = append(out, filter)
			continue
		}
		replaced = true
		if empty {
			continue
		}
		copied := mod
		filter.RequestHeaderModifier = &copied
		out = append(out, filter)
	}
	if !replaced && !empty {
		copied := mod
		out = append(out, gatewayv1.HTTPRouteFilter{
			Type:                  gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: &copied,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

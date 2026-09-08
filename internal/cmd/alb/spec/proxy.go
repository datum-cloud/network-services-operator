// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/display"
	"go.datum.net/network-services-operator/internal/validation"
)

const (
	gatewayKind = "Gateway"
)

type CreateInput struct {
	Name        string
	DisplayName string
	Endpoint    string
	TLSHostname string
	HostHeader  string
	Hostnames   []string
	ForceHTTPS  bool
}

type UpdateInput struct {
	DisplayName *string
	Endpoint    *string
	TLSHostname *string
	ForceHTTPS  *bool
	Hostnames   *[]string
}

func BuildHTTPProxy(in CreateInput) (*networkingv1alpha.HTTPProxy, error) {
	if in.Name == "" {
		return nil, util.UsageErrorf("name is required")
	}
	if in.Endpoint == "" {
		return nil, util.UsageErrorf("endpoint is required").
			WithFix("pass --endpoint, for example:\n       --endpoint https://origin.example.com")
	}

	proxy := &networkingv1alpha.HTTPProxy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: networkingv1alpha.GroupVersion.String(),
			Kind:       "HTTPProxy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      in.Name,
			Namespace: util.ResourceNamespace,
		},
		Spec: networkingv1alpha.HTTPProxySpec{
			Hostnames: toHostnames(in.Hostnames),
			Rules:     buildRules(in.Endpoint, in.TLSHostname, in.HostHeader, in.ForceHTTPS, nil),
		},
	}
	if in.DisplayName != "" {
		proxy.Annotations = map[string]string{
			display.AnnotationChosenName: in.DisplayName,
		}
	}

	if err := validateProxy(proxy); err != nil {
		return nil, err
	}
	return proxy, nil
}

func ApplyHTTPProxyUpdate(current *networkingv1alpha.HTTPProxy, in UpdateInput) (*networkingv1alpha.HTTPProxy, error) {
	if current == nil {
		return nil, util.NewCLIError(util.ExitError, "no load balancer to update")
	}

	updated := current.DeepCopy()
	if in.DisplayName != nil {
		if updated.Annotations == nil {
			updated.Annotations = map[string]string{}
		}
		name := strings.TrimSpace(*in.DisplayName)
		if name == "" {
			delete(updated.Annotations, display.AnnotationChosenName)
		} else {
			updated.Annotations[display.AnnotationChosenName] = name
		}
		if len(updated.Annotations) == 0 {
			updated.Annotations = nil
		}
	}

	if in.Hostnames != nil {
		updated.Spec.Hostnames = toHostnames(*in.Hostnames)
	}

	rulesChanged := in.Endpoint != nil || in.TLSHostname != nil || in.ForceHTTPS != nil
	if rulesChanged {
		endpoint := Endpoint(updated)
		if in.Endpoint != nil {
			endpoint = *in.Endpoint
		}
		if endpoint == "" {
			return nil, util.UsageErrorf("endpoint is required")
		}

		tlsHostname := TLSHostname(updated)
		if in.TLSHostname != nil {
			tlsHostname = strings.TrimSpace(*in.TLSHostname)
		}

		forceHTTPS := ForceHTTPS(updated)
		if in.ForceHTTPS != nil {
			forceHTTPS = *in.ForceHTTPS
		}

		connector := connectorRef(updated)
		hostHeader := HostHeader(updated)
		extraRules := extraRules(updated)
		headerFilter := headerFilterWithoutHost(backendRule(updated))

		rules := buildRules(endpoint, tlsHostname, hostHeader, forceHTTPS, connector)
		if idx := backendRuleIndexFromRules(rules); idx >= 0 && headerFilter != nil {
			rules[idx].Filters = mergeHeaderFilters(rules[idx].Filters, headerFilter)
		}
		updated.Spec.Rules = append(rules, extraRules...)
	}

	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func AddHostname(current *networkingv1alpha.HTTPProxy, hostname string) (*networkingv1alpha.HTTPProxy, error) {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	if hostname == "" {
		return nil, util.UsageErrorf("hostname is required")
	}
	updated := current.DeepCopy()
	for _, existing := range updated.Spec.Hostnames {
		if strings.EqualFold(string(existing), hostname) {
			return nil, util.NewCLIError(util.ExitConflict, fmt.Sprintf("hostname %q is already attached", hostname))
		}
	}
	updated.Spec.Hostnames = append(updated.Spec.Hostnames, gatewayv1.Hostname(hostname))
	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func RemoveHostname(current *networkingv1alpha.HTTPProxy, hostname string) (*networkingv1alpha.HTTPProxy, error) {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	if hostname == "" {
		return nil, util.UsageErrorf("hostname is required")
	}
	updated := current.DeepCopy()
	kept := updated.Spec.Hostnames[:0]
	found := false
	for _, existing := range updated.Spec.Hostnames {
		if strings.EqualFold(string(existing), hostname) {
			found = true
			continue
		}
		kept = append(kept, existing)
	}
	if !found {
		return nil, util.NewCLIError(util.ExitNotFound, fmt.Sprintf("hostname %q is not attached", hostname))
	}
	updated.Spec.Hostnames = kept
	if len(updated.Spec.Hostnames) == 0 {
		updated.Spec.Hostnames = nil
	}
	return updated, nil
}

func buildRules(endpoint, tlsHostname, hostHeader string, forceHTTPS bool, connector *networkingv1alpha.ConnectorReference) []networkingv1alpha.HTTPProxyRule {
	rules := make([]networkingv1alpha.HTTPProxyRule, 0, 2)
	if forceHTTPS {
		rules = append(rules, forceHTTPSRule())
	}

	backend := networkingv1alpha.HTTPProxyRuleBackend{
		Endpoint:  endpoint,
		Connector: connector,
	}
	if tlsHostname != "" {
		backend.TLS = &networkingv1alpha.HTTPProxyBackendTLS{Hostname: ptr.To(tlsHostname)}
	}

	rule := networkingv1alpha.HTTPProxyRule{
		Backends: []networkingv1alpha.HTTPProxyRuleBackend{backend},
	}
	if hostHeader = strings.TrimSpace(hostHeader); hostHeader != "" {
		rule.Filters = []gatewayv1.HTTPRouteFilter{hostHeaderFilter(hostHeader)}
	}
	return append(rules, rule)
}

func forceHTTPSRule() networkingv1alpha.HTTPProxyRule {
	return networkingv1alpha.HTTPProxyRule{
		Matches: []gatewayv1.HTTPRouteMatch{{
			Path: &gatewayv1.HTTPPathMatch{
				Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
				Value: ptr.To("/"),
			},
			Headers: []gatewayv1.HTTPHeaderMatch{{
				Name:  "x-forwarded-proto",
				Type:  ptr.To(gatewayv1.HeaderMatchExact),
				Value: "http",
			}},
		}},
		Filters: []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterRequestRedirect,
			RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
				Scheme:     ptr.To("https"),
				StatusCode: ptr.To(301),
			},
		}},
	}
}

func hostHeaderFilter(value string) gatewayv1.HTTPRouteFilter {
	return gatewayv1.HTTPRouteFilter{
		Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier,
		RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{
			Set: []gatewayv1.HTTPHeader{{
				Name:  "Host",
				Value: value,
			}},
		},
	}
}

func toHostnames(hostnames []string) []gatewayv1.Hostname {
	if len(hostnames) == 0 {
		return nil
	}
	out := make([]gatewayv1.Hostname, 0, len(hostnames))
	for _, h := range hostnames {
		h = strings.TrimSpace(strings.ToLower(h))
		if h == "" {
			continue
		}
		out = append(out, gatewayv1.Hostname(h))
	}
	return out
}

func connectorRef(proxy *networkingv1alpha.HTTPProxy) *networkingv1alpha.ConnectorReference {
	backend := backendOf(proxy)
	if backend == nil || backend.Connector == nil {
		return nil
	}
	copied := *backend.Connector
	return &copied
}

func extraRules(proxy *networkingv1alpha.HTTPProxy) []networkingv1alpha.HTTPProxyRule {
	if proxy == nil {
		return nil
	}
	var extra []networkingv1alpha.HTTPProxyRule
	for _, rule := range proxy.Spec.Rules {
		if len(rule.Backends) > 0 || isForceHTTPSRedirectRule(rule) {
			continue
		}
		extra = append(extra, rule)
	}
	return extra
}

func backendRuleIndexFromRules(rules []networkingv1alpha.HTTPProxyRule) int {
	for i := range rules {
		if len(rules[i].Backends) > 0 {
			return i
		}
	}
	return -1
}

func validateProxy(proxy *networkingv1alpha.HTTPProxy) error {
	errs := validation.ValidateHTTPProxy(proxy)
	if len(errs) == 0 {
		return nil
	}
	return util.NewCLIError(util.ExitUsage, errs.ToAggregate().Error())
}

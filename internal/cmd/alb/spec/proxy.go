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

	DisplayNameAnnotation = "kubernetes.io/display-name"
	MaxDisplayNameLength  = 50
)

type CreateInput struct {
	Name        string
	DisplayName string
	Backends    []BackendInput
	HostHeader  string
	Hostnames   []string
	ForceHTTPS  bool
}

type UpdateInput struct {
	DisplayName *string
	ForceHTTPS  *bool
}

func BuildHTTPProxy(in CreateInput) (*networkingv1alpha.HTTPProxy, error) {
	if in.Name == "" {
		return nil, util.UsageErrorf("name is required")
	}
	if len(in.Backends) == 0 {
		return nil, util.UsageErrorf("at least one backend is required").
			WithFix("pass --endpoint URL, or --network-service NAME --port PORTNAME")
	}

	rules := make([]networkingv1alpha.HTTPProxyRule, 0, 2)
	if in.ForceHTTPS {
		rules = append(rules, forceHTTPSRule())
	}
	rule := newRouteRule(DefaultRoutePath, toBackends(in.Backends))
	if hostHeader := strings.TrimSpace(in.HostHeader); hostHeader != "" {
		rule.Filters = []gatewayv1.HTTPRouteFilter{hostHeaderFilter(hostHeader)}
	}
	rules = append(rules, rule)

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
			Rules:     rules,
		},
	}
	if err := setDisplayName(proxy, in.DisplayName); err != nil {
		return nil, err
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
		if err := setDisplayName(updated, *in.DisplayName); err != nil {
			return nil, err
		}
	}
	if in.ForceHTTPS != nil {
		updated = SetForceHTTPS(updated, *in.ForceHTTPS)
	}

	if err := validateProxy(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func setDisplayName(proxy *networkingv1alpha.HTTPProxy, name string) error {
	name = strings.TrimSpace(name)
	if len(name) > MaxDisplayNameLength {
		return util.UsageErrorf("display name must be %d characters or fewer", MaxDisplayNameLength)
	}
	if proxy.Annotations == nil {
		proxy.Annotations = map[string]string{}
	}
	if name == "" {
		delete(proxy.Annotations, DisplayNameAnnotation)
	} else {
		proxy.Annotations[DisplayNameAnnotation] = name
	}
	if len(proxy.Annotations) == 0 {
		proxy.Annotations = nil
	}
	return nil
}

func DisplayName(proxy *networkingv1alpha.HTTPProxy) string {
	if proxy == nil {
		return ""
	}
	if name := strings.TrimSpace(proxy.Annotations[DisplayNameAnnotation]); name != "" {
		return name
	}
	return display.HTTPProxyDisplayName(proxy)
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

func validateProxy(proxy *networkingv1alpha.HTTPProxy) error {
	errs := validation.ValidateHTTPProxy(proxy)
	if len(errs) == 0 {
		return nil
	}
	return util.NewCLIError(util.ExitUsage, errs.ToAggregate().Error())
}

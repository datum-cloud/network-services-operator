// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
	"go.datum.net/network-services-operator/internal/display"
)

type WAFInput struct {
	ProxyName   string
	PolicyName  string
	DisplayName string
	Mode        networkingv1alpha.TrafficProtectionPolicyMode
	Paranoia    int
}

func DefaultWAFMode() networkingv1alpha.TrafficProtectionPolicyMode {
	return networkingv1alpha.TrafficProtectionPolicyEnforce
}

func ParseWAFMode(s string) (networkingv1alpha.TrafficProtectionPolicyMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "enforce":
		return networkingv1alpha.TrafficProtectionPolicyEnforce, nil
	case "observe":
		return networkingv1alpha.TrafficProtectionPolicyObserve, nil
	case "disabled":
		return networkingv1alpha.TrafficProtectionPolicyDisabled, nil
	default:
		return "", util.UsageErrorf("invalid WAF mode %q — must be one of: Enforce, Observe, Disabled", s)
	}
}

func BuildTPP(in WAFInput) (*networkingv1alpha.TrafficProtectionPolicy, error) {
	if in.ProxyName == "" {
		return nil, util.UsageErrorf("load balancer name is required")
	}
	mode := in.Mode
	if mode == "" {
		mode = DefaultWAFMode()
	}
	paranoia := in.Paranoia
	if paranoia == 0 {
		paranoia = 1
	}
	if paranoia < 1 || paranoia > 4 {
		return nil, util.UsageErrorf("paranoia must be between 1 and 4")
	}
	name := in.PolicyName
	if name == "" {
		name = in.ProxyName
	}

	policy := &networkingv1alpha.TrafficProtectionPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: networkingv1alpha.GroupVersion.String(),
			Kind:       "TrafficProtectionPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: util.ResourceNamespace,
		},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: mode,
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Group: gatewayv1.GroupName,
					Kind:  gatewayv1.Kind(gatewayKind),
					Name:  gatewayv1.ObjectName(in.ProxyName),
				},
			}},
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{{
				Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
				OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{
					ParanoiaLevels: networkingv1alpha.ParanoiaLevels{
						Blocking:  paranoia,
						Detection: paranoia,
					},
				},
			}},
		},
	}
	if in.DisplayName != "" {
		policy.Annotations = map[string]string{
			display.AnnotationDisplayName: in.DisplayName,
		}
	}
	return policy, nil
}

func ApplyTPPUpdate(current *networkingv1alpha.TrafficProtectionPolicy, mode networkingv1alpha.TrafficProtectionPolicyMode, paranoia int) (*networkingv1alpha.TrafficProtectionPolicy, error) {
	updated := current.DeepCopy()
	if mode != "" {
		updated.Spec.Mode = mode
	}
	if paranoia != 0 {
		if paranoia < 1 || paranoia > 4 {
			return nil, util.UsageErrorf("paranoia must be between 1 and 4")
		}
		ensureOWASPRuleSet(updated)
		for i := range updated.Spec.RuleSets {
			if updated.Spec.RuleSets[i].Type != networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet {
				continue
			}
			updated.Spec.RuleSets[i].OWASPCoreRuleSet.ParanoiaLevels = networkingv1alpha.ParanoiaLevels{
				Blocking:  paranoia,
				Detection: paranoia,
			}
		}
	}
	return updated, nil
}

func TPPMode(policy *networkingv1alpha.TrafficProtectionPolicy) string {
	if policy == nil {
		return ""
	}
	return string(policy.Spec.Mode)
}

func TPPParanoia(policy *networkingv1alpha.TrafficProtectionPolicy) int {
	if policy == nil {
		return 0
	}
	for i := range policy.Spec.RuleSets {
		if policy.Spec.RuleSets[i].Type == networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet {
			return policy.Spec.RuleSets[i].OWASPCoreRuleSet.ParanoiaLevels.Blocking
		}
	}
	return 0
}

func TPPTargetsProxy(policy *networkingv1alpha.TrafficProtectionPolicy, proxyName string) bool {
	if policy == nil {
		return false
	}
	for _, ref := range policy.Spec.TargetRefs {
		if strings.EqualFold(string(ref.Kind), gatewayKind) && string(ref.Name) == proxyName {
			return true
		}
	}
	return false
}

func ensureOWASPRuleSet(policy *networkingv1alpha.TrafficProtectionPolicy) {
	for i := range policy.Spec.RuleSets {
		if policy.Spec.RuleSets[i].Type == networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet {
			return
		}
	}
	policy.Spec.RuleSets = append(policy.Spec.RuleSets, networkingv1alpha.TrafficProtectionPolicyRuleSet{
		Type:             networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
		OWASPCoreRuleSet: networkingv1alpha.OWASPCRS{},
	})
}

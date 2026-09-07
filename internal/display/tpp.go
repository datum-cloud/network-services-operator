// SPDX-License-Identifier: AGPL-3.0-only

package display

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TPPDisplayValue(policy *networkingv1alpha.TrafficProtectionPolicy) string {
	if policy == nil {
		return ""
	}
	return string(policy.Spec.Mode)
}

func EnsureTPPAnnotations(policy, old *networkingv1alpha.TrafficProtectionPolicy, displayName string) bool {
	if policy == nil {
		return false
	}
	if displayName == "" {
		displayName = tppFallbackName(policy)
	}
	annotations, displayChanged := stampDisplay(policy.Annotations, displayName, TPPDisplayValue(policy))
	policy.Annotations = annotations

	var diff ActivityDiff
	if old != nil {
		diff = ComputeTPPActivityDiff(old, policy)
		if diff.Name == "" {
			diff.Name = displayName
		}
	}
	annotations, activityChanged := stampActivity(policy.Annotations, diff)
	policy.Annotations = annotations
	if len(policy.Annotations) == 0 {
		policy.Annotations = nil
	}
	return displayChanged || activityChanged
}

func ComputeTPPActivityDiff(oldPolicy, newPolicy *networkingv1alpha.TrafficProtectionPolicy) ActivityDiff {
	if oldPolicy == nil || newPolicy == nil {
		return ActivityDiff{}
	}

	var fields []string
	var values []string

	if oldPolicy.Spec.Mode != newPolicy.Spec.Mode {
		fields = append(fields, ActivityFieldMode)
		values = append(values, fmt.Sprintf("%s to %s", oldPolicy.Spec.Mode, newPolicy.Spec.Mode))
	}
	if oldPolicy.Spec.SamplingPercentage != newPolicy.Spec.SamplingPercentage {
		fields = append(fields, ActivityFieldSampling)
		values = append(values, strconv.Itoa(newPolicy.Spec.SamplingPercentage)+"%")
	}
	if paranoiaChanged(oldPolicy, newPolicy) {
		fields = append(fields, ActivityFieldParanoia)
		values = append(values, paranoiaValue(newPolicy))
	}
	if !equality.Semantic.DeepEqual(exclusionsOf(oldPolicy), exclusionsOf(newPolicy)) {
		fields = append(fields, ActivityFieldExclusions)
		values = append(values, exclusionsValue(newPolicy))
	}

	if len(fields) == 0 {
		return ActivityDiff{}
	}
	if len(fields) == 1 {
		return ActivityDiff{
			Change: ActivityChangeUpdated,
			Field:  fields[0],
			Value:  values[0],
		}
	}
	return ActivityDiff{
		Change: ActivityChangeUpdated,
		Field:  strings.Join(fields, ", "),
		Value:  strings.Join(values, "; "),
	}
}

func tppFallbackName(policy *networkingv1alpha.TrafficProtectionPolicy) string {
	if len(policy.Spec.TargetRefs) == 0 {
		return policy.Name
	}
	return string(policy.Spec.TargetRefs[0].Name)
}

func exclusionsOf(policy *networkingv1alpha.TrafficProtectionPolicy) *networkingv1alpha.OWASPRuleExclusions {
	for i := range policy.Spec.RuleSets {
		if policy.Spec.RuleSets[i].Type == networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet {
			return policy.Spec.RuleSets[i].OWASPCoreRuleSet.RuleExclusions
		}
	}
	return nil
}

func exclusionsValue(policy *networkingv1alpha.TrafficProtectionPolicy) string {
	ex := exclusionsOf(policy)
	if ex == nil {
		return ""
	}
	var parts []string
	if len(ex.Tags) > 0 {
		tags := make([]string, 0, len(ex.Tags))
		for _, t := range ex.Tags {
			tags = append(tags, string(t))
		}
		parts = append(parts, strings.Join(tags, ", "))
	}
	if len(ex.IDs) > 0 {
		ids := make([]string, 0, len(ex.IDs))
		for _, id := range ex.IDs {
			ids = append(ids, strconv.Itoa(id))
		}
		parts = append(parts, strings.Join(ids, ", "))
	}
	return strings.Join(parts, ", ")
}

func paranoiaChanged(oldPolicy, newPolicy *networkingv1alpha.TrafficProtectionPolicy) bool {
	return !equality.Semantic.DeepEqual(paranoiaOf(oldPolicy), paranoiaOf(newPolicy))
}

func paranoiaOf(policy *networkingv1alpha.TrafficProtectionPolicy) networkingv1alpha.ParanoiaLevels {
	for i := range policy.Spec.RuleSets {
		if policy.Spec.RuleSets[i].Type == networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet {
			return policy.Spec.RuleSets[i].OWASPCoreRuleSet.ParanoiaLevels
		}
	}
	return networkingv1alpha.ParanoiaLevels{}
}

func paranoiaValue(policy *networkingv1alpha.TrafficProtectionPolicy) string {
	levels := paranoiaOf(policy)
	return fmt.Sprintf("blocking %d, detection %d", levels.Blocking, levels.Detection)
}

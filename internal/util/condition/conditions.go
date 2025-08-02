package conditions

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FindStatusConditionOrDefault finds and returns the condition of the type
// specified in the default condition, or returns the default condition. If a
// condition is found with a status of ConditionUnknown, the default condition
// is returned.
func FindStatusConditionOrDefault(conditions []metav1.Condition, defaultCondition *metav1.Condition) *metav1.Condition {
	if c := apimeta.FindStatusCondition(conditions, defaultCondition.Type); c != nil && c.Status != metav1.ConditionUnknown {
		return c
	}
	return defaultCondition
}

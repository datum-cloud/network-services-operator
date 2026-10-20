package conditions

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FindStatusConditionOrDefault returns a copy of the condition of the type
// specified in the default condition, or the default condition itself when
// there is none or its status is ConditionUnknown.
//
// The copy carries no LastTransitionTime, so apimeta.SetStatusCondition stamps
// the time when the status changes and keeps the stored time when it does not.
// Read the stored condition for its time.
func FindStatusConditionOrDefault(conditions []metav1.Condition, defaultCondition *metav1.Condition) *metav1.Condition {
	if c := apimeta.FindStatusCondition(conditions, defaultCondition.Type); c != nil && c.Status != metav1.ConditionUnknown {
		found := *c
		found.LastTransitionTime = metav1.Time{}
		return &found
	}
	return defaultCondition
}

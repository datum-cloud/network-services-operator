// SPDX-License-Identifier: AGPL-3.0-only

package util

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

func FindCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

func ReadyStatus(conditions []metav1.Condition) string {
	c := FindCondition(conditions, "Ready")
	if c == nil {
		return "Unknown"
	}
	return string(c.Status)
}

func ReadyReason(conditions []metav1.Condition) string {
	c := FindCondition(conditions, "Ready")
	if c == nil {
		return ""
	}
	return c.Reason
}

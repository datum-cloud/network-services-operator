// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	StatusOK       = "OK"
	StatusPending  = "Pending"
	StatusError    = "Error"
	StatusRejected = "Rejected"
	StatusUnknown  = "Unknown"
)

func ProxyStatus(proxy *networkingv1alpha.HTTPProxy) (word, detail string) {
	if proxy == nil {
		return StatusUnknown, "no load balancer data"
	}

	if accepted := apimeta.FindStatusCondition(proxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionAccepted); accepted != nil &&
		accepted.Status == metav1.ConditionFalse {
		return StatusRejected, firstNonEmpty(accepted.Message, accepted.Reason, "the load balancer was rejected")
	}

	programmed := apimeta.FindStatusCondition(proxy.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
	if programmed == nil {
		return StatusPending, "waiting for the controller"
	}

	switch programmed.Status {
	case metav1.ConditionTrue:
		return StatusOK, firstNonEmpty(programmed.Message, "programmed")
	case metav1.ConditionFalse:
		if programmed.Reason == networkingv1alpha.HTTPProxyReasonPending || programmed.Reason == "" {
			return StatusPending, firstNonEmpty(programmed.Message, "waiting for the controller")
		}
		return StatusError, firstNonEmpty(programmed.Message, programmed.Reason)
	default:
		return StatusPending, firstNonEmpty(programmed.Message, "waiting for the controller")
	}
}

func ConditionStatus(conditions []metav1.Condition, condType string) string {
	c := apimeta.FindStatusCondition(conditions, condType)
	if c == nil {
		return emDash
	}
	switch c.Status {
	case metav1.ConditionTrue:
		return "True"
	case metav1.ConditionFalse:
		return "False"
	default:
		return "Unknown"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

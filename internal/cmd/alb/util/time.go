// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
)

func RelativeAge(t metav1.Time) string {
	if IsNeverTransitioned(t) {
		return emDash
	}

	d := time.Since(t.Time)
	if d < 0 {
		d = 0
	}
	return duration.HumanDuration(d)
}

func RelativeAgeVerbose(t metav1.Time) string {
	age := RelativeAge(t)
	if age == emDash {
		return age
	}
	return age + " ago"
}

func IsNeverTransitioned(t metav1.Time) bool {
	return t.IsZero() || t.Time.UTC().Unix() == 0
}

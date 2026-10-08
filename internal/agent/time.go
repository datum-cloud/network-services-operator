// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// minPlausibleTime is the floor a timestamp must clear to count as an
// observation. Both HTTPProxy and Domain declare
// `lastTransitionTime: "1970-01-01T00:00:00Z"` as a CRD default, and
// metav1.Time.IsZero() misses it because Go's zero time is year 1. The epoch
// then renders as twenty thousand days, which is enough on its own to call a
// five-second-old load balancer stalled.
var minPlausibleTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// plausible reports whether a timestamp can be believed as a real observation.
func plausible(t metav1.Time) bool {
	return !t.IsZero() && t.After(minPlausibleTime)
}

// formatTime renders a timestamp as RFC 3339, or empty when it is unset or
// implausible. An implausible value is dropped rather than passed on: the
// consumer is a language model that will do arithmetic on whatever it is given.
func formatTime(t metav1.Time) string {
	if !plausible(t) {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// transitionTime renders a condition's lastTransitionTime, discarding it when
// it cannot be believed. Beyond the absolute floor, a condition cannot have
// transitioned before the object carrying it existed, so anything older than
// creationTimestamp is a sentinel too.
func transitionTime(c metav1.Condition, created metav1.Time) string {
	if plausible(created) && c.LastTransitionTime.Time.Before(created.Time) {
		return ""
	}
	return formatTime(c.LastTransitionTime)
}

// discardedTime describes a timestamp that was thrown away, if one was. Saying
// so is part of the evidence: a reader who sees no age should know whether the
// API set none, or set one that could not be believed.
func discardedTime(c metav1.Condition, kept string) string {
	if kept != "" || c.LastTransitionTime.IsZero() {
		return ""
	}
	return fmt.Sprintf(
		"The time recorded against this status (%s) is a placeholder Datum writes when it has "+
			"nothing real to record, so nothing was worked out from it about how long this has "+
			"been going on.",
		c.LastTransitionTime.UTC().Format(time.RFC3339))
}

// age returns how long ago an RFC 3339 timestamp was. It refuses an empty,
// unparseable, implausible or future value: downstream reads age as evidence
// that a state is stuck, and a fabricated age is worse than none.
func age(rfc3339 string, now time.Time) (time.Duration, bool) {
	if rfc3339 == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return 0, false
	}
	if !t.After(minPlausibleTime) {
		return 0, false
	}
	d := now.Sub(t)
	if d < 0 {
		return 0, false
	}
	return d, true
}

// humanDuration renders a duration coarsest-unit-first. The consumer is a
// language model reading a tool result: "5d" lands where a nanosecond count, or
// a pair of timestamps it must subtract, does not.
func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%dh%dm", int(d.Hours()), m)
		}
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		days, hours := int(d.Hours())/24, int(d.Hours())%24
		if hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
}

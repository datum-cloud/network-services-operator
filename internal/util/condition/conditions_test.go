package conditions

import (
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var t0 = metav1.NewTime(time.Date(2026, 8, 13, 9, 35, 9, 0, time.UTC))

// reconcile follows the controllers' sequence: take the condition from the helper, change it, then hand it to
// SetStatusCondition.
func reconcile(conds []metav1.Condition, to metav1.ConditionStatus, reason string) []metav1.Condition {
	cond := FindStatusConditionOrDefault(conds, &metav1.Condition{
		Type: "Verified", Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: metav1.Now(),
	})
	cond.Status = to
	cond.Reason = reason
	apimeta.SetStatusCondition(&conds, *cond)
	return conds
}

func TestTransitionMovesLastTransitionTime(t *testing.T) {
	stored := []metav1.Condition{{Type: "Verified", Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: t0}}
	conds := reconcile(stored, metav1.ConditionTrue, "Verified")
	if conds[0].LastTransitionTime.Equal(&t0) {
		t.Errorf("lastTransitionTime did not move across False to True: still %s", t0)
	}
}

func TestEveryTransitionMovesLastTransitionTime(t *testing.T) {
	conds := []metav1.Condition{{Type: "Verified", Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: t0}}
	for i, to := range []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse, metav1.ConditionTrue} {
		before := metav1.NewTime(t0.Add(time.Duration(i+1) * time.Hour))
		conds[0].LastTransitionTime = before
		conds = reconcile(conds, to, "Changed")
		if conds[0].LastTransitionTime.Equal(&before) {
			t.Errorf("transition %d to %s kept lastTransitionTime %s", i+1, to, before)
		}
	}
}

func TestSameStatusKeepsLastTransitionTime(t *testing.T) {
	stored := []metav1.Condition{{Type: "Verified", Status: metav1.ConditionTrue, Reason: "Verified", LastTransitionTime: t0}}
	conds := reconcile(stored, metav1.ConditionTrue, "StillVerified")
	if !conds[0].LastTransitionTime.Equal(&t0) {
		t.Errorf("lastTransitionTime moved without a status change: %s, want %s", conds[0].LastTransitionTime, t0)
	}
	if conds[0].Reason != "StillVerified" {
		t.Errorf("reason = %q, want the new reason", conds[0].Reason)
	}
}

func TestChangingTheResultLeavesTheStoredConditionAlone(t *testing.T) {
	stored := []metav1.Condition{{Type: "Verified", Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: t0}}
	cond := FindStatusConditionOrDefault(stored, &metav1.Condition{Type: "Verified", Status: metav1.ConditionFalse})
	cond.Status = metav1.ConditionTrue
	if stored[0].Status != metav1.ConditionFalse {
		t.Errorf("the stored condition changed before SetStatusCondition: status %s", stored[0].Status)
	}
}

func TestAbsentOrUnknownReturnsTheDefault(t *testing.T) {
	def := &metav1.Condition{Type: "Verified", Status: metav1.ConditionFalse, Reason: "Pending", LastTransitionTime: t0}
	for name, conds := range map[string][]metav1.Condition{
		"absent":  nil,
		"unknown": {{Type: "Verified", Status: metav1.ConditionUnknown, LastTransitionTime: t0}},
	} {
		if got := FindStatusConditionOrDefault(conds, def); got != def {
			t.Errorf("%s: got %+v, want the default itself", name, got)
		}
	}
}

// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSkipUpdateValidation(t *testing.T) {
	deleting := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: ptrTime(metav1.Now())},
	}
	live := &corev1.ConfigMap{}

	tests := []struct {
		name    string
		obj     *corev1.ConfigMap
		oldSpec any
		newSpec any
		want    bool
	}{
		{"spec unchanged", live, map[string]string{"a": "b"}, map[string]string{"a": "b"}, true},
		{"spec changed", live, map[string]string{"a": "b"}, map[string]string{"a": "c"}, false},
		{"deleting with spec unchanged", deleting, map[string]string{"a": "b"}, map[string]string{"a": "b"}, true},
		{"deleting with spec changed", deleting, map[string]string{"a": "b"}, map[string]string{"a": "c"}, true},
		{"both nil", live, nil, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SkipUpdateValidation(tt.obj, tt.oldSpec, tt.newSpec); got != tt.want {
				t.Fatalf("SkipUpdateValidation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func ptrTime(t metav1.Time) *metav1.Time { return &t }

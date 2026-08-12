// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SkipUpdateValidation(newObj client.Object, oldSpec, newSpec any) bool {
	if !newObj.GetDeletionTimestamp().IsZero() {
		return true
	}

	return equality.Semantic.DeepEqual(oldSpec, newSpec)
}

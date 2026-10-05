// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/types"
)

func TestStoredSecretName(t *testing.T) {
	uid := types.UID("6f1c2a4e-0b7d-4c1e-9a43-2d5f8e7b1c90")
	got := StoredSecretName(uid)
	if got != "tc-15441e1abfba82916b1ba950ace11517" {
		t.Fatalf("unexpected name %q", got)
	}
	if again := StoredSecretName(uid); again != got {
		t.Fatalf("name is not deterministic: %q then %q", got, again)
	}
	if other := StoredSecretName("7a2d3b5f-1c8e-4d2f-8b54-3e6a9f8c2d01"); other == got {
		t.Fatalf("different UIDs share the name %q", got)
	}
}

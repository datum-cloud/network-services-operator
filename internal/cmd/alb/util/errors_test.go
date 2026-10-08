// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"bytes"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestClassifyError(t *testing.T) {
	resource := schema.GroupResource{Group: "networking.datumapis.com", Resource: "httpproxies"}

	tests := []struct {
		name     string
		in       error
		wantCode int
	}{
		{name: "nil", wantCode: ExitOK},
		{name: "403", in: apierrors.NewForbidden(resource, "my-app", errors.New("no access")), wantCode: ExitForbidden},
		{name: "404", in: apierrors.NewNotFound(resource, "my-app"), wantCode: ExitNotFound},
		{name: "409", in: apierrors.NewConflict(resource, "my-app", errors.New("conflict")), wantCode: ExitConflict},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.in)
			if tc.in == nil {
				if got != nil {
					t.Fatalf("ClassifyError(nil) = %v", got)
				}
				return
			}
			if got.Code() != tc.wantCode {
				t.Fatalf("code = %d, want %d (%s)", got.Code(), tc.wantCode, got.Error())
			}
		})
	}
}

func TestRenderExit(t *testing.T) {
	var buf bytes.Buffer
	code := RenderExit(&buf, UsageErrorf("bad flag"), false)
	if code != ExitUsage {
		t.Fatalf("code = %d, want %d", code, ExitUsage)
	}
	if !bytes.Contains(buf.Bytes(), []byte("Error: bad flag")) {
		t.Fatalf("output = %q", buf.String())
	}
}

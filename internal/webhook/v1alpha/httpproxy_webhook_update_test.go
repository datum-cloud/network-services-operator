// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func httpProxyWithEndpoint(endpoint string) *networkingv1alpha.HTTPProxy {
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "wedged", Namespace: "default"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{Endpoint: endpoint},
					},
				},
			},
		},
	}
}

func TestHTTPProxyValidateUpdate(t *testing.T) {
	const (
		validEndpoint   = "https://example.com"
		invalidEndpoint = "https://example.com/get"
		otherInvalid    = "https://example.com/other"
	)

	tests := []struct {
		name      string
		mutate    func(old, updated *networkingv1alpha.HTTPProxy)
		oldEndpt  string
		newEndpt  string
		wantError bool
	}{
		{
			name:      "metadata only update on an object whose stored spec is invalid",
			oldEndpt:  invalidEndpoint,
			newEndpt:  invalidEndpoint,
			mutate:    func(_, updated *networkingv1alpha.HTTPProxy) { updated.Finalizers = nil },
			wantError: false,
		},
		{
			name:     "finalizer added to an object whose stored spec is invalid",
			oldEndpt: invalidEndpoint,
			newEndpt: invalidEndpoint,
			mutate: func(_, updated *networkingv1alpha.HTTPProxy) {
				updated.Finalizers = []string{"networking.datumapis.com/httpproxy-cleanup"}
			},
			wantError: false,
		},
		{
			name:     "deleting object whose stored spec is invalid",
			oldEndpt: invalidEndpoint,
			newEndpt: invalidEndpoint,
			mutate: func(_, updated *networkingv1alpha.HTTPProxy) {
				now := metav1.Now()
				updated.DeletionTimestamp = &now
				updated.Finalizers = nil
			},
			wantError: false,
		},
		{
			name:      "valid spec change is still admitted",
			oldEndpt:  validEndpoint,
			newEndpt:  "https://other.example.com",
			wantError: false,
		},
		{
			name:      "spec change that introduces a violation is still rejected",
			oldEndpt:  validEndpoint,
			newEndpt:  invalidEndpoint,
			wantError: true,
		},
		{
			name:      "spec change that moves an existing violation is still rejected",
			oldEndpt:  invalidEndpoint,
			newEndpt:  otherInvalid,
			wantError: true,
		},
		{
			name:      "spec change that repairs a violation is admitted",
			oldEndpt:  invalidEndpoint,
			newEndpt:  validEndpoint,
			wantError: false,
		},
	}

	validator := &HTTPProxyCustomValidator{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldProxy := httpProxyWithEndpoint(tt.oldEndpt)
			newProxy := httpProxyWithEndpoint(tt.newEndpt)
			if tt.mutate != nil {
				tt.mutate(oldProxy, newProxy)
			}

			_, err := validator.ValidateUpdate(context.Background(), oldProxy, newProxy)
			if tt.wantError && err == nil {
				t.Fatalf("ValidateUpdate() = nil, want an error")
			}
			if !tt.wantError && err != nil {
				t.Fatalf("ValidateUpdate() = %v, want no error", err)
			}
		})
	}
}

func TestHTTPProxyValidateCreateStillRejectsInvalidSpec(t *testing.T) {
	validator := &HTTPProxyCustomValidator{}

	if _, err := validator.ValidateCreate(context.Background(), httpProxyWithEndpoint("https://example.com/get")); err == nil {
		t.Fatal("ValidateCreate() = nil, want an error for an endpoint with a path component")
	}
}

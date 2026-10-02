// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"strings"
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/validation"
)

func TestHTTPProxyValidateCreateWildcardHostnames(t *testing.T) {
	validatorFor := func(enabled bool) *HTTPProxyCustomValidator {
		gatewayConfig := config.GatewayConfig{
			TargetDomain:       "datumproxy.net",
			CertificateService: config.CertificateServiceConfig{Enabled: enabled},
		}
		return &HTTPProxyCustomValidator{opts: validation.HTTPProxyValidationOptions{Hostnames: validation.CustomHostnameOptions(gatewayConfig)}}
	}

	tests := []struct {
		name      string
		enabled   bool
		hostname  string
		wantError string
	}{
		{name: "wildcard refused with the certificate service off", hostname: "*.s3.example.com", wantError: "spec.hostnames[0].hostname"},
		{name: "wildcard admitted with the certificate service on", enabled: true, hostname: "*.s3.example.com"},
		{name: "platform wildcard refused", enabled: true, hostname: "*.datumproxy.net", wantError: "managed by the platform"},
		{name: "two wildcard labels refused", enabled: true, hostname: "*.*.example.com", wantError: `single leading "*." label`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := httpProxyWithEndpoint("https://origin.example.com")
			proxy.Spec.Hostnames = []gatewayv1.Hostname{gatewayv1.Hostname(tt.hostname)}

			_, err := validatorFor(tt.enabled).ValidateCreate(context.Background(), proxy)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateCreate() = %v, want no error", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("ValidateCreate() = %v, want an error containing %q", err, tt.wantError)
			}
		})
	}
}

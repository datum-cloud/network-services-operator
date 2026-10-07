// SPDX-License-Identifier: AGPL-3.0-only

package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

func TestValidateCustomHostname(t *testing.T) {
	t.Parallel()

	wildcards := HostnameOptions{AllowWildcards: true, PlatformDomains: []string{"datumproxy.net", "prism.global.datum-dns.net"}}

	tests := []struct {
		hostname string
		opts     HostnameOptions
		wantErr  string
	}{
		{hostname: "app.example.com", opts: wildcards},
		{hostname: "*.s3.example.com", opts: wildcards},
		{hostname: "*.example.com", opts: wildcards},
		{hostname: "*.a.example.com", opts: wildcards},
		{hostname: "*.a.example.com", wantErr: "a lowercase RFC 1123 subdomain"},
		{hostname: "a.*.example.com", opts: wildcards, wantErr: "a lowercase RFC 1123 subdomain"},
		{hostname: "*", opts: wildcards, wantErr: "a lowercase RFC 1123 subdomain"},
		{hostname: "*.", opts: wildcards, wantErr: `a single leading "*." label`},
		{hostname: "*.s3.example.com", wantErr: "a lowercase RFC 1123 subdomain"},
		{hostname: "*.com", opts: wildcards, wantErr: `a single leading "*." label`},
		{hostname: "*.*.example.com", opts: wildcards, wantErr: `a single leading "*." label`},
		{hostname: "foo.*.example.com", opts: wildcards, wantErr: "a lowercase RFC 1123 subdomain"},
		{hostname: "*example.com", opts: wildcards, wantErr: "a lowercase RFC 1123 subdomain"},
		{hostname: "*.datumproxy.net", opts: wildcards, wantErr: "wildcards under datumproxy.net are managed by the platform"},
		{hostname: "*.abc.datumproxy.net", opts: wildcards, wantErr: "wildcards under datumproxy.net are managed by the platform"},
		{hostname: "*.x.prism.global.datum-dns.net", opts: wildcards, wantErr: "managed by the platform"},
		{hostname: "*.notdatumproxy.net", opts: wildcards},
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			t.Parallel()
			errs := ValidateCustomHostname(field.NewPath("spec", "hostnames").Index(0), tt.hostname, tt.opts)
			if tt.wantErr == "" {
				assert.Empty(t, errs)
				return
			}
			if assert.Len(t, errs, 1) {
				assert.Contains(t, errs[0].Error(), tt.wantErr)
			}
		})
	}
}

func TestCustomHostnameOptionsFollowTheCertificateService(t *testing.T) {
	t.Parallel()

	off := CustomHostnameOptions(config.GatewayConfig{TargetDomain: "datumproxy.net"})
	assert.False(t, off.AllowWildcards)

	on := CustomHostnameOptions(config.GatewayConfig{
		TargetDomain:        "datumproxy.net",
		LegacyTargetDomains: []string{"prism.global.datum-dns.net"},
		CertificateService:  config.CertificateServiceConfig{Enabled: true},
	})
	assert.True(t, on.AllowWildcards)
	assert.Equal(t, []string{"datumproxy.net", "prism.global.datum-dns.net"}, on.PlatformDomains)
}

func TestValidateHTTPProxyWildcardHostnames(t *testing.T) {
	t.Parallel()

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "s3"},
		Spec:       networkingv1alpha.HTTPProxySpec{Hostnames: []gatewayv1.Hostname{"*.s3.example.com"}},
	}

	assert.NotEmpty(t, ValidateHTTPProxy(proxy, HTTPProxyValidationOptions{}), "wildcards stay refused without the certificate service")
	assert.Empty(t, ValidateHTTPProxy(proxy, HTTPProxyValidationOptions{Hostnames: HostnameOptions{AllowWildcards: true}}))
}

func TestValidateGatewayWildcardListener(t *testing.T) {
	t.Parallel()

	gateway := &gatewayv1.Gateway{
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "test-gateway-class",
			Listeners: []gatewayv1.Listener{{
				Name:     "http-hostname-0",
				Protocol: gatewayv1.HTTPProtocolType,
				Port:     80,
				Hostname: ptr.To(gatewayv1.Hostname("*.s3.example.com")),
			}},
		},
	}
	opts := GatewayValidationOptions{
		ValidPortNumbers:   []int{80, 443},
		ValidProtocolTypes: map[int][]gatewayv1.ProtocolType{80: {gatewayv1.HTTPProtocolType}},
	}

	assert.NotEmpty(t, ValidateGateway(gateway, opts), "wildcard listeners stay refused without the certificate service")

	opts.Hostnames = HostnameOptions{AllowWildcards: true}
	assert.Empty(t, ValidateGateway(gateway, opts))
}

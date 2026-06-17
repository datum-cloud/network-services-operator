package validation

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestValidateExternalHost(t *testing.T) {
	fldPath := field.NewPath("host")

	tests := []struct {
		name           string
		host           string
		expectedErrors field.ErrorList
	}{
		// IP literal – blocked ranges
		{
			name: "loopback IPv4 is rejected",
			host: "127.0.0.1",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath, "", ""),
			},
		},
		{
			name: "loopback IPv6 is rejected",
			host: "::1",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath, "", ""),
			},
		},
		{
			name: "link-local unicast IPv4 (169.254.169.254) is rejected",
			host: "169.254.169.254",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath, "", ""),
			},
		},
		{
			name: "unspecified IPv4 (0.0.0.0) is rejected",
			host: "0.0.0.0",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath, "", ""),
			},
		},
		// IP literal – allowed ranges
		{
			name:           "public IPv4 (203.0.113.10) is allowed",
			host:           "203.0.113.10",
			expectedErrors: field.ErrorList{},
		},
		{
			name:           "RFC1918 IPv4 (10.0.0.1) is allowed (allowlisted separately)",
			host:           "10.0.0.1",
			expectedErrors: field.ErrorList{},
		},
		// DNS names – single-label rejected, FQDN allowed
		{
			name: "single-label name (metadata) is rejected",
			host: "metadata",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath, "", ""),
			},
		},
		{
			name: "single-label name (localhost) is rejected",
			host: "localhost",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath, "", ""),
			},
		},
		{
			name:           "valid FQDN (jwks.example.com) is allowed",
			host:           "jwks.example.com",
			expectedErrors: field.ErrorList{},
		},
		{
			name:           "valid FQDN with subdomain (auth.idp.example.com) is allowed",
			host:           "auth.idp.example.com",
			expectedErrors: field.ErrorList{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateExternalHost(fldPath, tt.host)
			delta := cmp.Diff(tt.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("validateExternalHost(%q): expected errors %v, got %v, diff: %v",
					tt.host, tt.expectedErrors, errs, delta)
			}
		})
	}
}

func TestValidateExternalHTTPSURL(t *testing.T) {
	fldPath := field.NewPath("spec", "url")

	tests := []struct {
		name           string
		raw            string
		expectedErrors field.ErrorList
	}{
		// Empty string is valid (caller decides whether field is required)
		{
			name:           "empty string is allowed",
			raw:            "",
			expectedErrors: field.ErrorList{},
		},
		// Scheme checks
		{
			// "http://x" yields two errors: NotSupported at [scheme] (http vs https)
			// and Invalid at [host] ("x" is a single-label name).
			name: "http scheme is rejected and single-label host also produces error",
			raw:  "http://x",
			expectedErrors: field.ErrorList{
				field.NotSupported(fldPath.Key("scheme"), "", []string{"https"}),
				field.Invalid(fldPath.Key("host"), "", ""),
			},
		},
		{
			name: "ftp scheme is rejected",
			raw:  "ftp://example.com/keys",
			expectedErrors: field.ErrorList{
				field.NotSupported(fldPath.Key("scheme"), "", []string{"https"}),
			},
		},
		// Userinfo checks
		{
			name: "userinfo component is rejected",
			raw:  "https://user:pass@example.com/keys",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath.Key("userinfo"), "", ""),
			},
		},
		// Host validation (SSRF)
		{
			name: "link-local IP host is rejected",
			raw:  "https://169.254.169.254/latest/meta-data",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath.Key("host"), "", ""),
			},
		},
		{
			name: "loopback IP host is rejected",
			raw:  "https://127.0.0.1/keys",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath.Key("host"), "", ""),
			},
		},
		{
			name: "single-label hostname is rejected",
			raw:  "https://metadata/keys",
			expectedErrors: field.ErrorList{
				field.Invalid(fldPath.Key("host"), "", ""),
			},
		},
		// Valid cases
		{
			name:           "valid https URL with FQDN is allowed",
			raw:            "https://accounts.example.com",
			expectedErrors: field.ErrorList{},
		},
		{
			name:           "valid https URL with path is allowed",
			raw:            "https://jwks.example.com/oidc/keys",
			expectedErrors: field.ErrorList{},
		},
		{
			name:           "valid https URL with public IP is allowed",
			raw:            "https://203.0.113.10/keys",
			expectedErrors: field.ErrorList{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateExternalHTTPSURL(fldPath, tt.raw)
			delta := cmp.Diff(tt.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("validateExternalHTTPSURL(%q): expected errors %v, got %v, diff: %v",
					tt.raw, tt.expectedErrors, errs, delta)
			}
		})
	}
}

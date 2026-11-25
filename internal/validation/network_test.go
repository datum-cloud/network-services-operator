package validation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestValidateExternalHost(t *testing.T) {
	fldPath := field.NewPath("host")

	tests := []struct {
		host    string
		refusal string
		permit  []string
	}{
		{host: "0.0.0.0", refusal: "unspecified range 0.0.0.0/8"},
		{host: "0.1.2.3", refusal: "unspecified range 0.0.0.0/8"},
		{host: "10.0.0.1", refusal: "private range 10.0.0.0/8"},
		{host: "10.212.3.23", refusal: "private range 10.0.0.0/8"},
		{host: "100.64.0.1", refusal: "carrier-grade NAT range 100.64.0.0/10"},
		{host: "100.127.255.254", refusal: "carrier-grade NAT range 100.64.0.0/10"},
		{host: "127.0.0.1", refusal: "loopback range 127.0.0.0/8"},
		{host: "127.255.255.254", refusal: "loopback range 127.0.0.0/8"},
		{host: "169.254.169.254", refusal: "link-local range 169.254.0.0/16"},
		{host: "172.16.0.1", refusal: "private range 172.16.0.0/12"},
		{host: "172.31.255.254", refusal: "private range 172.16.0.0/12"},
		{host: "192.168.1.1", refusal: "private range 192.168.0.0/16"},
		{host: "224.0.0.1", refusal: "multicast range 224.0.0.0/4"},
		{host: "255.255.255.255", refusal: "reserved range 240.0.0.0/4"},
		{host: "127.0.0.1.", refusal: "loopback range 127.0.0.0/8"},
		{host: "::", refusal: "unspecified range ::/128"},
		{host: "::1", refusal: "loopback range ::1/128"},
		{host: "::7f00:1", refusal: "IPv4-compatible range ::/96"},
		{host: "64:ff9b::7f00:1", refusal: "NAT64 range 64:ff9b::/96"},
		{host: "64:ff9b::808:808", refusal: "NAT64 range 64:ff9b::/96"},
		{host: "64:ff9b:1::1", refusal: "local-use NAT64 range 64:ff9b:1::/48"},
		{host: "fc00::1", refusal: "unique local range fc00::/7"},
		{host: "fd20:0:5c::1:0:0", refusal: "unique local range fc00::/7"},
		{host: "fe80::1", refusal: "link-local range fe80::/10"},
		{host: "fe80::1%eth0", refusal: "may not carry an IPv6 zone"},
		{host: "fec0::1", refusal: "site-local range fec0::/10"},
		{host: "ff02::1", refusal: "multicast range ff00::/8"},
		{host: "::ffff:127.0.0.1", refusal: "IPv4-mapped form of 127.0.0.1, which is in the loopback range"},
		{host: "::ffff:10.0.0.1", refusal: "IPv4-mapped form of 10.0.0.1, which is in the private range"},
		{host: "::ffff:169.254.169.254", refusal: "IPv4-mapped form of 169.254.169.254, which is in the link-local range"},
		{host: "::ffff:100.64.0.1", refusal: "IPv4-mapped form of 100.64.0.1, which is in the carrier-grade NAT range"},
		{host: "::ffff:7f00:1", refusal: "IPv4-mapped form of 127.0.0.1"},
		{host: "2130706433", refusal: "IPv4 address 127.0.0.1 in a non-canonical spelling"},
		{host: "0x7f000001", refusal: "IPv4 address 127.0.0.1 in a non-canonical spelling"},
		{host: "0177.0.0.1", refusal: "IPv4 address 127.0.0.1 in a non-canonical spelling"},
		{host: "0x7f.0.0.1", refusal: "IPv4 address 127.0.0.1 in a non-canonical spelling"},
		{host: "127.1", refusal: "IPv4 address 127.0.0.1 in a non-canonical spelling"},
		{host: "127.0.1", refusal: "IPv4 address 127.0.0.1 in a non-canonical spelling"},
		{host: "169.254.43518", refusal: "IPv4 address 169.254.169.254 in a non-canonical spelling"},
		{host: "010.0.0.1", refusal: "IPv4 address 8.0.0.1 in a non-canonical spelling"},
		{host: "8.8.8.010", refusal: "IPv4 address 8.8.8.8 in a non-canonical spelling"},
		{host: "200.300", refusal: "non-canonical spelling"},
		{host: "localhost", refusal: "at least two segments"},
		{host: "metadata", refusal: "at least two segments"},
		{host: "kubernetes", refusal: "at least two segments"},
		{host: "kubernetes.default", refusal: `ends in "default", which is not a public top-level domain`},
		{host: "envoy-admin.envoy-gateway-system", refusal: `ends in "envoy-gateway-system"`},
		{host: "kubernetes.default.svc", refusal: `is under "svc"`},
		{host: "kubernetes.default.svc.cluster.local", refusal: `is under "cluster.local"`},
		{host: "openfga.openfga.svc.cluster.local.", refusal: `is under "cluster.local"`},
		{host: "10-1-2-3.default.pod", refusal: `is under "pod"`},
		{host: "etcd.cluster.local", refusal: `is under "cluster.local"`},
		{host: "printer.local", refusal: `is under "local"`},
		{host: "app.localhost", refusal: `is under "localhost"`},
		{host: "metadata.google.internal", refusal: `is under "internal"`},
		{host: "router.home.arpa", refusal: `is under "home.arpa"`},
		{host: "KUBERNETES.DEFAULT.SVC.CLUSTER.LOCAL", refusal: "lowercase RFC 1123"},
		{host: "1.1.1.1"},
		{host: "8.8.8.8"},
		{host: "203.0.113.10"},
		{host: "170.23.227.207"},
		{host: "100.63.255.255"},
		{host: "100.128.0.1"},
		{host: "172.15.255.255"},
		{host: "172.32.0.1"},
		{host: "169.253.0.1"},
		{host: "2001:4860:4860::8888"},
		{host: "2606:4700:4700::1111"},
		{host: "::ffff:8.8.8.8"},
		{host: "jwks.example.com"},
		{host: "auth.idp.example.com"},
		{host: "accounts.google.com"},
		{host: "waf-demo.vercel.app"},
		{host: "bm-lb-test-server-a.fly.dev"},
		{host: "origin.example.co.uk"},
		{host: "api.example.engineer"},
		{host: "example.com."},
		{host: "10.example.com"},
		{host: "127-0-0-1.example.com"},
		{host: "drip-origin.default.svc.cluster.local", permit: []string{"svc.cluster.local"}},
		{host: "drip-origin.default.svc.cluster.local", permit: []string{".svc.cluster.local."}},
		{host: "kubernetes.default", permit: []string{"svc.cluster.local"}, refusal: `ends in "default"`},
		{host: "127.0.0.1", permit: []string{"svc.cluster.local"}, refusal: "loopback range"},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			errs := validateExternalHost(fldPath, tt.host, tt.permit...)
			if tt.refusal == "" {
				if len(errs) != 0 {
					t.Fatalf("validateExternalHost(%q) refused a public host: %v", tt.host, errs)
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("validateExternalHost(%q) = %v, want one refusal containing %q", tt.host, errs, tt.refusal)
			}
			if got := errs[0].Error(); !strings.Contains(got, tt.refusal) || !strings.Contains(got, tt.host) {
				t.Errorf("validateExternalHost(%q) = %q, want it to name the value and contain %q", tt.host, got, tt.refusal)
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

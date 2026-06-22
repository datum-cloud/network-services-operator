package validation

import (
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

func TestValidateBackend(t *testing.T) {
	scenarios := map[string]struct {
		backend        *envoygatewayv1alpha1.Backend
		expectedErrors field.ErrorList
	}{
		"invalid backend type": {
			backend: &envoygatewayv1alpha1.Backend{
				Spec: envoygatewayv1alpha1.BackendSpec{
					Type: ptr.To(envoygatewayv1alpha1.BackendTypeDynamicResolver),
				},
			},
			expectedErrors: field.ErrorList{
				field.NotSupported(field.NewPath("spec", "type"), "", []string{}),
			},
		},
		"invalid backend endpoints - unix": {
			backend: &envoygatewayv1alpha1.Backend{
				Spec: envoygatewayv1alpha1.BackendSpec{
					Type: ptr.To(envoygatewayv1alpha1.BackendTypeEndpoints),
					Endpoints: []envoygatewayv1alpha1.BackendEndpoint{
						{
							Unix: ptr.To(envoygatewayv1alpha1.UnixSocket{}),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "endpoints").Index(0).Child("unix"), ""),
			},
		},
		// SSRF / egress hardening – IP address checks
		"ip endpoint - link-local (169.254.169.254) is rejected": {
			backend: &envoygatewayv1alpha1.Backend{
				Spec: envoygatewayv1alpha1.BackendSpec{
					Endpoints: []envoygatewayv1alpha1.BackendEndpoint{
						{
							IP: &envoygatewayv1alpha1.IPEndpoint{
								Address: "169.254.169.254",
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "endpoints").Index(0).Child("ip", "address"), "", ""),
			},
		},
		"ip endpoint - loopback (127.0.0.1) is rejected": {
			backend: &envoygatewayv1alpha1.Backend{
				Spec: envoygatewayv1alpha1.BackendSpec{
					Endpoints: []envoygatewayv1alpha1.BackendEndpoint{
						{
							IP: &envoygatewayv1alpha1.IPEndpoint{
								Address: "127.0.0.1",
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "endpoints").Index(0).Child("ip", "address"), "", ""),
			},
		},
		"ip endpoint - public IP (203.0.113.10) is allowed": {
			backend: &envoygatewayv1alpha1.Backend{
				Spec: envoygatewayv1alpha1.BackendSpec{
					Endpoints: []envoygatewayv1alpha1.BackendEndpoint{
						{
							IP: &envoygatewayv1alpha1.IPEndpoint{
								Address: "203.0.113.10",
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
		// SSRF / egress hardening – FQDN hostname checks
		"fqdn endpoint - single-label name (metadata) is rejected": {
			backend: &envoygatewayv1alpha1.Backend{
				Spec: envoygatewayv1alpha1.BackendSpec{
					Endpoints: []envoygatewayv1alpha1.BackendEndpoint{
						{
							FQDN: &envoygatewayv1alpha1.FQDNEndpoint{
								Hostname: "metadata",
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "endpoints").Index(0).Child("fqdn", "hostname"), "", ""),
			},
		},
		"fqdn endpoint - valid FQDN (jwks.example.com) is allowed": {
			backend: &envoygatewayv1alpha1.Backend{
				Spec: envoygatewayv1alpha1.BackendSpec{
					Endpoints: []envoygatewayv1alpha1.BackendEndpoint{
						{
							FQDN: &envoygatewayv1alpha1.FQDNEndpoint{
								Hostname: "jwks.example.com",
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			errs := ValidateBackend(scenario.backend)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

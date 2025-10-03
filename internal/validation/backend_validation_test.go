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

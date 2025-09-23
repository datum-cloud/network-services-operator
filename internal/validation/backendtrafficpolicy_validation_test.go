package validation

import (
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

func TestValidateBackendTrafficPolicy(t *testing.T) {
	scenarios := map[string]struct {
		backendTrafficPolicy *envoygatewayv1alpha1.BackendTrafficPolicy
		expectedErrors       field.ErrorList
	}{
		"spec.targetRef forbidden": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					PolicyTargetReferences: envoygatewayv1alpha1.PolicyTargetReferences{
						TargetRef: &gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
							SectionName: ptr.To(gatewayv1.SectionName("test")),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "targetRef"), "Invalid"),
			},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			errs := ValidateBackendTrafficPolicy(scenario.backendTrafficPolicy)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

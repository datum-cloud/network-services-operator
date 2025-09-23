package validation

import (
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"

	"go.datum.net/network-services-operator/internal/config"
)

func TestValidateHTTPRouteFilter(t *testing.T) {
	scenarios := map[string]struct {
		httpRouteFilter *envoygatewayv1alpha1.HTTPRouteFilter
		opts            config.HTTPRouteFilterValidationOptions
		expectedErrors  field.ErrorList
	}{
		"direct response - inline body too large": {
			httpRouteFilter: &envoygatewayv1alpha1.HTTPRouteFilter{
				Spec: envoygatewayv1alpha1.HTTPRouteFilterSpec{
					DirectResponse: &envoygatewayv1alpha1.HTTPDirectResponseFilter{
						Body: &envoygatewayv1alpha1.CustomResponseBody{
							Inline: ptr.To("too large"),
						},
					},
				},
			},
			opts: config.HTTPRouteFilterValidationOptions{
				MaxInlineBodySize: 1,
			},
			expectedErrors: field.ErrorList{
				field.TooLong(field.NewPath("spec", "directResponse", "body", "inline"), 0, 0),
			},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			errs := ValidateHTTPRouteFilter(scenario.httpRouteFilter, scenario.opts)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

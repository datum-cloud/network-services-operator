package validation

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestValidateHTTPProxy(t *testing.T) {
	scenarios := map[string]struct {
		proxy          *networkingv1alpha.HTTPProxy
		expectedErrors field.ErrorList
	}{
		"invalid endpoint URL format": {
			proxy: &networkingv1alpha.HTTPProxy{
				Spec: networkingv1alpha.HTTPProxySpec{
					Rules: []networkingv1alpha.HTTPProxyRule{
						{
							Backends: []networkingv1alpha.HTTPProxyRuleBackend{
								{
									Endpoint: ":invalid",
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "rules").Index(0).Child("backends").Index(0).Child("endpoint"), "Invalid", ""),
			},
		},
		"invalid host": {
			proxy: &networkingv1alpha.HTTPProxy{
				Spec: networkingv1alpha.HTTPProxySpec{
					Rules: []networkingv1alpha.HTTPProxyRule{
						{
							// Note: The CRD currently restricts the number of backends to
							// one item, but that is enforced in the API server and not in
							// this validation logic. The list of endpoints below may be valid
							// in the future, but right now it's written like this for brevity.
							Backends: []networkingv1alpha.HTTPProxyRuleBackend{
								{
									Endpoint: "http://__invalid.com",
								},
								{
									Endpoint: "http://www",
								},
								{
									Endpoint: "http://0.0.0.0",
								},
								{
									Endpoint: "http://[::]",
								},
								{
									Endpoint: "http://127.0.0.1",
								},
								{
									Endpoint: "http://[::1]",
								},
								{
									Endpoint: "http://169.254.0.1",
								},
								{
									Endpoint: "http://[fe80::1]",
								},
								{
									Endpoint: "http://224.0.0.1",
								},
								{
									Endpoint: "http://[ff02::1]",
								},
							},
						},
					},
				},
			},
			expectedErrors: func() field.ErrorList {
				backendsField := field.NewPath("spec", "rules").Index(0).Child("backends")
				return field.ErrorList{
					field.Invalid(backendsField.Index(0).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(1).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(2).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(3).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(4).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(5).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(6).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(7).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(8).Child("endpoint").Key("host"), "Invalid", ""),
					field.Invalid(backendsField.Index(9).Child("endpoint").Key("host"), "Invalid", ""),
				}
			}(),
		},
		"unsupported endpoint": {
			proxy: &networkingv1alpha.HTTPProxy{
				Spec: networkingv1alpha.HTTPProxySpec{
					Rules: []networkingv1alpha.HTTPProxyRule{
						{
							Backends: []networkingv1alpha.HTTPProxyRuleBackend{
								{
									Endpoint: "grpcs://user:pw@www.example.com/not-valid?test=wut#blah",
								},
							},
						},
					},
				},
			},
			expectedErrors: func() field.ErrorList {
				endpointFieldPath := field.NewPath("spec", "rules").Index(0).Child("backends").Index(0).Child("endpoint")
				return field.ErrorList{
					field.NotSupported(endpointFieldPath.Key("scheme"), "Invalid", []string{}),
					field.Invalid(endpointFieldPath.Key("userinfo"), "Invalid", ""),
					field.Invalid(endpointFieldPath.Key("path"), "Invalid", ""),
					field.Invalid(endpointFieldPath.Key("query"), "Invalid", ""),
					field.Invalid(endpointFieldPath.Key("fragment"), "Invalid", ""),
				}
			}(),
		},
		"backend required when RequestRedirect filter not present": {
			proxy: &networkingv1alpha.HTTPProxy{
				Spec: networkingv1alpha.HTTPProxySpec{
					Rules: []networkingv1alpha.HTTPProxyRule{
						{},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Required(field.NewPath("spec", "rules").Index(0).Child("backends"), ""),
			},
		},
		"backend not required when RequestRedirect filter present": {
			proxy: &networkingv1alpha.HTTPProxy{
				Spec: networkingv1alpha.HTTPProxySpec{
					Rules: []networkingv1alpha.HTTPProxyRule{
						{
							Filters: []gatewayv1.HTTPRouteFilter{
								{
									Type: gatewayv1.HTTPRouteFilterRequestRedirect,
									RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
										Hostname: ptr.To(gatewayv1.PreciseHostname("example.com")),
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
		"HTTPProxy name too long": {
			proxy: &networkingv1alpha.HTTPProxy{
				ObjectMeta: metav1.ObjectMeta{
					Name: strings.Repeat("a", 64),
				},
				Spec: networkingv1alpha.HTTPProxySpec{
					Rules: []networkingv1alpha.HTTPProxyRule{
						{
							Filters: []gatewayv1.HTTPRouteFilter{
								{
									Type: gatewayv1.HTTPRouteFilterRequestRedirect,
									RequestRedirect: &gatewayv1.HTTPRequestRedirectFilter{
										Hostname: ptr.To(gatewayv1.PreciseHostname("example.com")),
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("metadata.name"), "", ""),
			},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			if scenario.proxy.Name == "" {
				scenario.proxy.Name = "test"
			}
			errs := ValidateHTTPProxy(scenario.proxy)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

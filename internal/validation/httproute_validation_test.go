package validation

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
)

func TestValidateHTTPRoute(t *testing.T) {
	scenarios := map[string]struct {
		route          *gatewayv1.HTTPRoute
		opts           config.HTTPRouteValidationOptions
		expectedErrors field.ErrorList
	}{
		"valid httproute with single rule": {
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
											Name:  "test-endpointslice",
											Port:  ptr.To(gatewayv1.PortNumber(80)),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
		"valid httproute with multiple rules": {
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test1"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
											Name:  "test-endpointslice-1",
											Port:  ptr.To(gatewayv1.PortNumber(80)),
										},
									},
								},
							},
						},
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test2"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
											Name:  "test-endpointslice-2",
											Port:  ptr.To(gatewayv1.PortNumber(80)),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
		"valid httproute with multiple matches": {
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test1"),
									},
								},
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test2"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
											Name:  "test-endpointslice",
											Port:  ptr.To(gatewayv1.PortNumber(80)),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
		"valid httproute with multiple backend refs": {
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
											Name:  "test-endpointslice-1",
											Port:  ptr.To(gatewayv1.PortNumber(80)),
										},
									},
								},
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
											Name:  "test-endpointslice-2",
											Port:  ptr.To(gatewayv1.PortNumber(80)),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{},
		},
		"invalid httproute with invalid backend ref kind": {
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("Invalid")),
											Name:  "test-backendref",
											Port:  ptr.To(gatewayv1.PortNumber(80)),
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "rules").Index(0).Child("backendRefs").Index(0).Child("kind"), "Invalid", ""),
			},
		},
		"invalid httproute with missing backend ref port": {
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
											Name:  "test-endpointslice",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Required(field.NewPath("spec", "rules").Index(0).Child("backendRefs").Index(0).Child("port"), ""),
			},
		},
		"invalid parent ref": {
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-namespace",
				},
				Spec: gatewayv1.HTTPRouteSpec{
					CommonRouteSpec: gatewayv1.CommonRouteSpec{
						ParentRefs: []gatewayv1.ParentReference{
							{
								Group:     ptr.To(gatewayv1.Group("invalid.group")),
								Kind:      ptr.To(gatewayv1.Kind("InvalidKind")),
								Namespace: ptr.To(gatewayv1.Namespace("invalid-namespace")),
								Port:      ptr.To(gatewayv1.PortNumber(80)),
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "parentRefs").Index(0).Child("group"), "invalid.group", ""),
				field.Invalid(field.NewPath("spec", "parentRefs").Index(0).Child("kind"), "InvalidKind", ""),
				field.Invalid(field.NewPath("spec", "parentRefs").Index(0).Child("namespace"), "invalid-namespace", ""),
				field.Forbidden(field.NewPath("spec", "parentRefs").Index(0).Child("port"), ""),
			},
		},
		"invalid route filters": {
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptr.To(gatewayv1.PathMatchType("PathPrefix")),
										Value: ptr.To("/test"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{
									BackendRef: gatewayv1.BackendRef{
										BackendObjectReference: gatewayv1.BackendObjectReference{
											Group: ptr.To(gatewayv1.Group("discovery.k8s.io")),
											Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
											Name:  "test-endpointslice",
											Port:  ptr.To(gatewayv1.PortNumber(80)),
										},
									},
									Filters: []gatewayv1.HTTPRouteFilter{
										{
											Type: gatewayv1.HTTPRouteFilterRequestMirror,
										},
									},
								},
							},
							Filters: []gatewayv1.HTTPRouteFilter{
								{
									Type: gatewayv1.HTTPRouteFilterRequestMirror,
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.NotSupported(field.NewPath("spec", "rules").Index(0).Child("filters").Index(0).Child("type"), "RequestMirror", []string{}),
				field.NotSupported(field.NewPath("spec", "rules").Index(0).Child("backendRefs").Index(0).Child("filters").Index(0).Child("type"), "RequestMirror", []string{}),
			},
		},
		"service backend requires opt-in": {
			route: &gatewayv1.HTTPRoute{
				Spec: gatewayv1.HTTPRouteSpec{
					Rules: []gatewayv1.HTTPRouteRule{{
						BackendRefs: []gatewayv1.HTTPBackendRef{{
							BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
								Kind: ptr.To(gatewayv1.Kind("Service")),
								Name: "svc",
							}},
						}},
					}},
				},
			},
			expectedErrors: field.ErrorList{
				field.Required(field.NewPath("spec", "rules").Index(0).Child("backendRefs").Index(0).Child("group"), "group is required"),
			},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			errs := ValidateHTTPRoute(scenario.route, scenario.opts)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

func TestValidateHTTPRouteWithGatewayBackend(t *testing.T) {
	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-namespace"},
	}

	route.Spec.Rules = []gatewayv1.HTTPRouteRule{
		{
			BackendRefs: []gatewayv1.HTTPBackendRef{
				{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Group: ptr.To(gatewayv1.Group("gateway.envoyproxy.io")),
							Kind:  ptr.To(gatewayv1.Kind("Backend")),
							Name:  "test-backend",
						},
					},
				},
			},
		},
	}

	errs := ValidateHTTPRoute(route, config.HTTPRouteValidationOptions{})
	if diff := cmp.Diff(field.ErrorList{}, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail")); diff != "" {
		t.Fatalf("expected no validation errors, diff: %s", diff)
	}
}

func TestValidateHTTPRouteWithServiceBackendOptIn(t *testing.T) {

	route := &gatewayv1.HTTPRoute{
		Spec: gatewayv1.HTTPRouteSpec{
			Rules: []gatewayv1.HTTPRouteRule{{
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
						Kind: ptr.To(gatewayv1.Kind("Service")),
						Name: "svc",
					}},
				}},
			}},
		},
	}

	if errs := ValidateHTTPRoute(route, config.HTTPRouteValidationOptions{AllowServiceBackends: true}); len(errs) > 0 {
		t.Fatalf("expected no validation errors, got %v", errs)
	}
}

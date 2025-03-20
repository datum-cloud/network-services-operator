package validation

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestValidateGateway(t *testing.T) {

	scenarios := map[string]struct {
		gateway        *gatewayv1.Gateway
		opts           GatewayValidationOptions
		expectedErrors field.ErrorList
	}{
		"missing gateway class name": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "",
				},
			},
			expectedErrors: field.ErrorList{
				field.Required(field.NewPath("spec", "gatewayClassName"), "gatewayClassName is required"),
			},
		},
		"hostname not permitted": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: ptr.To(gatewayv1.Hostname("example.com")),
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				PermitListenerHostnames: false,
				ValidPortNumbers:        []int{80, 443},
				ValidProtocolTypes:      []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("hostname"), "example.com", "hostnames are not permitted"),
			},
		},
		"invalid port number": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     8080,
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.NotSupported(field.NewPath("spec", "listeners").Index(0).Child("port"), 8080, []string{"80", "443"}),
			},
		},
		"invalid protocol type": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "tcp",
							Protocol: gatewayv1.TCPProtocolType,
							Port:     80,
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.NotSupported(field.NewPath("spec", "listeners").Index(0).Child("protocol"), gatewayv1.TCPProtocolType, []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType}),
			},
		},
		"invalid tls mode": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "https",
							Protocol: gatewayv1.HTTPSProtocolType,
							Port:     443,
							TLS: &gatewayv1.GatewayTLSConfig{
								Mode: ptr.To(gatewayv1.TLSModePassthrough),
							},
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("tls", "mode"), gatewayv1.TLSModePassthrough, "mode must be set to Terminate"),
			},
		},
		"certificate refs not permitted": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "https",
							Protocol: gatewayv1.HTTPSProtocolType,
							Port:     443,
							TLS: &gatewayv1.GatewayTLSConfig{
								Mode: ptr.To(gatewayv1.TLSModeTerminate),
								CertificateRefs: []gatewayv1.SecretObjectReference{
									{
										Name: "test-cert",
									},
								},
							},
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:      []int{80, 443},
				ValidProtocolTypes:    []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
				PermitCertificateRefs: false,
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "listeners").Index(0).Child("tls", "certificateRefs"), 1, 0),
			},
		},
		"routes from all namespaces not permitted": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							AllowedRoutes: &gatewayv1.AllowedRoutes{
								Namespaces: &gatewayv1.RouteNamespaces{
									From: ptr.To(gatewayv1.NamespacesFromAll),
								},
							},
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				RoutesFromSameNamespaceOnly: true,
				ValidPortNumbers:            []int{80, 443},
				ValidProtocolTypes:          []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("allowedRoutes", "namespaces", "from"), gatewayv1.NamespacesFromAll, "allowedRoutes.namespaces.from must be set to NamespacesFromAll"),
			},
		},
		"kinds not permitted": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							AllowedRoutes: &gatewayv1.AllowedRoutes{
								Kinds: []gatewayv1.RouteGroupKind{
									{
										Kind: "HTTPRoute",
									},
								},
							},
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "listeners").Index(0).Child("allowedRoutes", "kinds"), 1, 0),
			},
		},
		"addresses not permitted": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
						},
					},
					Addresses: []gatewayv1.GatewayAddress{
						{
							Type:  ptr.To(gatewayv1.IPAddressType),
							Value: "192.168.1.1",
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "addresses"), 1, 0),
			},
		},
		"infrastructure not permitted": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
						},
					},
					Infrastructure: &gatewayv1.GatewayInfrastructure{
						Labels: map[gatewayv1.LabelKey]gatewayv1.LabelValue{
							"key": "value",
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "infrastructure"), "infrastructure is not permitted"),
			},
		},
		"backend tls not permitted": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
						},
					},
					BackendTLS: &gatewayv1.GatewayBackendTLS{
						ClientCertificateRef: &gatewayv1.SecretObjectReference{
							Name: "test-cert",
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: []gatewayv1.ProtocolType{gatewayv1.HTTPProtocolType, gatewayv1.HTTPSProtocolType},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "backendTLS"), "backendTLS is not permitted"),
			},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			errs := ValidateGateway(scenario.gateway, scenario.opts)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

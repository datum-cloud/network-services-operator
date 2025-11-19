package validation

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

func TestValidateGateway(t *testing.T) {

	defaultValidProtocolTypes := map[int][]gatewayv1.ProtocolType{
		gatewayutil.DefaultHTTPPort:  {gatewayv1.HTTPProtocolType},
		gatewayutil.DefaultHTTPSPort: {gatewayv1.HTTPSProtocolType},
	}

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:            "test-suite",
			DownstreamHostnameAccountingNamespace: "default",
			TargetDomain:                          "test-suite.com",
		},
	}

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
				ValidProtocolTypes: defaultValidProtocolTypes,
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
				ValidProtocolTypes: defaultValidProtocolTypes,
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
				ValidProtocolTypes: defaultValidProtocolTypes,
			},
			expectedErrors: field.ErrorList{
				field.Required(field.NewPath("spec", "listeners").Index(0).Child("tls", "options"), ""),
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
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
			},
			expectedErrors: field.ErrorList{
				field.Required(field.NewPath("spec", "listeners").Index(0).Child("tls", "options"), ""),
				field.Forbidden(field.NewPath("spec", "listeners").Index(0).Child("tls", "certificateRefs"), ""),
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
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
			},
			expectedErrors: field.ErrorList{
				field.NotSupported(field.NewPath("spec", "listeners").Index(0).Child("allowedRoutes", "namespaces", "from"), gatewayv1.NamespacesFromAll, []gatewayv1.FromNamespaces{gatewayv1.NamespacesFromSame}),
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
				ValidProtocolTypes: defaultValidProtocolTypes,
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
					Addresses: []gatewayv1.GatewaySpecAddress{
						{
							Type:  ptr.To(gatewayv1.IPAddressType),
							Value: "192.168.1.1",
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
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
				ValidProtocolTypes: defaultValidProtocolTypes,
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
				ValidProtocolTypes: defaultValidProtocolTypes,
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "backendTLS"), "backendTLS is not permitted"),
			},
		},
		"invalid tls settings": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "https",
							Protocol: gatewayv1.HTTPSProtocolType,
							Port:     443,
							TLS: &gatewayv1.GatewayTLSConfig{
								Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
									"test-option": "test-value",
								},
								FrontendValidation: &gatewayv1.FrontendTLSValidation{},
							},
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "listeners").Index(0).Child("tls", "frontendValidation"), ""),
				field.Forbidden(field.NewPath("spec", "listeners").Index(0).Child("tls", "options").Key("test-option"), ""),
			},
		},
		"tls option value not permitted": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "https",
							Protocol: gatewayv1.HTTPSProtocolType,
							Port:     443,
							TLS: &gatewayv1.GatewayTLSConfig{
								Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
									"test-option": "invalid-value",
								},
							},
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				PermittedTLSOptions: map[string][]string{
					"test-option": {"test-value"},
				},
			},
			expectedErrors: field.ErrorList{
				field.NotSupported(field.NewPath("spec", "listeners").Index(0).Child("tls", "options").Key("test-option"), "invalid-value", []string{}),
			},
		},
		"valid tls options": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							TLS: &gatewayv1.GatewayTLSConfig{
								Options: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
									"test-option": "valid-value",
								},
							},
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				PermittedTLSOptions: map[string][]string{
					"test-option": {"valid-value"},
				},
			},
			expectedErrors: field.ErrorList{},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			scenario.opts.GatewayDNSAddressFunc = testConfig.Gateway.GatewayDNSAddress
			scenario.gateway.UID = uuid.NewUUID()

			for i, l := range scenario.gateway.Spec.Listeners {
				if l.Hostname == nil {
					scenario.gateway.Spec.Listeners[i].Hostname = ptr.To(gatewayv1.Hostname(testConfig.Gateway.GatewayDNSAddress(scenario.gateway)))
				}
			}

			errs := ValidateGateway(scenario.gateway, scenario.opts)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

func TestValidateListenersAllowsExistingHostnameInStatus(t *testing.T) {
	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:            "test-suite",
			DownstreamHostnameAccountingNamespace: "default",
			TargetDomain:                          "test-suite.com",
		},
	}

	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			UID: uuid.NewUUID(),
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "test-gateway-class",
			Listeners: []gatewayv1.Listener{
				{
					Name:     gatewayutil.DefaultHTTPListenerName,
					Protocol: gatewayv1.HTTPProtocolType,
					Port:     gatewayutil.DefaultHTTPPort,
				},
			},
		},
	}

	// "old" format includes dashes from the UID
	oldStyleHostname := fmt.Sprintf("%s.%s", string(gateway.UID), testConfig.Gateway.TargetDomain)

	gateway.Spec.Listeners[0].Hostname = ptr.To(gatewayv1.Hostname(oldStyleHostname))

	gateway.Status.Addresses = []gatewayv1.GatewayStatusAddress{
		{
			Type:  ptr.To(gatewayv1.HostnameAddressType),
			Value: oldStyleHostname,
		},
	}

	opts := GatewayValidationOptions{
		GatewayDNSAddressFunc: testConfig.Gateway.GatewayDNSAddress,
		ValidPortNumbers:      []int{gatewayutil.DefaultHTTPPort},
		ValidProtocolTypes: map[int][]gatewayv1.ProtocolType{
			gatewayutil.DefaultHTTPPort:  {gatewayv1.HTTPProtocolType},
			gatewayutil.DefaultHTTPSPort: {gatewayv1.HTTPSProtocolType},
		},
	}

	errs := validateListeners(gateway, field.NewPath("spec", "listeners"), opts)
	assert.Len(t, errs, 0, "expected validateListeners to permit a hostname on a default listener that matches an existing status address")

}

package validation

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
)

func TestValidateGateway(t *testing.T) {

	defaultValidProtocolTypes := map[int][]gatewayv1.ProtocolType{
		HTTPPort:  {gatewayv1.HTTPProtocolType},
		HTTPSPort: {gatewayv1.HTTPSProtocolType},
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
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("hostname"), "example.com", "hostnames are not permitted"),
			},
		},
		"custom hostname: cluster not in allow list": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: ptr.To(gatewayv1.Hostname("custom.example.com")),
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "other-cluster",
						Suffixes:    []string{"example.com"},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("hostname"), gatewayv1.Hostname("custom.example.com"), "hostnames are not permitted"),
			},
		},
		"custom hostname: empty suffix list for cluster": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: ptr.To(gatewayv1.Hostname("custom.example.com")),
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "cluster-a",
						Suffixes:    []string{},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("hostname"), "custom.example.com", "hostname does not match any allowed suffixes: []"),
			},
		},
		"custom hostname: valid subdomain": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: ptr.To(gatewayv1.Hostname("foo.example.com")),
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "cluster-a",
						Suffixes:    []string{"example.com", "another.org"},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{},
		},
		"custom hostname: exact match": {
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
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "cluster-a",
						Suffixes:    []string{"example.com", "another.org"},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{},
		},
		"custom hostname: invalid, not a subdomain": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: ptr.To(gatewayv1.Hostname("foo.bar.com")),
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "cluster-a",
						Suffixes:    []string{"example.com"},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("hostname"), "foo.bar.com", "hostname does not match any allowed suffixes: [example.com]"),
			},
		},
		"custom hostname: invalid, superdomain": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: ptr.To(gatewayv1.Hostname("com")),
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "cluster-a",
						Suffixes:    []string{"example.com"},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("hostname"), "com", "hostname does not match any allowed suffixes: [example.com]"),
			},
		},
		"custom hostname: valid, matches second suffix in list": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: ptr.To(gatewayv1.Hostname("web.another.org")),
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "cluster-a",
						Suffixes:    []string{"example.com", "another.org"},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{},
		},
		"custom hostname: case-sensitive mismatch (current behavior)": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: ptr.To(gatewayv1.Hostname("foo.EXAMPLE.COM")),
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "cluster-a",
						Suffixes:    []string{"example.com"},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "listeners").Index(0).Child("hostname"), "foo.EXAMPLE.COM", "hostname does not match any allowed suffixes: [example.com]"),
			},
		},
		"custom hostname: nil hostname (should not error on hostname validation)": {
			gateway: &gatewayv1.Gateway{
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
							Hostname: nil, // Explicitly nil
						},
					},
				},
			},
			opts: GatewayValidationOptions{
				ValidPortNumbers:   []int{80, 443},
				ValidProtocolTypes: defaultValidProtocolTypes,
				CustomHostnameAllowList: []config.CustomHostnameAllowListEntry{
					{
						ClusterName: "cluster-a",
						Suffixes:    []string{"example.com"},
					},
				},
				ClusterName: "cluster-a",
			},
			expectedErrors: field.ErrorList{},
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
			errs := ValidateGateway(scenario.gateway, scenario.opts)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

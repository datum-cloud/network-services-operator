package validation

import (
	"strings"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

func TestTenantUpstreamsRefuseInternalHosts(t *testing.T) {
	refused := []string{
		"10.212.3.23",
		"172.16.0.1",
		"192.168.1.1",
		"100.64.0.1",
		"0.0.0.0",
		"127-0-0-1",
		"fd00::1",
		"fe80::1",
		"::ffff:10.0.0.1",
		"64:ff9b::a9fe:a9fe",
		"2130706433",
		"127.1",
		"kubernetes.default",
		"kubernetes.default.svc",
		"openfga.openfga.svc.cluster.local",
	}
	allowed := []string{
		"203.0.113.10",
		"2001:db8::10",
		"origin.example.com",
	}

	opts := &config.NetworkServicesOperator{}
	config.SetObjectDefaults_NetworkServicesOperator(opts)
	securityPolicyOpts := opts.Gateway.ExtensionAPIValidationOptions.SecurityPolicies

	callers := map[string]func(host string) field.ErrorList{
		"backend ip": func(host string) field.ErrorList {
			return ValidateBackend(&envoygatewayv1alpha1.Backend{Spec: envoygatewayv1alpha1.BackendSpec{
				Endpoints: []envoygatewayv1alpha1.BackendEndpoint{{IP: &envoygatewayv1alpha1.IPEndpoint{Address: host, Port: 443}}},
			}})
		},
		"backend fqdn": func(host string) field.ErrorList {
			return ValidateBackend(&envoygatewayv1alpha1.Backend{Spec: envoygatewayv1alpha1.BackendSpec{
				Endpoints: []envoygatewayv1alpha1.BackendEndpoint{{FQDN: &envoygatewayv1alpha1.FQDNEndpoint{Hostname: host, Port: 443}}},
			}})
		},
		"httpproxy endpoint": func(host string) field.ErrorList {
			return ValidateHTTPProxy(&networkingv1alpha.HTTPProxy{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: networkingv1alpha.HTTPProxySpec{
				Rules: []networkingv1alpha.HTTPProxyRule{{Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "http://" + urlHost(host)}}}},
			}}, HTTPProxyValidationOptions{})
		},
		"oidc issuer": func(host string) field.ErrorList {
			return ValidateSecurityPolicy(&envoygatewayv1alpha1.SecurityPolicy{Spec: envoygatewayv1alpha1.SecurityPolicySpec{
				OIDC: &envoygatewayv1alpha1.OIDC{Provider: envoygatewayv1alpha1.OIDCProvider{Issuer: "https://" + urlHost(host)}},
			}}, securityPolicyOpts)
		},
		"remote jwks": func(host string) field.ErrorList {
			return ValidateSecurityPolicy(&envoygatewayv1alpha1.SecurityPolicy{Spec: envoygatewayv1alpha1.SecurityPolicySpec{
				JWT: &envoygatewayv1alpha1.JWT{Providers: []envoygatewayv1alpha1.JWTProvider{{
					Name:       "p",
					RemoteJWKS: &envoygatewayv1alpha1.RemoteJWKS{URI: "https://" + urlHost(host) + "/keys"},
				}}},
			}}, securityPolicyOpts)
		},
	}

	for caller, validate := range callers {
		for _, host := range refused {
			t.Run(caller+"/refuses "+host, func(t *testing.T) {
				errs := validate(host)
				if len(errs) == 0 {
					t.Fatalf("%s accepted internal host %q", caller, host)
				}
				if !strings.Contains(errs.ToAggregate().Error(), host) {
					t.Errorf("%s refusal does not name %q: %v", caller, host, errs)
				}
			})
		}
		for _, host := range allowed {
			t.Run(caller+"/allows "+host, func(t *testing.T) {
				if caller == "backend fqdn" && !strings.Contains(host, "example") {
					t.Skip("address is not a hostname")
				}
				if errs := validate(host); len(errs) != 0 {
					t.Errorf("%s refused public host %q: %v", caller, host, errs)
				}
			})
		}
	}
}

func TestHTTPProxyEndpointInternalHostExceptions(t *testing.T) {
	endpointHost := field.NewPath("spec", "rules").Index(0).Child("backends").Index(0).Child("endpoint").Key("host")

	tests := []struct {
		name      string
		endpoint  string
		connector bool
		permitted []string
		wantError bool
	}{
		{name: "connector reaches loopback on the tenant side", endpoint: "http://127.0.0.1:8080", connector: true},
		{name: "connector reaches localhost on the tenant side", endpoint: "http://localhost:8080", connector: true},
		{name: "connector reaches a private address on the tenant side", endpoint: "http://10.0.0.5:8080", connector: true},
		{name: "connector still refuses link-local", endpoint: "http://169.254.169.254", connector: true, wantError: true},
		{name: "permitted suffix admits an in-cluster origin", endpoint: "http://drip-origin.default.svc.cluster.local:8080", permitted: []string{"svc.cluster.local"}},
		{name: "permitted suffix does not admit a short name", endpoint: "http://kubernetes.default", permitted: []string{"svc.cluster.local"}, wantError: true},
		{name: "permitted suffix does not admit a private address", endpoint: "http://10.0.0.5", permitted: []string{"svc.cluster.local"}, wantError: true},
		{name: "in-cluster origin refused without a permitted suffix", endpoint: "http://drip-origin.default.svc.cluster.local:8080", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := networkingv1alpha.HTTPProxyRuleBackend{Endpoint: tt.endpoint}
			if tt.connector {
				backend.Connector = &networkingv1alpha.ConnectorReference{Name: "connector-1"}
			}
			errs := ValidateHTTPProxy(&networkingv1alpha.HTTPProxy{ObjectMeta: metav1.ObjectMeta{Name: "test"}, Spec: networkingv1alpha.HTTPProxySpec{
				Rules: []networkingv1alpha.HTTPProxyRule{{Backends: []networkingv1alpha.HTTPProxyRuleBackend{backend}}},
			}}, HTTPProxyValidationOptions{PermittedInternalHostSuffixes: tt.permitted})

			if !tt.wantError {
				if len(errs) != 0 {
					t.Fatalf("expected %q to be accepted, got %v", tt.endpoint, errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Field != endpointHost.String() {
				t.Fatalf("expected one refusal at %s for %q, got %v", endpointHost, tt.endpoint, errs)
			}
		})
	}
}

func urlHost(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

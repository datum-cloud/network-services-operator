package validation

import (
	"testing"
	"time"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"go.datum.net/network-services-operator/internal/config"
)

func TestValidateSecurityPolicy(t *testing.T) {
	scenarios := map[string]struct {
		securityPolicy *envoygatewayv1alpha1.SecurityPolicy
		opts           func(*config.SecurityPolicyValidationOptions)
		expectedErrors field.ErrorList
	}{
		"spec.targetRef forbidden": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
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
		"apiKeyAuth.credentialRefs namespace set": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					APIKeyAuth: &envoygatewayv1alpha1.APIKeyAuth{
						CredentialRefs: []gatewayv1.SecretObjectReference{
							{
								Name:      "test-secret",
								Namespace: ptr.To(gatewayv1.Namespace("other-namespace")),
							},
							{
								Name: "test-secret",
							},
						},
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.APIKeyAuth.MaxCredentialRefs = 1
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "apiKeyAuth", "credentialRefs"), 2, 1),
				field.Forbidden(field.NewPath("spec", "apiKeyAuth", "credentialRefs").Index(0).Child("namespace"), "must not be set"),
			},
		},
		"apiKeyAuth.extractFrom too many": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					APIKeyAuth: &envoygatewayv1alpha1.APIKeyAuth{
						ExtractFrom: []*envoygatewayv1alpha1.ExtractFrom{
							{},
							{},
						},
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.APIKeyAuth.MaxExtractFrom = 1
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "apiKeyAuth", "extractFrom"), 2, 1),
			},
		},
		"apiKeyAuth.extractFrom fields too long": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					APIKeyAuth: &envoygatewayv1alpha1.APIKeyAuth{
						ExtractFrom: []*envoygatewayv1alpha1.ExtractFrom{
							{
								Headers: []string{"header1", "header2", "header3"},
								Params:  []string{"param1", "param2", "param3"},
								Cookies: []string{"cookie1", "cookie2", "cookie3"},
							},
						},
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.APIKeyAuth.MaxExtractFrom = 1
				cfg.APIKeyAuth.MaxExtractFromFieldLength = 1
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "apiKeyAuth", "extractFrom").Index(0).Child("headers"), 0, 0),
				field.TooMany(field.NewPath("spec", "apiKeyAuth", "extractFrom").Index(0).Child("params"), 0, 0),
				field.TooMany(field.NewPath("spec", "apiKeyAuth", "extractFrom").Index(0).Child("cookies"), 0, 0),
			},
		},
		"apiKeyAuth.forwardClientIDHeader too long": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					APIKeyAuth: &envoygatewayv1alpha1.APIKeyAuth{
						ForwardClientIDHeader: ptr.To("this-header-name-is-way-too-long"),
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.APIKeyAuth.MaxForwardClientIDHeaderLength = 1
			},
			expectedErrors: field.ErrorList{
				field.TooLong(field.NewPath("spec", "apiKeyAuth", "forwardClientIDHeader"), 0, 0),
			},
		},
		"CORS fields too long": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					CORS: &envoygatewayv1alpha1.CORS{
						AllowOrigins:  []envoygatewayv1alpha1.Origin{"origin1", "origin2", "origin3"},
						AllowMethods:  []string{"GET", "POST", "PUT", "DELETE"},
						AllowHeaders:  []string{"header1", "header2", "header3"},
						ExposeHeaders: []string{"expose1", "expose2", "expose3"},
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.CORS.MaxFieldLength = 1
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "cors", "allowOrigins"), 3, 2),
				field.TooMany(field.NewPath("spec", "cors", "allowMethods"), 4, 2),
				field.TooMany(field.NewPath("spec", "cors", "allowHeaders"), 3, 2),
				field.TooMany(field.NewPath("spec", "cors", "exposeHeaders"), 3, 2),
			},
		},
		"basicAuth.users namespace set": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					BasicAuth: &envoygatewayv1alpha1.BasicAuth{
						Users: gatewayv1.SecretObjectReference{
							Name:      "test-secret",
							Namespace: ptr.To(gatewayv1.Namespace("other-namespace")),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "basicAuth", "users", "namespace"), "must not be set"),
			},
		},
		"invalid remote jwks": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					JWT: &envoygatewayv1alpha1.JWT{
						Providers: []envoygatewayv1alpha1.JWTProvider{
							{
								Name: "test-provider",
								RemoteJWKS: &envoygatewayv1alpha1.RemoteJWKS{
									BackendCluster: envoygatewayv1alpha1.BackendCluster{
										BackendRef: &gatewayv1.BackendObjectReference{},
									},
								},
							},
							{
								Name: "invalid namespace",
								RemoteJWKS: &envoygatewayv1alpha1.RemoteJWKS{
									BackendCluster: envoygatewayv1alpha1.BackendCluster{
										BackendRefs: []envoygatewayv1alpha1.BackendRef{
											{
												BackendObjectReference: gatewayv1.BackendObjectReference{
													Name:      "test-backend",
													Namespace: ptr.To(gatewayv1.Namespace("other-namespace")),
												},
											},
										},
									},
								},
							},
							{
								// No comprehensive cluster setting validation here, as it's covered
								// by other tests. We just want to ensure that code path is executed.
								Name: "invalid cluster settings",
								RemoteJWKS: &envoygatewayv1alpha1.RemoteJWKS{
									BackendCluster: envoygatewayv1alpha1.BackendCluster{
										BackendRefs: []envoygatewayv1alpha1.BackendRef{
											{
												BackendObjectReference: gatewayv1.BackendObjectReference{
													Name: "test-backend",
												},
											},
										},
										BackendSettings: &envoygatewayv1alpha1.ClusterSettings{
											Retry: &envoygatewayv1alpha1.Retry{},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "jwt", "providers").Index(0).Child("remoteJWKS", "backendRef"), ""),
				field.Forbidden(field.NewPath("spec", "jwt", "providers").Index(1).Child("remoteJWKS", "backendRefs").Index(0).Child("namespace"), ""),
				field.Forbidden(field.NewPath("spec", "jwt", "providers").Index(2).Child("remoteJWKS", "backendSettings", "retry"), ""),
			},
		},
		"invalid JWT provider field lengths": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					JWT: &envoygatewayv1alpha1.JWT{
						Providers: []envoygatewayv1alpha1.JWTProvider{
							{
								Name: "test",
								ClaimToHeaders: []envoygatewayv1alpha1.ClaimToHeader{
									{
										Claim:  "claim1",
										Header: "header1",
									},
									{
										Claim:  "claim2",
										Header: "header2",
									},
								},
								ExtractFrom: &envoygatewayv1alpha1.JWTExtractor{
									Headers: []envoygatewayv1alpha1.JWTHeaderExtractor{
										{}, {}, {},
									},
									Cookies: []string{"cookie1", "cookie2", "cookie3"},
									Params:  []string{"param1", "param2", "param3"},
								},
							},
						},
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.JWTProvider.MaxClaimToHeaders = 1
				cfg.JWTProvider.MaxExtractorLength = 2
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "jwt", "providers").Index(0).Child("claimToHeaders"), 2, 1),
				field.TooMany(field.NewPath("spec", "jwt", "providers").Index(0).Child("extractFrom").Child("headers"), 3, 2),
				field.TooMany(field.NewPath("spec", "jwt", "providers").Index(0).Child("extractFrom").Child("cookies"), 3, 2),
				field.TooMany(field.NewPath("spec", "jwt", "providers").Index(0).Child("extractFrom").Child("params"), 3, 2),
			},
		},
		"invalid OIDC provider backend cluster settings": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					OIDC: &envoygatewayv1alpha1.OIDC{
						Provider: envoygatewayv1alpha1.OIDCProvider{
							// No comprehensive cluster setting validation here, as it's covered
							// by other tests. We just want to ensure that code path is executed.
							BackendCluster: envoygatewayv1alpha1.BackendCluster{
								BackendRef: &gatewayv1.BackendObjectReference{},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "oidc", "provider").Child("backendRef"), ""),
			},
		},
		"invalid OIDC provider secret refs": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					OIDC: &envoygatewayv1alpha1.OIDC{
						ClientIDRef: &gatewayv1.SecretObjectReference{
							Name:      "test-secret",
							Namespace: ptr.To(gatewayv1.Namespace("other-namespace")),
						},
						ClientSecret: gatewayv1.SecretObjectReference{
							Name:      "test-secret",
							Namespace: ptr.To(gatewayv1.Namespace("other-namespace")),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "oidc", "clientIDRef", "namespace"), "must not be set"),
				field.Forbidden(field.NewPath("spec", "oidc", "clientSecret", "namespace"), "must not be set"),
			},
		},
		"invalid oidc scopes, resources, and refresh token ttl": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					OIDC: &envoygatewayv1alpha1.OIDC{
						Scopes:                 []string{"scope1", "scope2", "scope3"},
						Resources:              []string{"resource1", "resource2", "resource3"},
						DefaultRefreshTokenTTL: ptr.To(gatewayv1.Duration("1s")),
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.OIDC.MaxScopes = 2
				cfg.OIDC.MaxResources = 2
				cfg.OIDC.MinRefreshTokenTTL = &metav1.Duration{Duration: 1 * time.Minute}
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "oidc", "scopes"), 0, 0),
				field.TooMany(field.NewPath("spec", "oidc", "resources"), 0, 0),
				field.Invalid(field.NewPath("spec", "oidc", "defaultRefreshTokenTTL"), 0, ""),
			},
		},
		"too many authorization rules": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					Authorization: &envoygatewayv1alpha1.Authorization{
						Rules: []envoygatewayv1alpha1.AuthorizationRule{
							{},
							{},
						},
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.Authorization.MaxRules = 1
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "authorization", "rules"), 2, 1),
			},
		},
		"authorization rule with too many CIDRs": {
			securityPolicy: &envoygatewayv1alpha1.SecurityPolicy{
				Spec: envoygatewayv1alpha1.SecurityPolicySpec{
					Authorization: &envoygatewayv1alpha1.Authorization{
						Rules: []envoygatewayv1alpha1.AuthorizationRule{
							{
								Principal: envoygatewayv1alpha1.Principal{
									ClientCIDRs: []envoygatewayv1alpha1.CIDR{
										"1.1.1.1/32",
										"1.1.1.2/32",
										"1.1.1.3/32",
									},
								},
							},
						},
					},
				},
			},
			opts: func(cfg *config.SecurityPolicyValidationOptions) {
				cfg.Authorization.MaxRules = 1
				cfg.Authorization.MaxClientCIDRs = 2
			},
			expectedErrors: field.ErrorList{
				field.TooMany(field.NewPath("spec", "authorization", "rules").Index(0).Child("principal", "clientCIDRs"), 3, 2),
			},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			opts := &config.NetworkServicesOperator{}
			config.SetObjectDefaults_NetworkServicesOperator(opts)
			securityPolicyOpts := opts.Gateway.ExtensionAPIValidationOptions.SecurityPolicies

			if scenario.opts != nil {
				scenario.opts(&securityPolicyOpts)
			}

			errs := ValidateSecurityPolicy(scenario.securityPolicy, securityPolicyOpts)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}

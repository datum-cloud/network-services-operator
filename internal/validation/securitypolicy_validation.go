package validation

import (
	"time"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type SecurityPolicyValidationOptions struct {
	APIKeyAuth    APIKeyAuthValidationOptions
	CORS          CORSValidationOptions
	JWTProvider   JWTProviderValidationOptions
	OIDC          OIDCValidationOptions
	Authorization AuthorizationValidationOptions
}

type APIKeyAuthValidationOptions struct {
	MaxCredentialRefs              int
	MaxExtractFrom                 int
	MaxExtractFromFieldLength      int
	MaxForwardClientIDHeaderLength int
}

type CORSValidationOptions struct {
	MaxFieldLength int
}

type JWTProviderValidationOptions struct {
	MaxClaimToHeaders  int
	MaxExtractorLength int
}

type OIDCValidationOptions struct {
	MaxScopes          int
	MaxResources       int
	MinRefreshTokenTTL time.Duration
}

type AuthorizationValidationOptions struct {
	MaxRules       int
	MaxClientCIDRs int
}

func ValidateSecurityPolicy(securityPolicy *envoygatewayv1alpha1.SecurityPolicy, opts SecurityPolicyValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	specPath := field.NewPath("spec")

	// nolint:staticcheck
	if securityPolicy.Spec.TargetRef != nil {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("targetRef"), "deprecated field, use spec.targetRefs or spec.targetSelectors instead"))
	}

	allErrs = append(allErrs, validateSecurityPolicyAPIKeyAuth(securityPolicy.Spec.APIKeyAuth, specPath.Child("apiKeyAuth"), opts)...)
	allErrs = append(allErrs, validateSecurityPolicyCORS(securityPolicy.Spec.CORS, specPath.Child("cors"), opts)...)
	allErrs = append(allErrs, validateSecurityPolicyBasicAuth(securityPolicy.Spec.BasicAuth, specPath.Child("basicAuth"))...)
	allErrs = append(allErrs, validateSecurityPolicyJWT(securityPolicy.Spec.JWT, specPath.Child("jwt"), opts)...)
	allErrs = append(allErrs, validateSecurityPolicyOIDC(securityPolicy.Spec.OIDC, specPath.Child("oidc"), opts)...)

	if securityPolicy.Spec.ExtAuth != nil {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("extAuth"), "extAuth is not permitted"))
	}

	allErrs = append(allErrs, validateSecurityPolicyAuthorization(securityPolicy.Spec.Authorization, specPath.Child("authorization"), opts)...)

	return allErrs
}

func validateSecurityPolicyAPIKeyAuth(apiKeyAuth *envoygatewayv1alpha1.APIKeyAuth, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {
	if apiKeyAuth == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateSecurityPolicyCredentialRefs(apiKeyAuth.CredentialRefs, fldPath.Child("credentialRefs"), opts)...)

	if len(apiKeyAuth.ExtractFrom) > opts.APIKeyAuth.MaxExtractFrom {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("extractFrom"), len(apiKeyAuth.ExtractFrom), opts.APIKeyAuth.MaxExtractFrom))
	}

	for i := range apiKeyAuth.ExtractFrom {
		extractFromPath := fldPath.Child("extractFrom").Index(i)
		allErrs = append(allErrs, validateSecurityPolicyExtractFrom(apiKeyAuth.ExtractFrom[i], extractFromPath, opts)...)
	}

	if apiKeyAuth.ForwardClientIDHeader != nil && len(*apiKeyAuth.ForwardClientIDHeader) > opts.APIKeyAuth.MaxForwardClientIDHeaderLength {
		allErrs = append(allErrs, field.TooLong(fldPath.Child("forwardClientIDHeader"), len(*apiKeyAuth.ForwardClientIDHeader), opts.APIKeyAuth.MaxForwardClientIDHeaderLength))
	}

	return allErrs
}

func validateSecurityPolicyCORS(cors *envoygatewayv1alpha1.CORS, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {
	if cors == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if len(cors.AllowOrigins) > opts.CORS.MaxFieldLength {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("allowOrigins"), len(cors.AllowOrigins), opts.CORS.MaxFieldLength))
	}

	if len(cors.AllowMethods) > opts.CORS.MaxFieldLength {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("allowMethods"), len(cors.AllowMethods), opts.CORS.MaxFieldLength))
	}

	if len(cors.AllowHeaders) > opts.CORS.MaxFieldLength {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("allowHeaders"), len(cors.AllowHeaders), opts.CORS.MaxFieldLength))
	}

	if len(cors.ExposeHeaders) > opts.CORS.MaxFieldLength {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("exposeHeaders"), len(cors.ExposeHeaders), opts.CORS.MaxFieldLength))
	}

	return allErrs
}

func validateSecurityPolicyBasicAuth(basicAuth *envoygatewayv1alpha1.BasicAuth, fldPath *field.Path) field.ErrorList {
	if basicAuth == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateGatewaySecretObjectReference(&basicAuth.Users, fldPath.Child("users"))...)

	return allErrs
}

func validateSecurityPolicyJWT(jwt *envoygatewayv1alpha1.JWT, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {
	if jwt == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	// JWT structs have a variety of length limit checks already.

	for i, provider := range jwt.Providers {
		providerPath := fldPath.Child("providers").Index(i)
		allErrs = append(allErrs, validateGatewayJWTProvider(provider, providerPath, opts)...)
	}

	return allErrs
}

func validateSecurityPolicyOIDC(oidc *envoygatewayv1alpha1.OIDC, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {
	if oidc == nil {
		return nil
	}

	allErrs := field.ErrorList{}
	allErrs = append(allErrs, validateSecurityPolicyOIDCProvider(oidc.Provider, fldPath.Child("provider"))...)
	allErrs = append(allErrs, validateGatewaySecretObjectReference(oidc.ClientIDRef, fldPath.Child("clientIDRef"))...)
	allErrs = append(allErrs, validateGatewaySecretObjectReference(&oidc.ClientSecret, fldPath.Child("clientSecret"))...)

	if len(oidc.Scopes) > opts.OIDC.MaxScopes {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("scopes"), len(oidc.Scopes), opts.OIDC.MaxScopes))
	}

	if len(oidc.Resources) > opts.OIDC.MaxResources {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("resources"), len(oidc.Resources), opts.OIDC.MaxResources))
	}

	if oidc.DefaultRefreshTokenTTL != nil {
		allErrs = append(allErrs, validateGatewayDuration(fldPath.Child("defaultRefreshTokenTTL"), oidc.DefaultRefreshTokenTTL, &opts.OIDC.MinRefreshTokenTTL, nil)...)
	}

	return allErrs
}

func validateSecurityPolicyAuthorization(authorization *envoygatewayv1alpha1.Authorization, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {
	if authorization == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if len(authorization.Rules) > opts.Authorization.MaxRules {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("rules"), len(authorization.Rules), opts.Authorization.MaxRules))
	}

	for i, rule := range authorization.Rules {
		rulePath := fldPath.Child("rules").Index(i)

		allErrs = append(allErrs, validateAuthorizationPrincipal(rule.Principal, rulePath.Child("principal"), opts)...)

	}

	return allErrs
}

func validateAuthorizationPrincipal(principal envoygatewayv1alpha1.Principal, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {

	allErrs := field.ErrorList{}

	if len(principal.ClientCIDRs) > opts.Authorization.MaxClientCIDRs {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("clientCIDRs"), len(principal.ClientCIDRs), opts.Authorization.MaxClientCIDRs))
	}

	return allErrs
}

func validateSecurityPolicyOIDCProvider(provider envoygatewayv1alpha1.OIDCProvider, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateGatewayBackendCluster(provider.BackendCluster, fldPath)...)

	return allErrs
}

func validateGatewayJWTProvider(provider envoygatewayv1alpha1.JWTProvider, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateRemoteJWKS(provider.RemoteJWKS, fldPath.Child("remoteJWKS"))...)

	if len(provider.ClaimToHeaders) > opts.JWTProvider.MaxClaimToHeaders {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("claimToHeaders"), len(provider.ClaimToHeaders), opts.JWTProvider.MaxClaimToHeaders))
	}

	if provider.ExtractFrom != nil {
		if len(provider.ExtractFrom.Headers) > opts.JWTProvider.MaxExtractorLength {
			allErrs = append(allErrs, field.TooMany(fldPath.Child("extractFrom").Child("headers"), len(provider.ExtractFrom.Headers), opts.JWTProvider.MaxExtractorLength))
		}

		if len(provider.ExtractFrom.Cookies) > opts.JWTProvider.MaxExtractorLength {
			allErrs = append(allErrs, field.TooMany(fldPath.Child("extractFrom").Child("cookies"), len(provider.ExtractFrom.Cookies), opts.JWTProvider.MaxExtractorLength))
		}

		if len(provider.ExtractFrom.Params) > opts.JWTProvider.MaxExtractorLength {
			allErrs = append(allErrs, field.TooMany(fldPath.Child("extractFrom").Child("params"), len(provider.ExtractFrom.Params), opts.JWTProvider.MaxExtractorLength))
		}
	}

	return allErrs
}

func validateRemoteJWKS(remoteJWKS *envoygatewayv1alpha1.RemoteJWKS, fldPath *field.Path) field.ErrorList {
	if remoteJWKS == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateGatewayBackendCluster(remoteJWKS.BackendCluster, fldPath)...)

	return allErrs
}

func validateGatewayBackendCluster(backendCluster envoygatewayv1alpha1.BackendCluster, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	// nolint:staticcheck
	if backendCluster.BackendRef != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("backendRef"), "backendRef is not permitted"))
	}

	for i, backendRef := range backendCluster.BackendRefs {
		allErrs = append(allErrs, validateBackendClusterBackendObjectReference(backendRef.BackendObjectReference, fldPath.Child("backendRefs").Index(i))...)
	}

	if backendCluster.BackendSettings != nil {
		allErrs = append(allErrs, validateGatewayClusterSettings(*backendCluster.BackendSettings, fldPath.Child("backendSettings"))...)
	}

	return allErrs
}

func validateBackendClusterBackendObjectReference(backendObjectRef gatewayv1.BackendObjectReference, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if backendObjectRef.Namespace != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("namespace"), "must not be set"))
	}

	return allErrs
}

func validateSecurityPolicyExtractFrom(extractFrom *envoygatewayv1alpha1.ExtractFrom, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {
	if extractFrom == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if len(extractFrom.Headers) > opts.APIKeyAuth.MaxExtractFromFieldLength {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("headers"), len(extractFrom.Headers), opts.APIKeyAuth.MaxExtractFromFieldLength))
	}

	if len(extractFrom.Params) > opts.APIKeyAuth.MaxExtractFromFieldLength {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("params"), len(extractFrom.Params), opts.APIKeyAuth.MaxExtractFromFieldLength))
	}

	if len(extractFrom.Cookies) > opts.APIKeyAuth.MaxExtractFromFieldLength {
		allErrs = append(allErrs, field.TooMany(fldPath.Child("cookies"), len(extractFrom.Cookies), opts.APIKeyAuth.MaxExtractFromFieldLength))
	}

	return allErrs
}

func validateSecurityPolicyCredentialRefs(refs []gatewayv1.SecretObjectReference, fldPath *field.Path, opts SecurityPolicyValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(refs) > opts.APIKeyAuth.MaxCredentialRefs {
		allErrs = append(allErrs, field.TooMany(fldPath, len(refs), opts.APIKeyAuth.MaxCredentialRefs))
	}

	for i, ref := range refs {
		refPath := fldPath.Index(i)
		allErrs = append(allErrs, validateGatewaySecretObjectReference(&ref, refPath)...)
	}

	return allErrs
}

func validateGatewaySecretObjectReference(secretRef *gatewayv1.SecretObjectReference, fldPath *field.Path) field.ErrorList {
	if secretRef == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	// Namespace must not be set
	if secretRef.Namespace != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("namespace"), "must not be set"))
	}

	return allErrs
}

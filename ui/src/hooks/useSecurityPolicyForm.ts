'use client';

import { useCallback, useMemo } from 'react';
import { useSecurityPolicyFormContext } from '@/components/security-policy/SecurityPolicyFormContext';
import type {
  TargetRefFormState,
  BasicAuthFormState,
  APIKeyAuthFormState,
  ExtractFromFormState,
  JWTFormState,
  JWTProviderFormState,
  ClaimToHeaderFormState,
  OIDCFormState,
  CORSFormState,
  StringMatchFormState,
  AuthorizationFormState,
  AuthorizationRuleFormState,
  CIDRFormState,
  SecretRefFormState,
  AUTH_TYPE_LABELS,
} from '@/lib/security-policy-defaults';
import { getFieldErrors, hasFieldError } from '@/lib/security-policy-validation';

export function useSecurityPolicyForm() {
  const { state, dispatch, toSecurityPolicy, validate, isValid } = useSecurityPolicyFormContext();

  // Metadata helpers
  const setName = useCallback(
    (name: string) => dispatch({ type: 'SET_NAME', payload: name }),
    [dispatch]
  );

  const setNamespace = useCallback(
    (namespace: string) => dispatch({ type: 'SET_NAMESPACE', payload: namespace }),
    [dispatch]
  );

  // Target ref helpers
  const addTargetRef = useCallback(
    () => dispatch({ type: 'ADD_TARGET_REF' }),
    [dispatch]
  );

  const removeTargetRef = useCallback(
    (id: string) => dispatch({ type: 'REMOVE_TARGET_REF', payload: id }),
    [dispatch]
  );

  const updateTargetRef = useCallback(
    (id: string, updates: Partial<TargetRefFormState>) =>
      dispatch({ type: 'UPDATE_TARGET_REF', payload: { id, updates } }),
    [dispatch]
  );

  // Auth type toggle helpers
  const enableBasicAuth = useCallback(() => dispatch({ type: 'ENABLE_BASIC_AUTH' }), [dispatch]);
  const disableBasicAuth = useCallback(() => dispatch({ type: 'DISABLE_BASIC_AUTH' }), [dispatch]);
  const enableAPIKeyAuth = useCallback(() => dispatch({ type: 'ENABLE_API_KEY_AUTH' }), [dispatch]);
  const disableAPIKeyAuth = useCallback(() => dispatch({ type: 'DISABLE_API_KEY_AUTH' }), [dispatch]);
  const enableJWT = useCallback(() => dispatch({ type: 'ENABLE_JWT' }), [dispatch]);
  const disableJWT = useCallback(() => dispatch({ type: 'DISABLE_JWT' }), [dispatch]);
  const enableOIDC = useCallback(() => dispatch({ type: 'ENABLE_OIDC' }), [dispatch]);
  const disableOIDC = useCallback(() => dispatch({ type: 'DISABLE_OIDC' }), [dispatch]);
  const enableCORS = useCallback(() => dispatch({ type: 'ENABLE_CORS' }), [dispatch]);
  const disableCORS = useCallback(() => dispatch({ type: 'DISABLE_CORS' }), [dispatch]);
  const enableAuthorization = useCallback(() => dispatch({ type: 'ENABLE_AUTHORIZATION' }), [dispatch]);
  const disableAuthorization = useCallback(() => dispatch({ type: 'DISABLE_AUTHORIZATION' }), [dispatch]);

  // Toggle helpers (convenience wrappers)
  const toggleBasicAuth = useCallback(
    (enabled: boolean) => enabled ? enableBasicAuth() : disableBasicAuth(),
    [enableBasicAuth, disableBasicAuth]
  );

  const toggleAPIKeyAuth = useCallback(
    (enabled: boolean) => enabled ? enableAPIKeyAuth() : disableAPIKeyAuth(),
    [enableAPIKeyAuth, disableAPIKeyAuth]
  );

  const toggleJWT = useCallback(
    (enabled: boolean) => enabled ? enableJWT() : disableJWT(),
    [enableJWT, disableJWT]
  );

  const toggleOIDC = useCallback(
    (enabled: boolean) => enabled ? enableOIDC() : disableOIDC(),
    [enableOIDC, disableOIDC]
  );

  const toggleCORS = useCallback(
    (enabled: boolean) => enabled ? enableCORS() : disableCORS(),
    [enableCORS, disableCORS]
  );

  const toggleAuthorization = useCallback(
    (enabled: boolean) => enabled ? enableAuthorization() : disableAuthorization(),
    [enableAuthorization, disableAuthorization]
  );

  // BasicAuth helpers
  const updateBasicAuth = useCallback(
    (updates: Partial<BasicAuthFormState>) =>
      dispatch({ type: 'UPDATE_BASIC_AUTH', payload: updates }),
    [dispatch]
  );

  // APIKeyAuth helpers
  const updateAPIKeyAuth = useCallback(
    (updates: Partial<APIKeyAuthFormState>) =>
      dispatch({ type: 'UPDATE_API_KEY_AUTH', payload: updates }),
    [dispatch]
  );

  const addCredentialRef = useCallback(
    () => dispatch({ type: 'ADD_CREDENTIAL_REF' }),
    [dispatch]
  );

  const removeCredentialRef = useCallback(
    (id: string) => dispatch({ type: 'REMOVE_CREDENTIAL_REF', payload: id }),
    [dispatch]
  );

  const updateCredentialRef = useCallback(
    (id: string, updates: Partial<SecretRefFormState>) =>
      dispatch({ type: 'UPDATE_CREDENTIAL_REF', payload: { id, updates } }),
    [dispatch]
  );

  const addExtractFrom = useCallback(
    () => dispatch({ type: 'ADD_EXTRACT_FROM' }),
    [dispatch]
  );

  const removeExtractFrom = useCallback(
    (id: string) => dispatch({ type: 'REMOVE_EXTRACT_FROM', payload: id }),
    [dispatch]
  );

  const updateExtractFrom = useCallback(
    (id: string, updates: Partial<ExtractFromFormState>) =>
      dispatch({ type: 'UPDATE_EXTRACT_FROM', payload: { id, updates } }),
    [dispatch]
  );

  // JWT helpers
  const updateJWT = useCallback(
    (updates: Partial<JWTFormState>) =>
      dispatch({ type: 'UPDATE_JWT', payload: updates }),
    [dispatch]
  );

  const addJWTProvider = useCallback(
    () => dispatch({ type: 'ADD_JWT_PROVIDER' }),
    [dispatch]
  );

  const removeJWTProvider = useCallback(
    (id: string) => dispatch({ type: 'REMOVE_JWT_PROVIDER', payload: id }),
    [dispatch]
  );

  const updateJWTProvider = useCallback(
    (id: string, updates: Partial<JWTProviderFormState>) =>
      dispatch({ type: 'UPDATE_JWT_PROVIDER', payload: { id, updates } }),
    [dispatch]
  );

  const addClaimToHeader = useCallback(
    (providerId: string) =>
      dispatch({ type: 'ADD_CLAIM_TO_HEADER', payload: { providerId } }),
    [dispatch]
  );

  const removeClaimToHeader = useCallback(
    (providerId: string, claimId: string) =>
      dispatch({ type: 'REMOVE_CLAIM_TO_HEADER', payload: { providerId, claimId } }),
    [dispatch]
  );

  const updateClaimToHeader = useCallback(
    (providerId: string, claimId: string, updates: Partial<ClaimToHeaderFormState>) =>
      dispatch({ type: 'UPDATE_CLAIM_TO_HEADER', payload: { providerId, claimId, updates } }),
    [dispatch]
  );

  // OIDC helpers
  const updateOIDC = useCallback(
    (updates: Partial<OIDCFormState>) =>
      dispatch({ type: 'UPDATE_OIDC', payload: updates }),
    [dispatch]
  );

  const updateOIDCProvider = useCallback(
    (updates: Partial<OIDCFormState['provider']>) =>
      dispatch({ type: 'UPDATE_OIDC_PROVIDER', payload: updates }),
    [dispatch]
  );

  // CORS helpers
  const updateCORS = useCallback(
    (updates: Partial<CORSFormState>) =>
      dispatch({ type: 'UPDATE_CORS', payload: updates }),
    [dispatch]
  );

  const addCORSOrigin = useCallback(
    () => dispatch({ type: 'ADD_CORS_ORIGIN' }),
    [dispatch]
  );

  const removeCORSOrigin = useCallback(
    (id: string) => dispatch({ type: 'REMOVE_CORS_ORIGIN', payload: id }),
    [dispatch]
  );

  const updateCORSOrigin = useCallback(
    (id: string, updates: Partial<StringMatchFormState>) =>
      dispatch({ type: 'UPDATE_CORS_ORIGIN', payload: { id, updates } }),
    [dispatch]
  );

  // Authorization helpers
  const updateAuthorization = useCallback(
    (updates: Partial<AuthorizationFormState>) =>
      dispatch({ type: 'UPDATE_AUTHORIZATION', payload: updates }),
    [dispatch]
  );

  const addAuthorizationRule = useCallback(
    () => dispatch({ type: 'ADD_AUTHORIZATION_RULE' }),
    [dispatch]
  );

  const removeAuthorizationRule = useCallback(
    (id: string) => dispatch({ type: 'REMOVE_AUTHORIZATION_RULE', payload: id }),
    [dispatch]
  );

  const updateAuthorizationRule = useCallback(
    (id: string, updates: Partial<AuthorizationRuleFormState>) =>
      dispatch({ type: 'UPDATE_AUTHORIZATION_RULE', payload: { id, updates } }),
    [dispatch]
  );

  const addClientCIDR = useCallback(
    (ruleId: string) =>
      dispatch({ type: 'ADD_CLIENT_CIDR', payload: { ruleId } }),
    [dispatch]
  );

  const removeClientCIDR = useCallback(
    (ruleId: string, cidrId: string) =>
      dispatch({ type: 'REMOVE_CLIENT_CIDR', payload: { ruleId, cidrId } }),
    [dispatch]
  );

  const updateClientCIDR = useCallback(
    (ruleId: string, cidrId: string, updates: Partial<CIDRFormState>) =>
      dispatch({ type: 'UPDATE_CLIENT_CIDR', payload: { ruleId, cidrId, updates } }),
    [dispatch]
  );

  // UI helpers
  const toggleSectionExpanded = useCallback(
    (section: string) => dispatch({ type: 'TOGGLE_SECTION_EXPANDED', payload: section }),
    [dispatch]
  );

  const setFieldTouched = useCallback(
    (field: string) => dispatch({ type: 'SET_FIELD_TOUCHED', payload: field }),
    [dispatch]
  );

  // Reset
  const reset = useCallback(() => dispatch({ type: 'RESET' }), [dispatch]);

  // Validation helpers
  const getErrors = useCallback(
    (fieldPrefix: string) => getFieldErrors(state.validation.errors, fieldPrefix),
    [state.validation.errors]
  );

  const hasError = useCallback(
    (fieldPrefix: string) => hasFieldError(state.validation.errors, fieldPrefix),
    [state.validation.errors]
  );

  // Computed values
  const enabledAuthTypes = useMemo(() => {
    const types: string[] = [];
    if (state.spec.basicAuth) types.push('basicAuth');
    if (state.spec.apiKeyAuth) types.push('apiKeyAuth');
    if (state.spec.jwt) types.push('jwt');
    if (state.spec.oidc) types.push('oidc');
    if (state.spec.cors) types.push('cors');
    if (state.spec.authorization) types.push('authorization');
    return types;
  }, [state.spec]);

  // Summary helpers for display
  const getAuthTypeSummary = useCallback((): string => {
    if (enabledAuthTypes.length === 0) {
      return 'No authentication configured';
    }
    return enabledAuthTypes
      .map(type => {
        const labels: Record<string, string> = {
          basicAuth: 'Basic Auth',
          apiKeyAuth: 'API Key Auth',
          jwt: 'JWT',
          oidc: 'OIDC',
          cors: 'CORS',
          authorization: 'Authorization',
        };
        return labels[type] || type;
      })
      .join(', ');
  }, [enabledAuthTypes]);

  const getTargetsSummary = useCallback((): string => {
    const count = state.spec.targetRefs.length;
    if (count === 0) return 'No targets';
    if (count === 1) return `1 target (${state.spec.targetRefs[0].kind}: ${state.spec.targetRefs[0].name || 'unnamed'})`;
    return `${count} targets`;
  }, [state.spec.targetRefs]);

  return {
    // State
    state,
    name: state.metadata.name,
    namespace: state.metadata.namespace,
    targetRefs: state.spec.targetRefs,
    basicAuth: state.spec.basicAuth,
    apiKeyAuth: state.spec.apiKeyAuth,
    jwt: state.spec.jwt,
    oidc: state.spec.oidc,
    cors: state.spec.cors,
    authorization: state.spec.authorization,
    errors: state.validation.errors,
    expandedSections: state.ui.expandedSections,

    // Metadata
    setName,
    setNamespace,

    // Target refs
    addTargetRef,
    removeTargetRef,
    updateTargetRef,

    // Auth type toggles
    enableBasicAuth,
    disableBasicAuth,
    enableAPIKeyAuth,
    disableAPIKeyAuth,
    enableJWT,
    disableJWT,
    enableOIDC,
    disableOIDC,
    enableCORS,
    disableCORS,
    enableAuthorization,
    disableAuthorization,
    toggleBasicAuth,
    toggleAPIKeyAuth,
    toggleJWT,
    toggleOIDC,
    toggleCORS,
    toggleAuthorization,

    // BasicAuth
    updateBasicAuth,

    // APIKeyAuth
    updateAPIKeyAuth,
    addCredentialRef,
    removeCredentialRef,
    updateCredentialRef,
    addExtractFrom,
    removeExtractFrom,
    updateExtractFrom,

    // JWT
    updateJWT,
    addJWTProvider,
    removeJWTProvider,
    updateJWTProvider,
    addClaimToHeader,
    removeClaimToHeader,
    updateClaimToHeader,

    // OIDC
    updateOIDC,
    updateOIDCProvider,

    // CORS
    updateCORS,
    addCORSOrigin,
    removeCORSOrigin,
    updateCORSOrigin,

    // Authorization
    updateAuthorization,
    addAuthorizationRule,
    removeAuthorizationRule,
    updateAuthorizationRule,
    addClientCIDR,
    removeClientCIDR,
    updateClientCIDR,

    // UI
    toggleSectionExpanded,
    setFieldTouched,

    // Validation
    validate,
    isValid,
    getErrors,
    hasError,

    // Conversion
    toSecurityPolicy,

    // Reset
    reset,

    // Computed
    enabledAuthTypes,

    // Summaries
    getAuthTypeSummary,
    getTargetsSummary,
  };
}

export type SecurityPolicyFormHook = ReturnType<typeof useSecurityPolicyForm>;

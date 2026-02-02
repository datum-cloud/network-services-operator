'use client';

import { createContext, useContext, useReducer, ReactNode } from 'react';
import type { SecurityPolicy } from '@/api/types';
import {
  SecurityPolicyFormState,
  SecurityPolicySpecFormState,
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
  createDefaultSecurityPolicy,
  createDefaultTargetRef,
  createDefaultSecretRef,
  createDefaultBasicAuth,
  createDefaultAPIKeyAuth,
  createDefaultExtractFrom,
  createDefaultJWT,
  createDefaultJWTProvider,
  createDefaultClaimToHeader,
  createDefaultOIDC,
  createDefaultCORS,
  createDefaultStringMatch,
  createDefaultAuthorization,
  createDefaultAuthorizationRule,
  createDefaultCIDR,
  securityPolicyFormStateToAPI,
  securityPolicyAPIToFormState,
} from '@/lib/security-policy-defaults';
import { validateSecurityPolicyForm, ValidationError } from '@/lib/security-policy-validation';

// Action types
type SecurityPolicyFormAction =
  // Metadata actions
  | { type: 'SET_NAME'; payload: string }
  | { type: 'SET_NAMESPACE'; payload: string }
  // Target ref actions
  | { type: 'ADD_TARGET_REF' }
  | { type: 'REMOVE_TARGET_REF'; payload: string }
  | { type: 'UPDATE_TARGET_REF'; payload: { id: string; updates: Partial<TargetRefFormState> } }
  // Auth type toggle actions
  | { type: 'ENABLE_BASIC_AUTH' }
  | { type: 'DISABLE_BASIC_AUTH' }
  | { type: 'ENABLE_API_KEY_AUTH' }
  | { type: 'DISABLE_API_KEY_AUTH' }
  | { type: 'ENABLE_JWT' }
  | { type: 'DISABLE_JWT' }
  | { type: 'ENABLE_OIDC' }
  | { type: 'DISABLE_OIDC' }
  | { type: 'ENABLE_CORS' }
  | { type: 'DISABLE_CORS' }
  | { type: 'ENABLE_AUTHORIZATION' }
  | { type: 'DISABLE_AUTHORIZATION' }
  // BasicAuth actions
  | { type: 'UPDATE_BASIC_AUTH'; payload: Partial<BasicAuthFormState> }
  // APIKeyAuth actions
  | { type: 'UPDATE_API_KEY_AUTH'; payload: Partial<APIKeyAuthFormState> }
  | { type: 'ADD_CREDENTIAL_REF' }
  | { type: 'REMOVE_CREDENTIAL_REF'; payload: string }
  | { type: 'UPDATE_CREDENTIAL_REF'; payload: { id: string; updates: Partial<SecretRefFormState> } }
  | { type: 'ADD_EXTRACT_FROM' }
  | { type: 'REMOVE_EXTRACT_FROM'; payload: string }
  | { type: 'UPDATE_EXTRACT_FROM'; payload: { id: string; updates: Partial<ExtractFromFormState> } }
  // JWT actions
  | { type: 'UPDATE_JWT'; payload: Partial<JWTFormState> }
  | { type: 'ADD_JWT_PROVIDER' }
  | { type: 'REMOVE_JWT_PROVIDER'; payload: string }
  | { type: 'UPDATE_JWT_PROVIDER'; payload: { id: string; updates: Partial<JWTProviderFormState> } }
  | { type: 'ADD_CLAIM_TO_HEADER'; payload: { providerId: string } }
  | { type: 'REMOVE_CLAIM_TO_HEADER'; payload: { providerId: string; claimId: string } }
  | { type: 'UPDATE_CLAIM_TO_HEADER'; payload: { providerId: string; claimId: string; updates: Partial<ClaimToHeaderFormState> } }
  // OIDC actions
  | { type: 'UPDATE_OIDC'; payload: Partial<OIDCFormState> }
  | { type: 'UPDATE_OIDC_PROVIDER'; payload: Partial<OIDCFormState['provider']> }
  // CORS actions
  | { type: 'UPDATE_CORS'; payload: Partial<CORSFormState> }
  | { type: 'ADD_CORS_ORIGIN' }
  | { type: 'REMOVE_CORS_ORIGIN'; payload: string }
  | { type: 'UPDATE_CORS_ORIGIN'; payload: { id: string; updates: Partial<StringMatchFormState> } }
  // Authorization actions
  | { type: 'UPDATE_AUTHORIZATION'; payload: Partial<AuthorizationFormState> }
  | { type: 'ADD_AUTHORIZATION_RULE' }
  | { type: 'REMOVE_AUTHORIZATION_RULE'; payload: string }
  | { type: 'UPDATE_AUTHORIZATION_RULE'; payload: { id: string; updates: Partial<AuthorizationRuleFormState> } }
  | { type: 'ADD_CLIENT_CIDR'; payload: { ruleId: string } }
  | { type: 'REMOVE_CLIENT_CIDR'; payload: { ruleId: string; cidrId: string } }
  | { type: 'UPDATE_CLIENT_CIDR'; payload: { ruleId: string; cidrId: string; updates: Partial<CIDRFormState> } }
  // UI actions
  | { type: 'TOGGLE_SECTION_EXPANDED'; payload: string }
  | { type: 'SET_FIELD_TOUCHED'; payload: string }
  // Validation
  | { type: 'VALIDATE' }
  | { type: 'CLEAR_VALIDATION' }
  // Load from existing policy
  | { type: 'LOAD_FROM_POLICY'; payload: SecurityPolicy }
  // Reset form
  | { type: 'RESET' };

// Reducer
function securityPolicyFormReducer(
  state: SecurityPolicyFormState,
  action: SecurityPolicyFormAction
): SecurityPolicyFormState {
  switch (action.type) {
    // Metadata
    case 'SET_NAME':
      return {
        ...state,
        metadata: { ...state.metadata, name: action.payload },
      };

    case 'SET_NAMESPACE':
      return {
        ...state,
        metadata: { ...state.metadata, namespace: action.payload },
      };

    // Target refs
    case 'ADD_TARGET_REF':
      return {
        ...state,
        spec: {
          ...state.spec,
          targetRefs: [...state.spec.targetRefs, createDefaultTargetRef()],
        },
      };

    case 'REMOVE_TARGET_REF':
      return {
        ...state,
        spec: {
          ...state.spec,
          targetRefs: state.spec.targetRefs.filter(r => r.id !== action.payload),
        },
      };

    case 'UPDATE_TARGET_REF':
      return {
        ...state,
        spec: {
          ...state.spec,
          targetRefs: state.spec.targetRefs.map(r =>
            r.id === action.payload.id ? { ...r, ...action.payload.updates } : r
          ),
        },
      };

    // Auth type toggles
    case 'ENABLE_BASIC_AUTH':
      return {
        ...state,
        spec: { ...state.spec, basicAuth: createDefaultBasicAuth() },
      };

    case 'DISABLE_BASIC_AUTH':
      return {
        ...state,
        spec: { ...state.spec, basicAuth: undefined },
      };

    case 'ENABLE_API_KEY_AUTH':
      return {
        ...state,
        spec: { ...state.spec, apiKeyAuth: createDefaultAPIKeyAuth() },
      };

    case 'DISABLE_API_KEY_AUTH':
      return {
        ...state,
        spec: { ...state.spec, apiKeyAuth: undefined },
      };

    case 'ENABLE_JWT':
      return {
        ...state,
        spec: { ...state.spec, jwt: createDefaultJWT() },
      };

    case 'DISABLE_JWT':
      return {
        ...state,
        spec: { ...state.spec, jwt: undefined },
      };

    case 'ENABLE_OIDC':
      return {
        ...state,
        spec: { ...state.spec, oidc: createDefaultOIDC() },
      };

    case 'DISABLE_OIDC':
      return {
        ...state,
        spec: { ...state.spec, oidc: undefined },
      };

    case 'ENABLE_CORS':
      return {
        ...state,
        spec: { ...state.spec, cors: createDefaultCORS() },
      };

    case 'DISABLE_CORS':
      return {
        ...state,
        spec: { ...state.spec, cors: undefined },
      };

    case 'ENABLE_AUTHORIZATION':
      return {
        ...state,
        spec: { ...state.spec, authorization: createDefaultAuthorization() },
      };

    case 'DISABLE_AUTHORIZATION':
      return {
        ...state,
        spec: { ...state.spec, authorization: undefined },
      };

    // BasicAuth
    case 'UPDATE_BASIC_AUTH':
      if (!state.spec.basicAuth) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          basicAuth: { ...state.spec.basicAuth, ...action.payload },
        },
      };

    // APIKeyAuth
    case 'UPDATE_API_KEY_AUTH':
      if (!state.spec.apiKeyAuth) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          apiKeyAuth: { ...state.spec.apiKeyAuth, ...action.payload },
        },
      };

    case 'ADD_CREDENTIAL_REF':
      if (!state.spec.apiKeyAuth) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          apiKeyAuth: {
            ...state.spec.apiKeyAuth,
            credentialRefs: [...state.spec.apiKeyAuth.credentialRefs, createDefaultSecretRef()],
          },
        },
      };

    case 'REMOVE_CREDENTIAL_REF':
      if (!state.spec.apiKeyAuth) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          apiKeyAuth: {
            ...state.spec.apiKeyAuth,
            credentialRefs: state.spec.apiKeyAuth.credentialRefs.filter(r => r.id !== action.payload),
          },
        },
      };

    case 'UPDATE_CREDENTIAL_REF':
      if (!state.spec.apiKeyAuth) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          apiKeyAuth: {
            ...state.spec.apiKeyAuth,
            credentialRefs: state.spec.apiKeyAuth.credentialRefs.map(r =>
              r.id === action.payload.id ? { ...r, ...action.payload.updates } : r
            ),
          },
        },
      };

    case 'ADD_EXTRACT_FROM':
      if (!state.spec.apiKeyAuth) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          apiKeyAuth: {
            ...state.spec.apiKeyAuth,
            extractFrom: [...state.spec.apiKeyAuth.extractFrom, createDefaultExtractFrom()],
          },
        },
      };

    case 'REMOVE_EXTRACT_FROM':
      if (!state.spec.apiKeyAuth) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          apiKeyAuth: {
            ...state.spec.apiKeyAuth,
            extractFrom: state.spec.apiKeyAuth.extractFrom.filter(e => e.id !== action.payload),
          },
        },
      };

    case 'UPDATE_EXTRACT_FROM':
      if (!state.spec.apiKeyAuth) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          apiKeyAuth: {
            ...state.spec.apiKeyAuth,
            extractFrom: state.spec.apiKeyAuth.extractFrom.map(e =>
              e.id === action.payload.id ? { ...e, ...action.payload.updates } : e
            ),
          },
        },
      };

    // JWT
    case 'UPDATE_JWT':
      if (!state.spec.jwt) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          jwt: { ...state.spec.jwt, ...action.payload },
        },
      };

    case 'ADD_JWT_PROVIDER':
      if (!state.spec.jwt) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          jwt: {
            ...state.spec.jwt,
            providers: [...state.spec.jwt.providers, createDefaultJWTProvider()],
          },
        },
      };

    case 'REMOVE_JWT_PROVIDER':
      if (!state.spec.jwt) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          jwt: {
            ...state.spec.jwt,
            providers: state.spec.jwt.providers.filter(p => p.id !== action.payload),
          },
        },
      };

    case 'UPDATE_JWT_PROVIDER':
      if (!state.spec.jwt) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          jwt: {
            ...state.spec.jwt,
            providers: state.spec.jwt.providers.map(p =>
              p.id === action.payload.id ? { ...p, ...action.payload.updates } : p
            ),
          },
        },
      };

    case 'ADD_CLAIM_TO_HEADER':
      if (!state.spec.jwt) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          jwt: {
            ...state.spec.jwt,
            providers: state.spec.jwt.providers.map(p =>
              p.id === action.payload.providerId
                ? { ...p, claimToHeaders: [...p.claimToHeaders, createDefaultClaimToHeader()] }
                : p
            ),
          },
        },
      };

    case 'REMOVE_CLAIM_TO_HEADER':
      if (!state.spec.jwt) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          jwt: {
            ...state.spec.jwt,
            providers: state.spec.jwt.providers.map(p =>
              p.id === action.payload.providerId
                ? { ...p, claimToHeaders: p.claimToHeaders.filter(c => c.id !== action.payload.claimId) }
                : p
            ),
          },
        },
      };

    case 'UPDATE_CLAIM_TO_HEADER':
      if (!state.spec.jwt) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          jwt: {
            ...state.spec.jwt,
            providers: state.spec.jwt.providers.map(p =>
              p.id === action.payload.providerId
                ? {
                    ...p,
                    claimToHeaders: p.claimToHeaders.map(c =>
                      c.id === action.payload.claimId ? { ...c, ...action.payload.updates } : c
                    ),
                  }
                : p
            ),
          },
        },
      };

    // OIDC
    case 'UPDATE_OIDC':
      if (!state.spec.oidc) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          oidc: { ...state.spec.oidc, ...action.payload },
        },
      };

    case 'UPDATE_OIDC_PROVIDER':
      if (!state.spec.oidc) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          oidc: {
            ...state.spec.oidc,
            provider: { ...state.spec.oidc.provider, ...action.payload },
          },
        },
      };

    // CORS
    case 'UPDATE_CORS':
      if (!state.spec.cors) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          cors: { ...state.spec.cors, ...action.payload },
        },
      };

    case 'ADD_CORS_ORIGIN':
      if (!state.spec.cors) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          cors: {
            ...state.spec.cors,
            allowOrigins: [...state.spec.cors.allowOrigins, createDefaultStringMatch()],
          },
        },
      };

    case 'REMOVE_CORS_ORIGIN':
      if (!state.spec.cors) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          cors: {
            ...state.spec.cors,
            allowOrigins: state.spec.cors.allowOrigins.filter(o => o.id !== action.payload),
          },
        },
      };

    case 'UPDATE_CORS_ORIGIN':
      if (!state.spec.cors) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          cors: {
            ...state.spec.cors,
            allowOrigins: state.spec.cors.allowOrigins.map(o =>
              o.id === action.payload.id ? { ...o, ...action.payload.updates } : o
            ),
          },
        },
      };

    // Authorization
    case 'UPDATE_AUTHORIZATION':
      if (!state.spec.authorization) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          authorization: { ...state.spec.authorization, ...action.payload },
        },
      };

    case 'ADD_AUTHORIZATION_RULE':
      if (!state.spec.authorization) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          authorization: {
            ...state.spec.authorization,
            rules: [...state.spec.authorization.rules, createDefaultAuthorizationRule()],
          },
        },
      };

    case 'REMOVE_AUTHORIZATION_RULE':
      if (!state.spec.authorization) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          authorization: {
            ...state.spec.authorization,
            rules: state.spec.authorization.rules.filter(r => r.id !== action.payload),
          },
        },
      };

    case 'UPDATE_AUTHORIZATION_RULE':
      if (!state.spec.authorization) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          authorization: {
            ...state.spec.authorization,
            rules: state.spec.authorization.rules.map(r =>
              r.id === action.payload.id ? { ...r, ...action.payload.updates } : r
            ),
          },
        },
      };

    case 'ADD_CLIENT_CIDR':
      if (!state.spec.authorization) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          authorization: {
            ...state.spec.authorization,
            rules: state.spec.authorization.rules.map(r =>
              r.id === action.payload.ruleId
                ? {
                    ...r,
                    principal: {
                      ...r.principal,
                      clientCIDRs: [...r.principal.clientCIDRs, createDefaultCIDR()],
                    },
                  }
                : r
            ),
          },
        },
      };

    case 'REMOVE_CLIENT_CIDR':
      if (!state.spec.authorization) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          authorization: {
            ...state.spec.authorization,
            rules: state.spec.authorization.rules.map(r =>
              r.id === action.payload.ruleId
                ? {
                    ...r,
                    principal: {
                      ...r.principal,
                      clientCIDRs: r.principal.clientCIDRs.filter(c => c.id !== action.payload.cidrId),
                    },
                  }
                : r
            ),
          },
        },
      };

    case 'UPDATE_CLIENT_CIDR':
      if (!state.spec.authorization) return state;
      return {
        ...state,
        spec: {
          ...state.spec,
          authorization: {
            ...state.spec.authorization,
            rules: state.spec.authorization.rules.map(r =>
              r.id === action.payload.ruleId
                ? {
                    ...r,
                    principal: {
                      ...r.principal,
                      clientCIDRs: r.principal.clientCIDRs.map(c =>
                        c.id === action.payload.cidrId ? { ...c, ...action.payload.updates } : c
                      ),
                    },
                  }
                : r
            ),
          },
        },
      };

    // UI
    case 'TOGGLE_SECTION_EXPANDED': {
      const newExpanded = new Set(state.ui.expandedSections);
      if (newExpanded.has(action.payload)) {
        newExpanded.delete(action.payload);
      } else {
        newExpanded.add(action.payload);
      }
      return {
        ...state,
        ui: { ...state.ui, expandedSections: newExpanded },
      };
    }

    case 'SET_FIELD_TOUCHED':
      return {
        ...state,
        validation: {
          ...state.validation,
          touched: { ...state.validation.touched, [action.payload]: true },
        },
      };

    // Validation
    case 'VALIDATE': {
      const result = validateSecurityPolicyForm(state);
      return {
        ...state,
        validation: {
          ...state.validation,
          errors: result.errors,
        },
      };
    }

    case 'CLEAR_VALIDATION':
      return {
        ...state,
        validation: {
          errors: [],
          touched: {},
        },
      };

    // Load from policy
    case 'LOAD_FROM_POLICY': {
      return securityPolicyAPIToFormState(action.payload);
    }

    // Reset
    case 'RESET':
      return createDefaultSecurityPolicy();

    default:
      return state;
  }
}

// Context
interface SecurityPolicyFormContextType {
  state: SecurityPolicyFormState;
  dispatch: React.Dispatch<SecurityPolicyFormAction>;
  toSecurityPolicy: () => SecurityPolicy;
  validate: () => boolean;
  isValid: boolean;
}

const SecurityPolicyFormContext = createContext<SecurityPolicyFormContextType | null>(null);

// Provider
interface SecurityPolicyFormProviderProps {
  children: ReactNode;
  initialPolicy?: SecurityPolicy;
}

export function SecurityPolicyFormProvider({ children, initialPolicy }: SecurityPolicyFormProviderProps) {
  const [state, dispatch] = useReducer(securityPolicyFormReducer, undefined, () => {
    if (initialPolicy) {
      return securityPolicyAPIToFormState(initialPolicy);
    }
    return createDefaultSecurityPolicy();
  });

  const toSecurityPolicy = (): SecurityPolicy => {
    return securityPolicyFormStateToAPI(state);
  };

  const validate = (): boolean => {
    dispatch({ type: 'VALIDATE' });
    const result = validateSecurityPolicyForm(state);
    return result.valid;
  };

  const isValid = state.validation.errors.length === 0;

  return (
    <SecurityPolicyFormContext.Provider value={{ state, dispatch, toSecurityPolicy, validate, isValid }}>
      {children}
    </SecurityPolicyFormContext.Provider>
  );
}

// Hook to use the context
export function useSecurityPolicyFormContext(): SecurityPolicyFormContextType {
  const context = useContext(SecurityPolicyFormContext);
  if (!context) {
    throw new Error('useSecurityPolicyFormContext must be used within a SecurityPolicyFormProvider');
  }
  return context;
}

// Export the action type for external use
export type { SecurityPolicyFormAction };

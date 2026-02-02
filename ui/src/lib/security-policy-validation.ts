// Validation constants from backend config.go
// These match the backend SecurityPolicyValidationOptions

export const MAX_CREDENTIAL_REFS = 5;
export const MAX_EXTRACT_FROM = 5;
export const MAX_EXTRACT_FROM_FIELD_LENGTH = 10;
export const MAX_FORWARD_CLIENT_ID_HEADER_LENGTH = 256;
export const MAX_CORS_FIELD_LENGTH = 10;
export const MAX_JWT_CLAIM_TO_HEADERS = 5;
export const MAX_JWT_EXTRACTOR_LENGTH = 5;
export const MAX_OIDC_SCOPES = 5;
export const MAX_OIDC_RESOURCES = 5;
export const MIN_REFRESH_TOKEN_TTL = '5m';
export const MAX_AUTHORIZATION_RULES = 20;
export const MAX_CLIENT_CIDRS = 5;

// Validation types
export interface ValidationError {
  field: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}

// Import form state types (will be created in defaults file)
import type {
  SecurityPolicyFormState,
  BasicAuthFormState,
  APIKeyAuthFormState,
  JWTFormState,
  OIDCFormState,
  CORSFormState,
  AuthorizationFormState,
} from './security-policy-defaults';

// Main validation function
export function validateSecurityPolicyForm(state: SecurityPolicyFormState): ValidationResult {
  const errors: ValidationError[] = [];

  // Validate metadata
  errors.push(...validateName(state.metadata.name).errors);
  errors.push(...validateNamespace(state.metadata.namespace).errors);

  // Validate target refs
  if (!state.spec.targetRefs || state.spec.targetRefs.length === 0) {
    errors.push({ field: 'targetRefs', message: 'At least one target reference is required' });
  }

  // Validate each enabled auth type
  if (state.spec.basicAuth) {
    errors.push(...validateBasicAuth(state.spec.basicAuth).errors);
  }

  if (state.spec.apiKeyAuth) {
    errors.push(...validateAPIKeyAuth(state.spec.apiKeyAuth).errors);
  }

  if (state.spec.jwt) {
    errors.push(...validateJWT(state.spec.jwt).errors);
  }

  if (state.spec.oidc) {
    errors.push(...validateOIDC(state.spec.oidc).errors);
  }

  if (state.spec.cors) {
    errors.push(...validateCORS(state.spec.cors).errors);
  }

  if (state.spec.authorization) {
    errors.push(...validateAuthorization(state.spec.authorization).errors);
  }

  return { valid: errors.length === 0, errors };
}

// Validate name
export function validateName(name: string): ValidationResult {
  const errors: ValidationError[] = [];

  if (!name || name.trim() === '') {
    errors.push({ field: 'name', message: 'Name is required' });
  } else {
    const nameRegex = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
    if (!nameRegex.test(name)) {
      errors.push({
        field: 'name',
        message: 'Name must be lowercase alphanumeric characters or hyphens, and must start and end with an alphanumeric character',
      });
    }
    if (name.length > 253) {
      errors.push({ field: 'name', message: 'Name must be 253 characters or less' });
    }
  }

  return { valid: errors.length === 0, errors };
}

// Validate namespace
export function validateNamespace(namespace: string): ValidationResult {
  const errors: ValidationError[] = [];

  if (!namespace || namespace.trim() === '') {
    errors.push({ field: 'namespace', message: 'Namespace is required' });
  }

  return { valid: errors.length === 0, errors };
}

// Validate BasicAuth
export function validateBasicAuth(basicAuth: BasicAuthFormState): ValidationResult {
  const errors: ValidationError[] = [];

  if (!basicAuth.users?.name) {
    errors.push({ field: 'basicAuth.users.name', message: 'Users secret reference is required' });
  }

  // Namespace must not be set on secret references
  if (basicAuth.users?.namespace) {
    errors.push({ field: 'basicAuth.users.namespace', message: 'Namespace must not be set on secret reference' });
  }

  return { valid: errors.length === 0, errors };
}

// Validate APIKeyAuth
export function validateAPIKeyAuth(apiKeyAuth: APIKeyAuthFormState): ValidationResult {
  const errors: ValidationError[] = [];

  if (!apiKeyAuth.credentialRefs || apiKeyAuth.credentialRefs.length === 0) {
    errors.push({ field: 'apiKeyAuth.credentialRefs', message: 'At least one credential reference is required' });
  }

  if (apiKeyAuth.credentialRefs && apiKeyAuth.credentialRefs.length > MAX_CREDENTIAL_REFS) {
    errors.push({
      field: 'apiKeyAuth.credentialRefs',
      message: `Maximum ${MAX_CREDENTIAL_REFS} credential references allowed`,
    });
  }

  // Validate each credential ref
  apiKeyAuth.credentialRefs?.forEach((ref, i) => {
    if (!ref.name) {
      errors.push({ field: `apiKeyAuth.credentialRefs.${i}.name`, message: 'Secret name is required' });
    }
    if (ref.namespace) {
      errors.push({ field: `apiKeyAuth.credentialRefs.${i}.namespace`, message: 'Namespace must not be set' });
    }
  });

  // Validate extractFrom
  if (apiKeyAuth.extractFrom && apiKeyAuth.extractFrom.length > MAX_EXTRACT_FROM) {
    errors.push({
      field: 'apiKeyAuth.extractFrom',
      message: `Maximum ${MAX_EXTRACT_FROM} extractFrom entries allowed`,
    });
  }

  apiKeyAuth.extractFrom?.forEach((entry, i) => {
    if (entry.headers && entry.headers.length > MAX_EXTRACT_FROM_FIELD_LENGTH) {
      errors.push({
        field: `apiKeyAuth.extractFrom.${i}.headers`,
        message: `Maximum ${MAX_EXTRACT_FROM_FIELD_LENGTH} headers allowed`,
      });
    }
    if (entry.params && entry.params.length > MAX_EXTRACT_FROM_FIELD_LENGTH) {
      errors.push({
        field: `apiKeyAuth.extractFrom.${i}.params`,
        message: `Maximum ${MAX_EXTRACT_FROM_FIELD_LENGTH} params allowed`,
      });
    }
    if (entry.cookies && entry.cookies.length > MAX_EXTRACT_FROM_FIELD_LENGTH) {
      errors.push({
        field: `apiKeyAuth.extractFrom.${i}.cookies`,
        message: `Maximum ${MAX_EXTRACT_FROM_FIELD_LENGTH} cookies allowed`,
      });
    }
  });

  // Validate forwardClientIDHeader length
  if (apiKeyAuth.forwardClientIDHeader && apiKeyAuth.forwardClientIDHeader.length > MAX_FORWARD_CLIENT_ID_HEADER_LENGTH) {
    errors.push({
      field: 'apiKeyAuth.forwardClientIDHeader',
      message: `Header name must be ${MAX_FORWARD_CLIENT_ID_HEADER_LENGTH} characters or less`,
    });
  }

  return { valid: errors.length === 0, errors };
}

// Validate JWT
export function validateJWT(jwt: JWTFormState): ValidationResult {
  const errors: ValidationError[] = [];

  if (!jwt.providers || jwt.providers.length === 0) {
    errors.push({ field: 'jwt.providers', message: 'At least one JWT provider is required' });
  }

  jwt.providers?.forEach((provider, i) => {
    if (!provider.name) {
      errors.push({ field: `jwt.providers.${i}.name`, message: 'Provider name is required' });
    }

    // Validate claimToHeaders
    if (provider.claimToHeaders && provider.claimToHeaders.length > MAX_JWT_CLAIM_TO_HEADERS) {
      errors.push({
        field: `jwt.providers.${i}.claimToHeaders`,
        message: `Maximum ${MAX_JWT_CLAIM_TO_HEADERS} claim to header mappings allowed`,
      });
    }

    provider.claimToHeaders?.forEach((mapping, j) => {
      if (!mapping.claim) {
        errors.push({ field: `jwt.providers.${i}.claimToHeaders.${j}.claim`, message: 'Claim is required' });
      }
      if (!mapping.header) {
        errors.push({ field: `jwt.providers.${i}.claimToHeaders.${j}.header`, message: 'Header is required' });
      }
    });

    // Validate extractFrom
    if (provider.extractFrom) {
      if (provider.extractFrom.headers && provider.extractFrom.headers.length > MAX_JWT_EXTRACTOR_LENGTH) {
        errors.push({
          field: `jwt.providers.${i}.extractFrom.headers`,
          message: `Maximum ${MAX_JWT_EXTRACTOR_LENGTH} headers allowed`,
        });
      }
      if (provider.extractFrom.cookies && provider.extractFrom.cookies.length > MAX_JWT_EXTRACTOR_LENGTH) {
        errors.push({
          field: `jwt.providers.${i}.extractFrom.cookies`,
          message: `Maximum ${MAX_JWT_EXTRACTOR_LENGTH} cookies allowed`,
        });
      }
      if (provider.extractFrom.params && provider.extractFrom.params.length > MAX_JWT_EXTRACTOR_LENGTH) {
        errors.push({
          field: `jwt.providers.${i}.extractFrom.params`,
          message: `Maximum ${MAX_JWT_EXTRACTOR_LENGTH} params allowed`,
        });
      }
    }
  });

  return { valid: errors.length === 0, errors };
}

// Validate OIDC
export function validateOIDC(oidc: OIDCFormState): ValidationResult {
  const errors: ValidationError[] = [];

  if (!oidc.provider?.issuer) {
    errors.push({ field: 'oidc.provider.issuer', message: 'Provider issuer is required' });
  }

  if (!oidc.clientID && !oidc.clientIDRef?.name) {
    errors.push({ field: 'oidc.clientID', message: 'Client ID or Client ID reference is required' });
  }

  if (!oidc.clientSecret?.name) {
    errors.push({ field: 'oidc.clientSecret.name', message: 'Client secret reference is required' });
  }

  // Namespace must not be set
  if (oidc.clientIDRef?.namespace) {
    errors.push({ field: 'oidc.clientIDRef.namespace', message: 'Namespace must not be set' });
  }
  if (oidc.clientSecret?.namespace) {
    errors.push({ field: 'oidc.clientSecret.namespace', message: 'Namespace must not be set' });
  }

  // Validate scopes
  if (oidc.scopes && oidc.scopes.length > MAX_OIDC_SCOPES) {
    errors.push({ field: 'oidc.scopes', message: `Maximum ${MAX_OIDC_SCOPES} scopes allowed` });
  }

  // Validate resources
  if (oidc.resources && oidc.resources.length > MAX_OIDC_RESOURCES) {
    errors.push({ field: 'oidc.resources', message: `Maximum ${MAX_OIDC_RESOURCES} resources allowed` });
  }

  // Validate refresh token TTL if set
  if (oidc.defaultRefreshTokenTTL) {
    const ttlResult = validateDuration(oidc.defaultRefreshTokenTTL, MIN_REFRESH_TOKEN_TTL);
    if (!ttlResult.valid) {
      errors.push({ field: 'oidc.defaultRefreshTokenTTL', message: ttlResult.errors[0]?.message || 'Invalid duration' });
    }
  }

  return { valid: errors.length === 0, errors };
}

// Validate CORS
export function validateCORS(cors: CORSFormState): ValidationResult {
  const errors: ValidationError[] = [];

  if (cors.allowOrigins && cors.allowOrigins.length > MAX_CORS_FIELD_LENGTH) {
    errors.push({ field: 'cors.allowOrigins', message: `Maximum ${MAX_CORS_FIELD_LENGTH} origins allowed` });
  }

  if (cors.allowMethods && cors.allowMethods.length > MAX_CORS_FIELD_LENGTH) {
    errors.push({ field: 'cors.allowMethods', message: `Maximum ${MAX_CORS_FIELD_LENGTH} methods allowed` });
  }

  if (cors.allowHeaders && cors.allowHeaders.length > MAX_CORS_FIELD_LENGTH) {
    errors.push({ field: 'cors.allowHeaders', message: `Maximum ${MAX_CORS_FIELD_LENGTH} headers allowed` });
  }

  if (cors.exposeHeaders && cors.exposeHeaders.length > MAX_CORS_FIELD_LENGTH) {
    errors.push({ field: 'cors.exposeHeaders', message: `Maximum ${MAX_CORS_FIELD_LENGTH} expose headers allowed` });
  }

  // Validate each origin
  cors.allowOrigins?.forEach((origin, i) => {
    if (!origin.value) {
      errors.push({ field: `cors.allowOrigins.${i}.value`, message: 'Origin value is required' });
    }
  });

  return { valid: errors.length === 0, errors };
}

// Validate Authorization
export function validateAuthorization(auth: AuthorizationFormState): ValidationResult {
  const errors: ValidationError[] = [];

  if (!auth.defaultAction) {
    errors.push({ field: 'authorization.defaultAction', message: 'Default action is required' });
  }

  if (auth.rules && auth.rules.length > MAX_AUTHORIZATION_RULES) {
    errors.push({ field: 'authorization.rules', message: `Maximum ${MAX_AUTHORIZATION_RULES} rules allowed` });
  }

  auth.rules?.forEach((rule, i) => {
    if (!rule.action) {
      errors.push({ field: `authorization.rules.${i}.action`, message: 'Rule action is required' });
    }

    // Validate clientCIDRs
    if (rule.principal?.clientCIDRs && rule.principal.clientCIDRs.length > MAX_CLIENT_CIDRS) {
      errors.push({
        field: `authorization.rules.${i}.principal.clientCIDRs`,
        message: `Maximum ${MAX_CLIENT_CIDRS} client CIDRs allowed`,
      });
    }

    rule.principal?.clientCIDRs?.forEach((cidr, j) => {
      if (!cidr.cidr) {
        errors.push({ field: `authorization.rules.${i}.principal.clientCIDRs.${j}.cidr`, message: 'CIDR is required' });
      } else if (!isValidCIDR(cidr.cidr)) {
        errors.push({ field: `authorization.rules.${i}.principal.clientCIDRs.${j}.cidr`, message: 'Invalid CIDR format' });
      }
    });
  });

  return { valid: errors.length === 0, errors };
}

// Helper to validate CIDR format
export function isValidCIDR(cidr: string): boolean {
  // Basic CIDR validation: IP/prefix
  const cidrRegex = /^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\/(3[0-2]|[12]?[0-9])$|^([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\/(12[0-8]|1[01][0-9]|[1-9]?[0-9])$/;
  return cidrRegex.test(cidr);
}

// Helper to validate duration format and minimum
export function validateDuration(duration: string, minimum?: string): ValidationResult {
  const errors: ValidationError[] = [];

  // Parse duration string (e.g., "5m", "1h", "30s")
  const durationRegex = /^(\d+)(s|m|h)$/;
  const match = duration.match(durationRegex);

  if (!match) {
    errors.push({ field: 'duration', message: 'Invalid duration format. Use format like "5m", "1h", "30s"' });
    return { valid: false, errors };
  }

  if (minimum) {
    const durationMs = parseDurationToMs(duration);
    const minimumMs = parseDurationToMs(minimum);

    if (durationMs !== null && minimumMs !== null && durationMs < minimumMs) {
      errors.push({ field: 'duration', message: `Duration must be at least ${minimum}` });
    }
  }

  return { valid: errors.length === 0, errors };
}

// Parse duration string to milliseconds
export function parseDurationToMs(duration: string): number | null {
  const match = duration.match(/^(\d+)(s|m|h)$/);
  if (!match) return null;

  const value = parseInt(match[1], 10);
  const unit = match[2];

  switch (unit) {
    case 's': return value * 1000;
    case 'm': return value * 60 * 1000;
    case 'h': return value * 60 * 60 * 1000;
    default: return null;
  }
}

// Helper to get errors for a specific field prefix
export function getFieldErrors(errors: ValidationError[], fieldPrefix: string): string[] {
  return errors
    .filter(err => err.field === fieldPrefix || err.field.startsWith(`${fieldPrefix}.`))
    .map(err => err.message);
}

// Helper to check if a field has errors
export function hasFieldError(errors: ValidationError[], fieldPrefix: string): boolean {
  return getFieldErrors(errors, fieldPrefix).length > 0;
}

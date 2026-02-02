import type {
  SecurityPolicy,
  SecurityPolicySpec,
  LocalPolicyTargetReference,
  BasicAuth,
  APIKeyAuth,
  ExtractFrom,
  JWT,
  JWTProvider,
  ClaimToHeader,
  JWTExtractFrom,
  JWTHeaderExtractor,
  RemoteJWKS,
  OIDC,
  OIDCProvider,
  CORS,
  StringMatch,
  Authorization,
  AuthorizationRule,
  Principal,
  CIDR,
  JWTPrincipal,
  JWTClaim,
  SecretObjectReference,
} from '@/api/types';

// Generate unique IDs for React keys
let idCounter = 0;
export function generateId(): string {
  return `${Date.now()}-${++idCounter}`;
}

// Form state types with IDs for React keys
export interface SecurityPolicyFormState {
  metadata: {
    name: string;
    namespace: string;
  };
  spec: SecurityPolicySpecFormState;
  validation: {
    errors: { field: string; message: string }[];
    touched: Record<string, boolean>;
  };
  ui: {
    expandedSections: Set<string>;
  };
}

export interface SecurityPolicySpecFormState {
  targetRefs: TargetRefFormState[];
  basicAuth?: BasicAuthFormState;
  apiKeyAuth?: APIKeyAuthFormState;
  jwt?: JWTFormState;
  oidc?: OIDCFormState;
  cors?: CORSFormState;
  authorization?: AuthorizationFormState;
}

export interface TargetRefFormState extends LocalPolicyTargetReference {
  id: string;
}

// BasicAuth form state
export interface BasicAuthFormState {
  users: SecretRefFormState;
  forwardUsernameHeader?: string;
}

export interface SecretRefFormState {
  id: string;
  name: string;
  namespace?: string;
}

// APIKeyAuth form state
export interface APIKeyAuthFormState {
  credentialRefs: SecretRefFormState[];
  extractFrom: ExtractFromFormState[];
  forwardClientIDHeader?: string;
}

export interface ExtractFromFormState {
  id: string;
  headers: string[];
  params: string[];
  cookies: string[];
}

// JWT form state
export interface JWTFormState {
  providers: JWTProviderFormState[];
  optional?: boolean;
}

export interface JWTProviderFormState {
  id: string;
  name: string;
  issuer?: string;
  audiences: string[];
  remoteJWKS?: RemoteJWKSFormState;
  claimToHeaders: ClaimToHeaderFormState[];
  recomputeRoute?: boolean;
  extractFrom?: JWTExtractFromFormState;
}

export interface RemoteJWKSFormState {
  uri: string;
}

export interface ClaimToHeaderFormState {
  id: string;
  claim: string;
  header: string;
}

export interface JWTExtractFromFormState {
  headers: JWTHeaderExtractorFormState[];
  cookies: string[];
  params: string[];
}

export interface JWTHeaderExtractorFormState {
  id: string;
  name: string;
  valuePrefix?: string;
}

// OIDC form state
export interface OIDCFormState {
  provider: OIDCProviderFormState;
  clientID: string;
  clientIDRef?: SecretRefFormState;
  clientSecret: SecretRefFormState;
  scopes: string[];
  resources: string[];
  redirectURL?: string;
  logoutPath?: string;
  forwardAccessToken?: boolean;
  defaultTokenTTL?: string;
  refreshToken?: boolean;
  defaultRefreshTokenTTL?: string;
  cookieDomain?: string;
}

export interface OIDCProviderFormState {
  issuer: string;
  authorizationEndpoint?: string;
  tokenEndpoint?: string;
}

// CORS form state
export interface CORSFormState {
  allowOrigins: StringMatchFormState[];
  allowMethods: string[];
  allowHeaders: string[];
  exposeHeaders: string[];
  maxAge?: string;
  allowCredentials?: boolean;
}

export interface StringMatchFormState {
  id: string;
  type: 'Exact' | 'Prefix' | 'Suffix' | 'RegularExpression';
  value: string;
}

// Authorization form state
export interface AuthorizationFormState {
  defaultAction: 'Allow' | 'Deny';
  rules: AuthorizationRuleFormState[];
}

export interface AuthorizationRuleFormState {
  id: string;
  name?: string;
  action: 'Allow' | 'Deny';
  principal: PrincipalFormState;
}

export interface PrincipalFormState {
  clientCIDRs: CIDRFormState[];
  jwt?: JWTPrincipalFormState;
}

export interface CIDRFormState {
  id: string;
  cidr: string;
}

export interface JWTPrincipalFormState {
  provider: string;
  claims: JWTClaimFormState[];
  scopes: string[];
}

export interface JWTClaimFormState {
  id: string;
  name: string;
  values: string[];
  valueType?: 'String' | 'StringArray';
}

// Factory functions for creating default objects

export function createDefaultSecurityPolicy(): SecurityPolicyFormState {
  return {
    metadata: {
      name: '',
      namespace: 'default',
    },
    spec: {
      targetRefs: [],
    },
    validation: {
      errors: [],
      touched: {},
    },
    ui: {
      expandedSections: new Set(),
    },
  };
}

export function createDefaultTargetRef(): TargetRefFormState {
  return {
    id: generateId(),
    group: 'gateway.networking.k8s.io',
    kind: 'HTTPRoute',
    name: '',
  };
}

export function createDefaultSecretRef(): SecretRefFormState {
  return {
    id: generateId(),
    name: '',
  };
}

export function createDefaultBasicAuth(): BasicAuthFormState {
  return {
    users: createDefaultSecretRef(),
  };
}

export function createDefaultAPIKeyAuth(): APIKeyAuthFormState {
  return {
    credentialRefs: [createDefaultSecretRef()],
    extractFrom: [createDefaultExtractFrom()],
  };
}

export function createDefaultExtractFrom(): ExtractFromFormState {
  return {
    id: generateId(),
    headers: [],
    params: [],
    cookies: [],
  };
}

export function createDefaultJWT(): JWTFormState {
  return {
    providers: [createDefaultJWTProvider()],
  };
}

export function createDefaultJWTProvider(): JWTProviderFormState {
  return {
    id: generateId(),
    name: '',
    audiences: [],
    claimToHeaders: [],
  };
}

export function createDefaultClaimToHeader(): ClaimToHeaderFormState {
  return {
    id: generateId(),
    claim: '',
    header: '',
  };
}

export function createDefaultJWTExtractFrom(): JWTExtractFromFormState {
  return {
    headers: [],
    cookies: [],
    params: [],
  };
}

export function createDefaultJWTHeaderExtractor(): JWTHeaderExtractorFormState {
  return {
    id: generateId(),
    name: '',
  };
}

export function createDefaultOIDC(): OIDCFormState {
  return {
    provider: {
      issuer: '',
    },
    clientID: '',
    clientSecret: createDefaultSecretRef(),
    scopes: ['openid'],
    resources: [],
  };
}

export function createDefaultCORS(): CORSFormState {
  return {
    allowOrigins: [],
    allowMethods: [],
    allowHeaders: [],
    exposeHeaders: [],
  };
}

export function createDefaultStringMatch(): StringMatchFormState {
  return {
    id: generateId(),
    type: 'Exact',
    value: '',
  };
}

export function createDefaultAuthorization(): AuthorizationFormState {
  return {
    defaultAction: 'Deny',
    rules: [],
  };
}

export function createDefaultAuthorizationRule(): AuthorizationRuleFormState {
  return {
    id: generateId(),
    action: 'Allow',
    principal: {
      clientCIDRs: [],
    },
  };
}

export function createDefaultCIDR(): CIDRFormState {
  return {
    id: generateId(),
    cidr: '',
  };
}

export function createDefaultJWTPrincipal(): JWTPrincipalFormState {
  return {
    provider: '',
    claims: [],
    scopes: [],
  };
}

export function createDefaultJWTClaim(): JWTClaimFormState {
  return {
    id: generateId(),
    name: '',
    values: [],
  };
}

// Conversion functions: Form state -> API types

export function securityPolicyFormStateToAPI(state: SecurityPolicyFormState): SecurityPolicy {
  return {
    apiVersion: 'gateway.envoyproxy.io/v1alpha1',
    kind: 'SecurityPolicy',
    metadata: {
      name: state.metadata.name,
      namespace: state.metadata.namespace,
    },
    spec: securityPolicySpecFormStateToAPI(state.spec),
  };
}

export function securityPolicySpecFormStateToAPI(spec: SecurityPolicySpecFormState): SecurityPolicySpec {
  const result: SecurityPolicySpec = {};

  if (spec.targetRefs.length > 0) {
    result.targetRefs = spec.targetRefs.map(targetRefFormStateToAPI);
  }

  if (spec.basicAuth) {
    result.basicAuth = basicAuthFormStateToAPI(spec.basicAuth);
  }

  if (spec.apiKeyAuth) {
    result.apiKeyAuth = apiKeyAuthFormStateToAPI(spec.apiKeyAuth);
  }

  if (spec.jwt) {
    result.jwt = jwtFormStateToAPI(spec.jwt);
  }

  if (spec.oidc) {
    result.oidc = oidcFormStateToAPI(spec.oidc);
  }

  if (spec.cors) {
    result.cors = corsFormStateToAPI(spec.cors);
  }

  if (spec.authorization) {
    result.authorization = authorizationFormStateToAPI(spec.authorization);
  }

  return result;
}

export function targetRefFormStateToAPI(ref: TargetRefFormState): LocalPolicyTargetReference {
  const result: LocalPolicyTargetReference = {
    group: ref.group,
    kind: ref.kind,
    name: ref.name,
  };
  if (ref.sectionName) {
    result.sectionName = ref.sectionName;
  }
  return result;
}

export function secretRefFormStateToAPI(ref: SecretRefFormState): SecretObjectReference {
  return { name: ref.name };
}

export function basicAuthFormStateToAPI(basicAuth: BasicAuthFormState): BasicAuth {
  const result: BasicAuth = {
    users: secretRefFormStateToAPI(basicAuth.users),
  };
  if (basicAuth.forwardUsernameHeader) {
    result.forwardUsernameHeader = basicAuth.forwardUsernameHeader;
  }
  return result;
}

export function apiKeyAuthFormStateToAPI(apiKeyAuth: APIKeyAuthFormState): APIKeyAuth {
  const result: APIKeyAuth = {
    credentialRefs: apiKeyAuth.credentialRefs
      .filter(r => r.name)
      .map(secretRefFormStateToAPI),
  };

  const extractFrom = apiKeyAuth.extractFrom
    .filter(e => e.headers.length > 0 || e.params.length > 0 || e.cookies.length > 0)
    .map(extractFromFormStateToAPI);

  if (extractFrom.length > 0) {
    result.extractFrom = extractFrom;
  }

  if (apiKeyAuth.forwardClientIDHeader) {
    result.forwardClientIDHeader = apiKeyAuth.forwardClientIDHeader;
  }

  return result;
}

export function extractFromFormStateToAPI(extractFrom: ExtractFromFormState): ExtractFrom {
  const result: ExtractFrom = {};
  if (extractFrom.headers.length > 0) result.headers = extractFrom.headers;
  if (extractFrom.params.length > 0) result.params = extractFrom.params;
  if (extractFrom.cookies.length > 0) result.cookies = extractFrom.cookies;
  return result;
}

export function jwtFormStateToAPI(jwt: JWTFormState): JWT {
  const result: JWT = {
    providers: jwt.providers.filter(p => p.name).map(jwtProviderFormStateToAPI),
  };
  if (jwt.optional !== undefined) {
    result.optional = jwt.optional;
  }
  return result;
}

export function jwtProviderFormStateToAPI(provider: JWTProviderFormState): JWTProvider {
  const result: JWTProvider = {
    name: provider.name,
  };

  if (provider.issuer) result.issuer = provider.issuer;
  if (provider.audiences.length > 0) result.audiences = provider.audiences;

  if (provider.remoteJWKS?.uri) {
    result.remoteJWKS = { uri: provider.remoteJWKS.uri };
  }

  const claimToHeaders = provider.claimToHeaders
    .filter(c => c.claim && c.header)
    .map(({ claim, header }) => ({ claim, header }));
  if (claimToHeaders.length > 0) {
    result.claimToHeaders = claimToHeaders;
  }

  if (provider.recomputeRoute !== undefined) {
    result.recomputeRoute = provider.recomputeRoute;
  }

  if (provider.extractFrom) {
    result.extractFrom = jwtExtractFromFormStateToAPI(provider.extractFrom);
  }

  return result;
}

export function jwtExtractFromFormStateToAPI(extractFrom: JWTExtractFromFormState): JWTExtractFrom {
  const result: JWTExtractFrom = {};

  const headers = extractFrom.headers.filter(h => h.name).map(({ name, valuePrefix }) => ({
    name,
    ...(valuePrefix ? { valuePrefix } : {}),
  }));
  if (headers.length > 0) result.headers = headers;

  if (extractFrom.cookies.length > 0) result.cookies = extractFrom.cookies;
  if (extractFrom.params.length > 0) result.params = extractFrom.params;

  return result;
}

export function oidcFormStateToAPI(oidc: OIDCFormState): OIDC {
  const result: OIDC = {
    provider: {
      issuer: oidc.provider.issuer,
    },
    clientID: oidc.clientID,
    clientSecret: secretRefFormStateToAPI(oidc.clientSecret),
  };

  if (oidc.provider.authorizationEndpoint) {
    result.provider.authorizationEndpoint = oidc.provider.authorizationEndpoint;
  }
  if (oidc.provider.tokenEndpoint) {
    result.provider.tokenEndpoint = oidc.provider.tokenEndpoint;
  }

  if (oidc.clientIDRef?.name) {
    result.clientIDRef = secretRefFormStateToAPI(oidc.clientIDRef);
  }

  if (oidc.scopes.length > 0) result.scopes = oidc.scopes;
  if (oidc.resources.length > 0) result.resources = oidc.resources;
  if (oidc.redirectURL) result.redirectURL = oidc.redirectURL;
  if (oidc.logoutPath) result.logoutPath = oidc.logoutPath;
  if (oidc.forwardAccessToken !== undefined) result.forwardAccessToken = oidc.forwardAccessToken;
  if (oidc.defaultTokenTTL) result.defaultTokenTTL = oidc.defaultTokenTTL;
  if (oidc.refreshToken !== undefined) result.refreshToken = oidc.refreshToken;
  if (oidc.defaultRefreshTokenTTL) result.defaultRefreshTokenTTL = oidc.defaultRefreshTokenTTL;
  if (oidc.cookieDomain) result.cookieDomain = oidc.cookieDomain;

  return result;
}

export function corsFormStateToAPI(cors: CORSFormState): CORS {
  const result: CORS = {};

  const allowOrigins = cors.allowOrigins
    .filter(o => o.value)
    .map(({ type, value }) => ({ type, value }));
  if (allowOrigins.length > 0) result.allowOrigins = allowOrigins;

  if (cors.allowMethods.length > 0) result.allowMethods = cors.allowMethods;
  if (cors.allowHeaders.length > 0) result.allowHeaders = cors.allowHeaders;
  if (cors.exposeHeaders.length > 0) result.exposeHeaders = cors.exposeHeaders;
  if (cors.maxAge) result.maxAge = cors.maxAge;
  if (cors.allowCredentials !== undefined) result.allowCredentials = cors.allowCredentials;

  return result;
}

export function authorizationFormStateToAPI(auth: AuthorizationFormState): Authorization {
  const result: Authorization = {
    defaultAction: auth.defaultAction,
  };

  const rules = auth.rules.map(authorizationRuleFormStateToAPI);
  if (rules.length > 0) {
    result.rules = rules;
  }

  return result;
}

export function authorizationRuleFormStateToAPI(rule: AuthorizationRuleFormState): AuthorizationRule {
  const result: AuthorizationRule = {
    action: rule.action,
    principal: principalFormStateToAPI(rule.principal),
  };

  if (rule.name) {
    result.name = rule.name;
  }

  return result;
}

export function principalFormStateToAPI(principal: PrincipalFormState): Principal {
  const result: Principal = {};

  const clientCIDRs = principal.clientCIDRs
    .filter(c => c.cidr)
    .map(({ cidr }) => ({ cidr }));
  if (clientCIDRs.length > 0) {
    result.clientCIDRs = clientCIDRs;
  }

  if (principal.jwt) {
    result.jwt = jwtPrincipalFormStateToAPI(principal.jwt);
  }

  return result;
}

export function jwtPrincipalFormStateToAPI(jwt: JWTPrincipalFormState): JWTPrincipal {
  const result: JWTPrincipal = {
    provider: jwt.provider,
  };

  const claims = jwt.claims
    .filter(c => c.name)
    .map(({ name, values, valueType }) => ({
      name,
      ...(values.length > 0 ? { values } : {}),
      ...(valueType ? { valueType } : {}),
    }));
  if (claims.length > 0) {
    result.claims = claims;
  }

  if (jwt.scopes.length > 0) {
    result.scopes = jwt.scopes;
  }

  return result;
}

// Conversion functions: API types -> Form state

export function securityPolicyAPIToFormState(policy: SecurityPolicy): SecurityPolicyFormState {
  return {
    metadata: {
      name: policy.metadata.name,
      namespace: policy.metadata.namespace || 'default',
    },
    spec: securityPolicySpecAPIToFormState(policy.spec),
    validation: {
      errors: [],
      touched: {},
    },
    ui: {
      expandedSections: new Set(),
    },
  };
}

export function securityPolicySpecAPIToFormState(spec: SecurityPolicySpec): SecurityPolicySpecFormState {
  const result: SecurityPolicySpecFormState = {
    targetRefs: spec.targetRefs?.map(targetRefAPIToFormState) || [],
  };

  if (spec.basicAuth) {
    result.basicAuth = basicAuthAPIToFormState(spec.basicAuth);
  }

  if (spec.apiKeyAuth) {
    result.apiKeyAuth = apiKeyAuthAPIToFormState(spec.apiKeyAuth);
  }

  if (spec.jwt) {
    result.jwt = jwtAPIToFormState(spec.jwt);
  }

  if (spec.oidc) {
    result.oidc = oidcAPIToFormState(spec.oidc);
  }

  if (spec.cors) {
    result.cors = corsAPIToFormState(spec.cors);
  }

  if (spec.authorization) {
    result.authorization = authorizationAPIToFormState(spec.authorization);
  }

  return result;
}

export function targetRefAPIToFormState(ref: LocalPolicyTargetReference): TargetRefFormState {
  return {
    id: generateId(),
    ...ref,
  };
}

export function secretRefAPIToFormState(ref: SecretObjectReference): SecretRefFormState {
  return {
    id: generateId(),
    name: ref.name,
    namespace: ref.namespace,
  };
}

export function basicAuthAPIToFormState(basicAuth: BasicAuth): BasicAuthFormState {
  return {
    users: secretRefAPIToFormState(basicAuth.users),
    forwardUsernameHeader: basicAuth.forwardUsernameHeader,
  };
}

export function apiKeyAuthAPIToFormState(apiKeyAuth: APIKeyAuth): APIKeyAuthFormState {
  return {
    credentialRefs: apiKeyAuth.credentialRefs.map(secretRefAPIToFormState),
    extractFrom: apiKeyAuth.extractFrom?.map(extractFromAPIToFormState) || [createDefaultExtractFrom()],
    forwardClientIDHeader: apiKeyAuth.forwardClientIDHeader,
  };
}

export function extractFromAPIToFormState(extractFrom: ExtractFrom): ExtractFromFormState {
  return {
    id: generateId(),
    headers: extractFrom.headers || [],
    params: extractFrom.params || [],
    cookies: extractFrom.cookies || [],
  };
}

export function jwtAPIToFormState(jwt: JWT): JWTFormState {
  return {
    providers: jwt.providers.map(jwtProviderAPIToFormState),
    optional: jwt.optional,
  };
}

export function jwtProviderAPIToFormState(provider: JWTProvider): JWTProviderFormState {
  return {
    id: generateId(),
    name: provider.name,
    issuer: provider.issuer,
    audiences: provider.audiences || [],
    remoteJWKS: provider.remoteJWKS ? { uri: provider.remoteJWKS.uri } : undefined,
    claimToHeaders: provider.claimToHeaders?.map(c => ({
      id: generateId(),
      claim: c.claim,
      header: c.header,
    })) || [],
    recomputeRoute: provider.recomputeRoute,
    extractFrom: provider.extractFrom ? jwtExtractFromAPIToFormState(provider.extractFrom) : undefined,
  };
}

export function jwtExtractFromAPIToFormState(extractFrom: JWTExtractFrom): JWTExtractFromFormState {
  return {
    headers: extractFrom.headers?.map(h => ({
      id: generateId(),
      name: h.name,
      valuePrefix: h.valuePrefix,
    })) || [],
    cookies: extractFrom.cookies || [],
    params: extractFrom.params || [],
  };
}

export function oidcAPIToFormState(oidc: OIDC): OIDCFormState {
  return {
    provider: {
      issuer: oidc.provider.issuer,
      authorizationEndpoint: oidc.provider.authorizationEndpoint,
      tokenEndpoint: oidc.provider.tokenEndpoint,
    },
    clientID: oidc.clientID,
    clientIDRef: oidc.clientIDRef ? secretRefAPIToFormState(oidc.clientIDRef) : undefined,
    clientSecret: secretRefAPIToFormState(oidc.clientSecret),
    scopes: oidc.scopes || [],
    resources: oidc.resources || [],
    redirectURL: oidc.redirectURL,
    logoutPath: oidc.logoutPath,
    forwardAccessToken: oidc.forwardAccessToken,
    defaultTokenTTL: oidc.defaultTokenTTL,
    refreshToken: oidc.refreshToken,
    defaultRefreshTokenTTL: oidc.defaultRefreshTokenTTL,
    cookieDomain: oidc.cookieDomain,
  };
}

export function corsAPIToFormState(cors: CORS): CORSFormState {
  return {
    allowOrigins: cors.allowOrigins?.map(o => ({
      id: generateId(),
      type: o.type,
      value: o.value,
    })) || [],
    allowMethods: cors.allowMethods || [],
    allowHeaders: cors.allowHeaders || [],
    exposeHeaders: cors.exposeHeaders || [],
    maxAge: cors.maxAge,
    allowCredentials: cors.allowCredentials,
  };
}

export function authorizationAPIToFormState(auth: Authorization): AuthorizationFormState {
  return {
    defaultAction: auth.defaultAction,
    rules: auth.rules?.map(authorizationRuleAPIToFormState) || [],
  };
}

export function authorizationRuleAPIToFormState(rule: AuthorizationRule): AuthorizationRuleFormState {
  return {
    id: generateId(),
    name: rule.name,
    action: rule.action,
    principal: principalAPIToFormState(rule.principal),
  };
}

export function principalAPIToFormState(principal: Principal): PrincipalFormState {
  return {
    clientCIDRs: principal.clientCIDRs?.map(c => ({
      id: generateId(),
      cidr: c.cidr,
    })) || [],
    jwt: principal.jwt ? jwtPrincipalAPIToFormState(principal.jwt) : undefined,
  };
}

export function jwtPrincipalAPIToFormState(jwt: JWTPrincipal): JWTPrincipalFormState {
  return {
    provider: jwt.provider,
    claims: jwt.claims?.map(c => ({
      id: generateId(),
      name: c.name,
      values: c.values || [],
      valueType: c.valueType,
    })) || [],
    scopes: jwt.scopes || [],
  };
}

// Auth type display names
export const AUTH_TYPE_LABELS: Record<string, string> = {
  basicAuth: 'Basic Auth',
  apiKeyAuth: 'API Key Auth',
  jwt: 'JWT',
  oidc: 'OIDC',
  cors: 'CORS',
  authorization: 'Authorization',
};

export const AUTH_TYPE_DESCRIPTIONS: Record<string, string> = {
  basicAuth: 'Username/password authentication via htpasswd secret',
  apiKeyAuth: 'API key authentication from headers, parameters, or cookies',
  jwt: 'JSON Web Token validation with configurable providers',
  oidc: 'OpenID Connect authentication with OAuth flows',
  cors: 'Cross-Origin Resource Sharing configuration',
  authorization: 'Allow/Deny rules based on client CIDRs and JWT claims',
};

// Target ref kinds
export const TARGET_REF_KINDS = [
  { value: 'Gateway', label: 'Gateway' },
  { value: 'HTTPRoute', label: 'HTTPRoute' },
] as const;

// HTTP Methods for CORS
export const CORS_METHODS = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'] as const;

// String match types for CORS origins
export const STRING_MATCH_TYPES = [
  { value: 'Exact', label: 'Exact' },
  { value: 'Prefix', label: 'Prefix' },
  { value: 'Suffix', label: 'Suffix' },
  { value: 'RegularExpression', label: 'Regex' },
] as const;

// Authorization actions
export const AUTHORIZATION_ACTIONS = ['Allow', 'Deny'] as const;

// Kubernetes metadata types
export interface ObjectMeta {
  name: string;
  namespace?: string;
  uid?: string;
  resourceVersion?: string;
  creationTimestamp?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

export interface Condition {
  type: string;
  status: 'True' | 'False' | 'Unknown';
  lastTransitionTime?: string;
  reason?: string;
  message?: string;
  observedGeneration?: number;
}

export interface TypeMeta {
  apiVersion: string;
  kind: string;
}

// HTTPProxy types (networking.datumapis.com/v1alpha)
export interface HTTPProxy {
  apiVersion: 'networking.datumapis.com/v1alpha';
  kind: 'HTTPProxy';
  metadata: ObjectMeta;
  spec: HTTPProxySpec;
  status?: HTTPProxyStatus;
}

export interface HTTPProxySpec {
  hostnames: string[];
  rules: HTTPRule[];
}

export interface HTTPRule {
  name?: string;
  matches?: HTTPMatch[];
  filters?: HTTPFilter[];
  backends: HTTPBackend[];
}

export interface HTTPMatch {
  path?: HTTPPathMatch;
  headers?: HTTPHeaderMatch[];
  queryParams?: HTTPQueryParamMatch[];
  method?: string;
}

export interface HTTPPathMatch {
  type: 'Exact' | 'PathPrefix' | 'RegularExpression';
  value: string;
}

export interface HTTPHeaderMatch {
  type: 'Exact' | 'RegularExpression';
  name: string;
  value: string;
}

export interface HTTPQueryParamMatch {
  type: 'Exact' | 'RegularExpression';
  name: string;
  value: string;
}

export interface HTTPFilter {
  type: 'RequestHeaderModifier' | 'ResponseHeaderModifier' | 'URLRewrite' | 'RequestRedirect';
  requestHeaderModifier?: HTTPHeaderModifier;
  responseHeaderModifier?: HTTPHeaderModifier;
  urlRewrite?: URLRewrite;
  requestRedirect?: RequestRedirect;
}

export interface HTTPHeaderModifier {
  set?: HTTPHeader[];
  add?: HTTPHeader[];
  remove?: string[];
}

export interface HTTPHeader {
  name: string;
  value: string;
}

export interface URLRewrite {
  hostname?: string;
  path?: HTTPPathMatch;
}

export interface RequestRedirect {
  scheme?: string;
  hostname?: string;
  path?: HTTPPathMatch;
  port?: number;
  statusCode?: number;
}

export interface HTTPBackend {
  endpoint?: string;
  connectorRef?: ConnectorRef;
  weight?: number;
}

export interface ConnectorRef {
  name: string;
  namespace?: string;
}

export interface HTTPProxyStatus {
  hostnames?: string[];
  addresses?: GatewayAddress[];
  conditions?: Condition[];
}

export interface GatewayAddress {
  type: 'IPAddress' | 'Hostname';
  value: string;
}

// Domain types (networking.datumapis.com/v1alpha)
export interface Domain {
  apiVersion: 'networking.datumapis.com/v1alpha';
  kind: 'Domain';
  metadata: ObjectMeta;
  spec: DomainSpec;
  status?: DomainStatus;
}

export interface DomainSpec {
  domainName: string;
}

export interface DomainStatus {
  verification?: DomainVerification;
  registration?: DomainRegistration;
  conditions?: Condition[];
}

export interface DomainVerification {
  dns?: DNSVerification;
  http?: HTTPVerification;
}

export interface DNSVerification {
  recordType: string;
  recordName: string;
  recordValue: string;
}

export interface HTTPVerification {
  path: string;
  content: string;
}

export interface DomainRegistration {
  registrar?: string;
  creationDate?: string;
  expirationDate?: string;
  nameServers?: string[];
}

// TrafficProtectionPolicy types (networking.datumapis.com/v1alpha)
export interface TrafficProtectionPolicy {
  apiVersion: 'networking.datumapis.com/v1alpha';
  kind: 'TrafficProtectionPolicy';
  metadata: ObjectMeta;
  spec: TrafficProtectionPolicySpec;
  status?: TrafficProtectionPolicyStatus;
}

export interface TrafficProtectionPolicySpec {
  targetRefs: PolicyTargetRef[];
  mode: 'Observe' | 'Enforce' | 'Disabled';
  samplingPercentage?: number;
  ruleSets?: RuleSetConfig[];
}

export interface PolicyTargetRef {
  group: string;
  kind: string;
  name: string;
  namespace?: string;
}

export interface RuleSetConfig {
  name: string;
  paranoiaLevel?: 1 | 2 | 3 | 4;
  scoreThreshold?: number;
  ruleExclusions?: RuleExclusion[];
}

export interface RuleExclusion {
  ruleId: string;
  reason?: string;
}

export interface TrafficProtectionPolicyStatus {
  conditions?: Condition[];
}

// Connector types (networking.datumapis.com/v1alpha1)
export interface Connector {
  apiVersion: 'networking.datumapis.com/v1alpha1';
  kind: 'Connector';
  metadata: ObjectMeta;
  spec: ConnectorSpec;
  status?: ConnectorStatus;
}

export interface ConnectorSpec {
  connectorClassName: string;
  capabilities: ConnectorCapability[];
}

export type ConnectorCapability = 'ConnectTCP' | 'ConnectUDP' | 'ConnectHTTP';

export interface ConnectorStatus {
  connectionDetails?: ConnectionDetails;
  capabilities?: ConnectorCapability[];
  conditions?: Condition[];
}

export interface ConnectionDetails {
  endpoint?: string;
  lastConnected?: string;
  clientId?: string;
}

// ConnectorAdvertisement types (networking.datumapis.com/v1alpha1)
export interface ConnectorAdvertisement {
  apiVersion: 'networking.datumapis.com/v1alpha1';
  kind: 'ConnectorAdvertisement';
  metadata: ObjectMeta;
  spec: ConnectorAdvertisementSpec;
  status?: ConnectorAdvertisementStatus;
}

export interface ConnectorAdvertisementSpec {
  connectorRef: ConnectorRef;
  layer4?: Layer4Service[];
}

export interface Layer4Service {
  name: string;
  address: string;
  ports: ServicePort[];
}

export interface ServicePort {
  port: number;
  protocol: 'TCP' | 'UDP';
  targetPort?: number;
}

export interface ConnectorAdvertisementStatus {
  conditions?: Condition[];
}

// List types for API responses
export interface ResourceList<T> {
  apiVersion: string;
  kind: string;
  metadata: {
    continue?: string;
    resourceVersion?: string;
  };
  items: T[];
}

// API Error type
export interface ApiError {
  code: number;
  message: string;
  details?: string;
}

// Dashboard stats types
export interface DashboardStats {
  gateways: {
    total: number;
    healthy: number;
    unhealthy: number;
  };
  domains: {
    total: number;
    verified: number;
    pending: number;
  };
  policies: {
    total: number;
    enforcing: number;
    observing: number;
    disabled: number;
  };
  connectors: {
    total: number;
    connected: number;
    disconnected: number;
  };
}

export interface RecentActivity {
  id: string;
  type: 'gateway' | 'domain' | 'policy' | 'connector';
  action: 'created' | 'updated' | 'deleted';
  resourceName: string;
  timestamp: string;
  user?: string;
}

// Gateway API types (gateway.networking.k8s.io/v1)
export interface Gateway {
  apiVersion: 'gateway.networking.k8s.io/v1';
  kind: 'Gateway';
  metadata: ObjectMeta;
  spec: GatewaySpec;
  status?: GatewayStatus;
}

export interface GatewaySpec {
  gatewayClassName: string;
  listeners: GatewayListener[];
  addresses?: GatewaySpecAddress[];
}

export interface GatewayListener {
  name: string;
  hostname?: string;
  port: number;
  protocol: 'HTTP' | 'HTTPS' | 'TLS' | 'TCP' | 'UDP';
  tls?: GatewayTLSConfig;
  allowedRoutes?: AllowedRoutes;
}

export interface GatewayTLSConfig {
  mode?: 'Terminate' | 'Passthrough';
  certificateRefs?: SecretObjectReference[];
}

export interface SecretObjectReference {
  group?: string;
  kind?: string;
  name: string;
  namespace?: string;
}

export interface AllowedRoutes {
  namespaces?: {
    from?: 'All' | 'Same' | 'Selector';
    selector?: {
      matchLabels?: Record<string, string>;
    };
  };
  kinds?: { group?: string; kind: string }[];
}

export interface GatewaySpecAddress {
  type?: 'IPAddress' | 'Hostname' | 'NamedAddress';
  value: string;
}

export interface GatewayStatus {
  addresses?: GatewayAddress[];
  conditions?: Condition[];
  listeners?: ListenerStatus[];
}

export interface ListenerStatus {
  name: string;
  attachedRoutes: number;
  supportedKinds?: { group?: string; kind: string }[];
  conditions?: Condition[];
}

// HTTPRoute types (gateway.networking.k8s.io/v1)
export interface HTTPRoute {
  apiVersion: 'gateway.networking.k8s.io/v1';
  kind: 'HTTPRoute';
  metadata: ObjectMeta;
  spec: HTTPRouteSpec;
  status?: HTTPRouteStatus;
}

export interface HTTPRouteSpec {
  parentRefs?: ParentReference[];
  hostnames?: string[];
  rules?: HTTPRouteRule[];
}

export interface ParentReference {
  group?: string;
  kind?: string;
  namespace?: string;
  name: string;
  sectionName?: string;
  port?: number;
}

export interface HTTPRouteRule {
  matches?: HTTPRouteMatch[];
  filters?: HTTPRouteFilter[];
  backendRefs?: HTTPBackendRef[];
}

export interface HTTPRouteMatch {
  path?: HTTPPathMatch;
  headers?: HTTPHeaderMatch[];
  queryParams?: HTTPQueryParamMatch[];
  method?: string;
}

export interface HTTPRouteFilter {
  type: string;
  requestHeaderModifier?: HTTPHeaderModifier;
  responseHeaderModifier?: HTTPHeaderModifier;
  requestRedirect?: RequestRedirect;
  urlRewrite?: URLRewrite;
  extensionRef?: {
    group: string;
    kind: string;
    name: string;
  };
}

export interface HTTPBackendRef {
  group?: string;
  kind?: string;
  name: string;
  namespace?: string;
  port?: number;
  weight?: number;
}

export interface HTTPRouteStatus {
  parents?: RouteParentStatus[];
}

export interface RouteParentStatus {
  parentRef: ParentReference;
  controllerName: string;
  conditions?: Condition[];
}

// EndpointSlice types (discovery.k8s.io/v1)
export interface EndpointSlice {
  apiVersion: 'discovery.k8s.io/v1';
  kind: 'EndpointSlice';
  metadata: ObjectMeta & {
    ownerReferences?: OwnerReference[];
  };
  addressType: 'IPv4' | 'IPv6' | 'FQDN';
  endpoints: Endpoint[];
  ports?: EndpointPort[];
}

export interface OwnerReference {
  apiVersion: string;
  kind: string;
  name: string;
  uid: string;
  controller?: boolean;
  blockOwnerDeletion?: boolean;
}

export interface Endpoint {
  addresses: string[];
  conditions?: EndpointConditions;
  hostname?: string;
  nodeName?: string;
  zone?: string;
  targetRef?: {
    kind: string;
    name: string;
    namespace?: string;
    uid?: string;
  };
}

export interface EndpointConditions {
  ready?: boolean;
  serving?: boolean;
  terminating?: boolean;
}

export interface EndpointPort {
  name?: string;
  protocol?: 'TCP' | 'UDP' | 'SCTP';
  port?: number;
  appProtocol?: string;
}

// Related resources container type
export interface HTTPProxyRelatedResources {
  gateway: Gateway | null;
  httpRoute: HTTPRoute | null;
  endpointSlices: EndpointSlice[];
  domains: Domain[];
}

// Convenience type aliases for lists
export type HTTPProxyList = ResourceList<HTTPProxy>;
export type DomainList = ResourceList<Domain>;
export type TrafficProtectionPolicyList = ResourceList<TrafficProtectionPolicy>;
export type ConnectorList = ResourceList<Connector>;
export type ConnectorAdvertisementList = ResourceList<ConnectorAdvertisement>;
export type GatewayList = ResourceList<Gateway>;
export type HTTPRouteList = ResourceList<HTTPRoute>;
export type EndpointSliceList = ResourceList<EndpointSlice>;

// SecurityPolicy types (gateway.envoyproxy.io/v1alpha1)
export interface SecurityPolicy {
  apiVersion: 'gateway.envoyproxy.io/v1alpha1';
  kind: 'SecurityPolicy';
  metadata: ObjectMeta;
  spec: SecurityPolicySpec;
  status?: SecurityPolicyStatus;
}

export interface SecurityPolicySpec {
  targetRefs?: LocalPolicyTargetReference[];
  targetSelectors?: TargetSelector[];
  basicAuth?: BasicAuth;
  apiKeyAuth?: APIKeyAuth;
  jwt?: JWT;
  oidc?: OIDC;
  cors?: CORS;
  authorization?: Authorization;
}

export interface LocalPolicyTargetReference {
  group: string;
  kind: string;
  name: string;
  sectionName?: string;
}

export interface TargetSelector {
  group?: string;
  kind: string;
  matchLabels?: Record<string, string>;
}

export interface SecurityPolicyStatus {
  conditions?: Condition[];
  ancestors?: PolicyAncestorStatus[];
}

export interface PolicyAncestorStatus {
  ancestorRef: ParentReference;
  controllerName: string;
  conditions?: Condition[];
}

// BasicAuth types
export interface BasicAuth {
  users: SecretObjectReference;
  forwardUsernameHeader?: string;
}

// APIKeyAuth types
export interface APIKeyAuth {
  credentialRefs: SecretObjectReference[];
  extractFrom?: ExtractFrom[];
  forwardClientIDHeader?: string;
}

export interface ExtractFrom {
  headers?: string[];
  params?: string[];
  cookies?: string[];
}

// JWT types
export interface JWT {
  providers: JWTProvider[];
  optional?: boolean;
}

export interface JWTProvider {
  name: string;
  issuer?: string;
  audiences?: string[];
  remoteJWKS?: RemoteJWKS;
  claimToHeaders?: ClaimToHeader[];
  recomputeRoute?: boolean;
  extractFrom?: JWTExtractFrom;
}

export interface RemoteJWKS {
  uri: string;
  backendRefs?: BackendRef[];
  backendSettings?: BackendSettings;
}

export interface BackendRef {
  group?: string;
  kind?: string;
  name: string;
  namespace?: string;
  port?: number;
}

export interface BackendSettings {
  // Simplified - can be expanded as needed
  retry?: RetrySettings;
}

export interface RetrySettings {
  numRetries?: number;
}

export interface ClaimToHeader {
  claim: string;
  header: string;
}

export interface JWTExtractFrom {
  headers?: JWTHeaderExtractor[];
  cookies?: string[];
  params?: string[];
}

export interface JWTHeaderExtractor {
  name: string;
  valuePrefix?: string;
}

// OIDC types
export interface OIDC {
  provider: OIDCProvider;
  clientID: string;
  clientIDRef?: SecretObjectReference;
  clientSecret: SecretObjectReference;
  scopes?: string[];
  resources?: string[];
  redirectURL?: string;
  logoutPath?: string;
  forwardAccessToken?: boolean;
  defaultTokenTTL?: string;
  refreshToken?: boolean;
  defaultRefreshTokenTTL?: string;
  cookieNames?: OIDCCookieNames;
  cookieDomain?: string;
}

export interface OIDCProvider {
  issuer: string;
  authorizationEndpoint?: string;
  tokenEndpoint?: string;
  backendRefs?: BackendRef[];
  backendSettings?: BackendSettings;
}

export interface OIDCCookieNames {
  accessToken?: string;
  idToken?: string;
}

// CORS types
export interface CORS {
  allowOrigins?: StringMatch[];
  allowMethods?: string[];
  allowHeaders?: string[];
  exposeHeaders?: string[];
  maxAge?: string;
  allowCredentials?: boolean;
}

export interface StringMatch {
  type: 'Exact' | 'Prefix' | 'Suffix' | 'RegularExpression';
  value: string;
}

// Authorization types
export interface Authorization {
  defaultAction: AuthorizationAction;
  rules?: AuthorizationRule[];
}

export type AuthorizationAction = 'Allow' | 'Deny';

export interface AuthorizationRule {
  name?: string;
  action: AuthorizationAction;
  principal: Principal;
}

export interface Principal {
  clientCIDRs?: CIDR[];
  jwt?: JWTPrincipal;
}

export interface CIDR {
  cidr: string;
}

export interface JWTPrincipal {
  provider: string;
  claims?: JWTClaim[];
  scopes?: string[];
}

export interface JWTClaim {
  name: string;
  values?: string[];
  valueType?: 'String' | 'StringArray';
}

export type SecurityPolicyList = ResourceList<SecurityPolicy>;

// Proxy testing types
export type HTTPMethod = 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH' | 'HEAD' | 'OPTIONS';

export interface TestProxyRequest {
  method: HTTPMethod;
  path: string;
  headers?: Record<string, string>;
  body?: string;
}

export interface TestProxyResponse {
  statusCode: number;
  statusText: string;
  headers: Record<string, string>;
  body: string;
  latencyMs: number;
  timestamp: string;
  error?: string;
}

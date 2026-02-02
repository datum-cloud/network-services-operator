import {
  HTTPProxy,
  Domain,
  TrafficProtectionPolicy,
  Connector,
  ConnectorAdvertisement,
  ResourceList,
  DashboardStats,
  RecentActivity,
  HTTPProxyRelatedResources,
  TestProxyRequest,
  TestProxyResponse,
  SecurityPolicy,
} from './types';

// Check if we're in development mode with mock data
// Set NEXT_PUBLIC_USE_MOCK_DATA=true to use mock data in development
const isDev = typeof window !== 'undefined' &&
  (process.env.NODE_ENV === 'development' && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true');

// Simulated network delay for mock data
const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

// Mock data for local development
const mockHTTPProxies: HTTPProxy[] = [
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'HTTPProxy',
    metadata: {
      name: 'api-gateway',
      namespace: 'production',
      uid: 'uuid-1',
      creationTimestamp: '2024-01-15T10:30:00Z',
      labels: { app: 'api', environment: 'production' },
    },
    spec: {
      hostnames: ['api.example.com', 'api.example.io'],
      rules: [
        {
          matches: [
            {
              path: { type: 'PathPrefix', value: '/v1' },
              method: 'GET',
            },
          ],
          filters: [
            {
              type: 'RequestHeaderModifier',
              requestHeaderModifier: {
                add: [{ name: 'X-Gateway-Version', value: 'v1' }],
              },
            },
          ],
          backends: [
            {
              endpoint: 'http://api-v1-service:8080',
              weight: 100,
            },
          ],
        },
        {
          matches: [
            {
              path: { type: 'PathPrefix', value: '/v2' },
            },
          ],
          backends: [
            {
              endpoint: 'http://api-v2-service:8080',
              connectorRef: { name: 'edge-connector-us-west' },
              weight: 80,
            },
            {
              endpoint: 'http://api-v2-service-backup:8080',
              weight: 20,
            },
          ],
        },
      ],
    },
    status: {
      hostnames: ['api.example.com', 'api.example.io'],
      addresses: [
        { type: 'IPAddress', value: '203.0.113.50' },
        { type: 'Hostname', value: 'lb.example.com' },
      ],
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-15T10:31:00Z',
          reason: 'Accepted',
          message: 'HTTPProxy has been accepted',
        },
        {
          type: 'Programmed',
          status: 'True',
          lastTransitionTime: '2024-01-15T10:31:30Z',
          reason: 'Programmed',
          message: 'Configuration applied successfully',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'HTTPProxy',
    metadata: {
      name: 'web-frontend',
      namespace: 'production',
      uid: 'uuid-2',
      creationTimestamp: '2024-01-10T08:00:00Z',
      labels: { app: 'web', environment: 'production' },
    },
    spec: {
      hostnames: ['www.example.com', 'example.com'],
      rules: [
        {
          matches: [
            {
              path: { type: 'PathPrefix', value: '/' },
            },
          ],
          filters: [
            {
              type: 'RequestRedirect',
              requestRedirect: {
                scheme: 'https',
                statusCode: 301,
              },
            },
          ],
          backends: [
            {
              endpoint: 'http://frontend-service:3000',
            },
          ],
        },
      ],
    },
    status: {
      hostnames: ['www.example.com', 'example.com'],
      addresses: [{ type: 'IPAddress', value: '203.0.113.51' }],
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-10T08:01:00Z',
          reason: 'Accepted',
          message: 'HTTPProxy has been accepted',
        },
        {
          type: 'Programmed',
          status: 'True',
          lastTransitionTime: '2024-01-10T08:01:30Z',
          reason: 'Programmed',
          message: 'Configuration applied successfully',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'HTTPProxy',
    metadata: {
      name: 'staging-api',
      namespace: 'staging',
      uid: 'uuid-3',
      creationTimestamp: '2024-01-18T14:00:00Z',
      labels: { app: 'api', environment: 'staging' },
    },
    spec: {
      hostnames: ['staging-api.example.com'],
      rules: [
        {
          backends: [
            {
              endpoint: 'http://staging-api-service:8080',
            },
          ],
        },
      ],
    },
    status: {
      hostnames: ['staging-api.example.com'],
      addresses: [{ type: 'IPAddress', value: '203.0.113.100' }],
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-18T14:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Programmed',
          status: 'False',
          lastTransitionTime: '2024-01-18T14:01:30Z',
          reason: 'BackendNotFound',
          message: 'Backend service staging-api-service not found',
        },
      ],
    },
  },
];

const mockDomains: Domain[] = [
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'Domain',
    metadata: {
      name: 'example-com',
      namespace: 'production',
      uid: 'domain-uuid-1',
      creationTimestamp: '2024-01-05T09:00:00Z',
    },
    spec: {
      domainName: 'example.com',
    },
    status: {
      verification: {
        dns: {
          recordType: 'TXT',
          recordName: '_datum-verify.example.com',
          recordValue: 'datum-verification=abc123def456',
        },
        http: {
          path: '/.well-known/datum-verify.txt',
          content: 'datum-verification=abc123def456',
        },
      },
      registration: {
        registrar: 'Cloudflare, Inc.',
        creationDate: '2010-03-15T00:00:00Z',
        expirationDate: '2025-03-15T00:00:00Z',
        nameServers: ['ns1.cloudflare.com', 'ns2.cloudflare.com'],
      },
      conditions: [
        {
          type: 'Verified',
          status: 'True',
          lastTransitionTime: '2024-01-05T09:10:00Z',
          reason: 'DNSVerified',
          message: 'Domain verified via DNS TXT record',
        },
        {
          type: 'ValidDomain',
          status: 'True',
          lastTransitionTime: '2024-01-05T09:05:00Z',
          reason: 'ValidFQDN',
        },
        {
          type: 'VerifiedDNS',
          status: 'True',
          lastTransitionTime: '2024-01-05T09:10:00Z',
          reason: 'RecordFound',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'Domain',
    metadata: {
      name: 'api-example-io',
      namespace: 'production',
      uid: 'domain-uuid-2',
      creationTimestamp: '2024-01-12T11:00:00Z',
    },
    spec: {
      domainName: 'api.example.io',
    },
    status: {
      verification: {
        dns: {
          recordType: 'TXT',
          recordName: '_datum-verify.api.example.io',
          recordValue: 'datum-verification=xyz789ghi012',
        },
      },
      conditions: [
        {
          type: 'Verified',
          status: 'False',
          lastTransitionTime: '2024-01-12T11:05:00Z',
          reason: 'VerificationPending',
          message: 'Waiting for DNS verification',
        },
        {
          type: 'ValidDomain',
          status: 'True',
          lastTransitionTime: '2024-01-12T11:01:00Z',
          reason: 'ValidFQDN',
        },
        {
          type: 'VerifiedDNS',
          status: 'False',
          lastTransitionTime: '2024-01-12T11:05:00Z',
          reason: 'RecordNotFound',
          message: 'DNS TXT record not found',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'Domain',
    metadata: {
      name: 'staging-example',
      namespace: 'staging',
      uid: 'domain-uuid-3',
      creationTimestamp: '2024-01-16T16:00:00Z',
    },
    spec: {
      domainName: 'staging.example.com',
    },
    status: {
      verification: {
        dns: {
          recordType: 'TXT',
          recordName: '_datum-verify.staging.example.com',
          recordValue: 'datum-verification=stg456mno789',
        },
      },
      registration: {
        registrar: 'Cloudflare, Inc.',
        creationDate: '2010-03-15T00:00:00Z',
        expirationDate: '2025-03-15T00:00:00Z',
        nameServers: ['ns1.cloudflare.com', 'ns2.cloudflare.com'],
      },
      conditions: [
        {
          type: 'Verified',
          status: 'True',
          lastTransitionTime: '2024-01-16T16:15:00Z',
          reason: 'DNSVerified',
        },
        {
          type: 'ValidDomain',
          status: 'True',
          lastTransitionTime: '2024-01-16T16:01:00Z',
          reason: 'ValidFQDN',
        },
        {
          type: 'VerifiedDNS',
          status: 'True',
          lastTransitionTime: '2024-01-16T16:15:00Z',
          reason: 'RecordFound',
        },
      ],
    },
  },
];

const mockPolicies: TrafficProtectionPolicy[] = [
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'TrafficProtectionPolicy',
    metadata: {
      name: 'production-waf',
      namespace: 'production',
      uid: 'policy-uuid-1',
      creationTimestamp: '2024-01-08T12:00:00Z',
    },
    spec: {
      targetRefs: [
        {
          group: 'networking.datumapis.com',
          kind: 'HTTPProxy',
          name: 'api-gateway',
        },
        {
          group: 'networking.datumapis.com',
          kind: 'HTTPProxy',
          name: 'web-frontend',
        },
      ],
      mode: 'Enforce',
      samplingPercentage: 100,
      ruleSets: [
        {
          name: 'owasp-crs',
          paranoiaLevel: 2,
          scoreThreshold: 5,
          ruleExclusions: [
            {
              ruleId: '942100',
              reason: 'False positive on JSON API requests',
            },
          ],
        },
      ],
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-08T12:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Enforcing',
          status: 'True',
          lastTransitionTime: '2024-01-08T12:01:30Z',
          reason: 'PolicyEnforced',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'TrafficProtectionPolicy',
    metadata: {
      name: 'staging-waf',
      namespace: 'staging',
      uid: 'policy-uuid-2',
      creationTimestamp: '2024-01-14T09:00:00Z',
    },
    spec: {
      targetRefs: [
        {
          group: 'networking.datumapis.com',
          kind: 'HTTPProxy',
          name: 'staging-api',
        },
      ],
      mode: 'Observe',
      samplingPercentage: 50,
      ruleSets: [
        {
          name: 'owasp-crs',
          paranoiaLevel: 3,
          scoreThreshold: 3,
        },
      ],
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-14T09:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Observing',
          status: 'True',
          lastTransitionTime: '2024-01-14T09:01:30Z',
          reason: 'PolicyObserving',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha',
    kind: 'TrafficProtectionPolicy',
    metadata: {
      name: 'disabled-policy',
      namespace: 'development',
      uid: 'policy-uuid-3',
      creationTimestamp: '2024-01-17T15:00:00Z',
    },
    spec: {
      targetRefs: [
        {
          group: 'networking.datumapis.com',
          kind: 'HTTPProxy',
          name: 'dev-gateway',
        },
      ],
      mode: 'Disabled',
      ruleSets: [
        {
          name: 'owasp-crs',
          paranoiaLevel: 1,
        },
      ],
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-17T15:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Disabled',
          status: 'True',
          lastTransitionTime: '2024-01-17T15:01:30Z',
          reason: 'PolicyDisabled',
        },
      ],
    },
  },
];

const mockConnectors: Connector[] = [
  {
    apiVersion: 'networking.datumapis.com/v1alpha1',
    kind: 'Connector',
    metadata: {
      name: 'edge-connector-us-west',
      namespace: 'network-system',
      uid: 'connector-uuid-1',
      creationTimestamp: '2024-01-03T08:00:00Z',
      labels: { region: 'us-west-2', tier: 'production' },
    },
    spec: {
      connectorClassName: 'datum-edge-connector',
      capabilities: ['ConnectTCP', 'ConnectHTTP'],
    },
    status: {
      connectionDetails: {
        endpoint: 'wss://edge.datumapis.com/connect',
        lastConnected: '2024-01-19T12:30:00Z',
        clientId: 'client-us-west-001',
      },
      capabilities: ['ConnectTCP', 'ConnectHTTP'],
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-03T08:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Ready',
          status: 'True',
          lastTransitionTime: '2024-01-03T08:02:00Z',
          reason: 'Connected',
          message: 'Connector is connected and ready',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha1',
    kind: 'Connector',
    metadata: {
      name: 'edge-connector-eu-central',
      namespace: 'network-system',
      uid: 'connector-uuid-2',
      creationTimestamp: '2024-01-05T10:00:00Z',
      labels: { region: 'eu-central-1', tier: 'production' },
    },
    spec: {
      connectorClassName: 'datum-edge-connector',
      capabilities: ['ConnectTCP'],
    },
    status: {
      connectionDetails: {
        endpoint: 'wss://edge-eu.datumapis.com/connect',
        lastConnected: '2024-01-19T11:45:00Z',
        clientId: 'client-eu-central-001',
      },
      capabilities: ['ConnectTCP'],
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-05T10:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Ready',
          status: 'True',
          lastTransitionTime: '2024-01-05T10:02:00Z',
          reason: 'Connected',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha1',
    kind: 'Connector',
    metadata: {
      name: 'edge-connector-ap-southeast',
      namespace: 'network-system',
      uid: 'connector-uuid-3',
      creationTimestamp: '2024-01-10T06:00:00Z',
      labels: { region: 'ap-southeast-1', tier: 'staging' },
    },
    spec: {
      connectorClassName: 'datum-edge-connector',
      capabilities: ['ConnectTCP', 'ConnectHTTP'],
    },
    status: {
      connectionDetails: {
        endpoint: 'wss://edge-ap.datumapis.com/connect',
        clientId: 'client-ap-southeast-001',
      },
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-10T06:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Ready',
          status: 'False',
          lastTransitionTime: '2024-01-19T08:00:00Z',
          reason: 'Disconnected',
          message: 'Connector lost connection at 2024-01-19T08:00:00Z',
        },
      ],
    },
  },
];

const mockConnectorAdvertisements: ConnectorAdvertisement[] = [
  {
    apiVersion: 'networking.datumapis.com/v1alpha1',
    kind: 'ConnectorAdvertisement',
    metadata: {
      name: 'us-west-services',
      namespace: 'network-system',
      uid: 'adv-uuid-1',
      creationTimestamp: '2024-01-03T08:30:00Z',
    },
    spec: {
      connectorRef: {
        name: 'edge-connector-us-west',
        namespace: 'network-system',
      },
      layer4: [
        {
          name: 'database-cluster',
          address: '10.0.1.100',
          ports: [
            { port: 5432, protocol: 'TCP', targetPort: 5432 },
            { port: 5433, protocol: 'TCP', targetPort: 5432 },
          ],
        },
        {
          name: 'redis-cache',
          address: '10.0.1.200',
          ports: [{ port: 6379, protocol: 'TCP' }],
        },
      ],
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-03T08:31:00Z',
          reason: 'Accepted',
        },
      ],
    },
  },
  {
    apiVersion: 'networking.datumapis.com/v1alpha1',
    kind: 'ConnectorAdvertisement',
    metadata: {
      name: 'eu-central-services',
      namespace: 'network-system',
      uid: 'adv-uuid-2',
      creationTimestamp: '2024-01-05T10:30:00Z',
    },
    spec: {
      connectorRef: {
        name: 'edge-connector-eu-central',
        namespace: 'network-system',
      },
      layer4: [
        {
          name: 'mongodb-cluster',
          address: '10.1.1.100',
          ports: [{ port: 27017, protocol: 'TCP' }],
        },
      ],
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-05T10:31:00Z',
          reason: 'Accepted',
        },
      ],
    },
  },
];

const mockRecentActivity: RecentActivity[] = [
  {
    id: 'activity-1',
    type: 'gateway',
    action: 'created',
    resourceName: 'staging-api',
    timestamp: '2024-01-18T14:00:00Z',
    user: 'admin@example.com',
  },
  {
    id: 'activity-2',
    type: 'policy',
    action: 'updated',
    resourceName: 'production-waf',
    timestamp: '2024-01-18T10:30:00Z',
    user: 'security@example.com',
  },
  {
    id: 'activity-3',
    type: 'domain',
    action: 'created',
    resourceName: 'staging.example.com',
    timestamp: '2024-01-16T16:00:00Z',
    user: 'admin@example.com',
  },
  {
    id: 'activity-4',
    type: 'connector',
    action: 'updated',
    resourceName: 'edge-connector-ap-southeast',
    timestamp: '2024-01-19T08:00:00Z',
  },
  {
    id: 'activity-5',
    type: 'gateway',
    action: 'updated',
    resourceName: 'api-gateway',
    timestamp: '2024-01-15T10:30:00Z',
    user: 'devops@example.com',
  },
];

const mockSecurityPolicies: SecurityPolicy[] = [
  {
    apiVersion: 'gateway.envoyproxy.io/v1alpha1',
    kind: 'SecurityPolicy',
    metadata: {
      name: 'api-auth-policy',
      namespace: 'production',
      uid: 'sp-uuid-1',
      creationTimestamp: '2024-01-10T09:00:00Z',
    },
    spec: {
      targetRefs: [
        {
          group: 'gateway.networking.k8s.io',
          kind: 'HTTPRoute',
          name: 'api-route',
        },
      ],
      jwt: {
        providers: [
          {
            name: 'auth0',
            issuer: 'https://example.auth0.com/',
            audiences: ['https://api.example.com'],
            remoteJWKS: {
              uri: 'https://example.auth0.com/.well-known/jwks.json',
            },
            claimToHeaders: [
              { claim: 'sub', header: 'x-user-id' },
            ],
          },
        ],
      },
      cors: {
        allowOrigins: [
          { type: 'Exact', value: 'https://app.example.com' },
          { type: 'Prefix', value: 'https://staging.' },
        ],
        allowMethods: ['GET', 'POST', 'PUT', 'DELETE'],
        allowHeaders: ['Authorization', 'Content-Type'],
        maxAge: '86400s',
        allowCredentials: true,
      },
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-10T09:01:00Z',
          reason: 'Accepted',
          message: 'SecurityPolicy has been accepted',
        },
        {
          type: 'Programmed',
          status: 'True',
          lastTransitionTime: '2024-01-10T09:02:00Z',
          reason: 'Programmed',
          message: 'SecurityPolicy configuration applied',
        },
      ],
    },
  },
  {
    apiVersion: 'gateway.envoyproxy.io/v1alpha1',
    kind: 'SecurityPolicy',
    metadata: {
      name: 'admin-basic-auth',
      namespace: 'production',
      uid: 'sp-uuid-2',
      creationTimestamp: '2024-01-12T14:00:00Z',
    },
    spec: {
      targetRefs: [
        {
          group: 'gateway.networking.k8s.io',
          kind: 'HTTPRoute',
          name: 'admin-route',
          sectionName: 'admin-section',
        },
      ],
      basicAuth: {
        users: {
          name: 'admin-htpasswd',
        },
        forwardUsernameHeader: 'X-Admin-User',
      },
      authorization: {
        defaultAction: 'Deny',
        rules: [
          {
            name: 'allow-internal',
            action: 'Allow',
            principal: {
              clientCIDRs: [
                { cidr: '10.0.0.0/8' },
                { cidr: '192.168.0.0/16' },
              ],
            },
          },
        ],
      },
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-12T14:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Programmed',
          status: 'True',
          lastTransitionTime: '2024-01-12T14:02:00Z',
          reason: 'Programmed',
        },
      ],
    },
  },
  {
    apiVersion: 'gateway.envoyproxy.io/v1alpha1',
    kind: 'SecurityPolicy',
    metadata: {
      name: 'api-key-policy',
      namespace: 'staging',
      uid: 'sp-uuid-3',
      creationTimestamp: '2024-01-15T11:00:00Z',
    },
    spec: {
      targetRefs: [
        {
          group: 'gateway.networking.k8s.io',
          kind: 'Gateway',
          name: 'staging-gateway',
        },
      ],
      apiKeyAuth: {
        credentialRefs: [
          { name: 'api-keys-secret' },
        ],
        extractFrom: [
          {
            headers: ['X-API-Key', 'Authorization'],
          },
          {
            params: ['api_key'],
          },
        ],
        forwardClientIDHeader: 'X-Client-ID',
      },
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-15T11:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Programmed',
          status: 'False',
          lastTransitionTime: '2024-01-15T11:02:00Z',
          reason: 'SecretNotFound',
          message: 'Secret api-keys-secret not found',
        },
      ],
    },
  },
  {
    apiVersion: 'gateway.envoyproxy.io/v1alpha1',
    kind: 'SecurityPolicy',
    metadata: {
      name: 'oidc-login-policy',
      namespace: 'production',
      uid: 'sp-uuid-4',
      creationTimestamp: '2024-01-18T16:00:00Z',
    },
    spec: {
      targetRefs: [
        {
          group: 'gateway.networking.k8s.io',
          kind: 'HTTPRoute',
          name: 'app-route',
        },
      ],
      oidc: {
        provider: {
          issuer: 'https://accounts.google.com',
          authorizationEndpoint: 'https://accounts.google.com/o/oauth2/v2/auth',
          tokenEndpoint: 'https://oauth2.googleapis.com/token',
        },
        clientID: 'my-client-id.apps.googleusercontent.com',
        clientSecret: {
          name: 'google-oidc-secret',
        },
        scopes: ['openid', 'profile', 'email'],
        redirectURL: 'https://app.example.com/oauth2/callback',
        forwardAccessToken: true,
      },
    },
    status: {
      conditions: [
        {
          type: 'Accepted',
          status: 'True',
          lastTransitionTime: '2024-01-18T16:01:00Z',
          reason: 'Accepted',
        },
        {
          type: 'Programmed',
          status: 'True',
          lastTransitionTime: '2024-01-18T16:02:00Z',
          reason: 'Programmed',
        },
      ],
    },
  },
];

// API error type
interface ApiError {
  error: {
    code: number;
    message: string;
  };
}

// Helper function for API requests
async function apiRequest<T>(
  path: string,
  options: RequestInit = {}
): Promise<T> {
  const response = await fetch(path, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
  });

  if (!response.ok) {
    const errorData = (await response.json()) as ApiError;
    throw new Error(errorData.error?.message || `HTTP error ${response.status}`);
  }

  // Handle 204 No Content
  if (response.status === 204) {
    return undefined as T;
  }

  return response.json();
}

// API Client
class ApiClient {
  private baseUrl: string;

  constructor(baseUrl: string = '/api') {
    this.baseUrl = baseUrl;
  }

  // HTTPProxy APIs
  async listHTTPProxies(namespace?: string): Promise<ResourceList<HTTPProxy>> {
    if (isDev) {
      await delay(300);
      const items = namespace
        ? mockHTTPProxies.filter(p => p.metadata.namespace === namespace)
        : mockHTTPProxies;
      return {
        apiVersion: 'networking.datumapis.com/v1alpha',
        kind: 'HTTPProxyList',
        metadata: {},
        items,
      };
    }

    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<ResourceList<HTTPProxy>>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/httpproxies`
    );
  }

  async getHTTPProxy(name: string, namespace: string): Promise<HTTPProxy | null> {
    if (isDev) {
      await delay(200);
      return mockHTTPProxies.find(
        p => p.metadata.name === name && p.metadata.namespace === namespace
      ) || null;
    }

    try {
      return await apiRequest<HTTPProxy>(
        `${this.baseUrl}/v1alpha/namespaces/${namespace}/httpproxies/${name}`
      );
    } catch (error) {
      if (error instanceof Error && error.message.includes('not found')) {
        return null;
      }
      throw error;
    }
  }

  async getHTTPProxyRelatedResources(name: string, namespace: string): Promise<HTTPProxyRelatedResources> {
    if (isDev) {
      await delay(300);
      const proxy = mockHTTPProxies.find(
        p => p.metadata.name === name && p.metadata.namespace === namespace
      );
      if (!proxy) {
        return { gateway: null, httpRoute: null, endpointSlices: [], domains: [] };
      }
      const hostnames = proxy.spec.hostnames || [];
      const relatedDomains = mockDomains.filter(d =>
        hostnames.some(h => h === d.spec.domainName || h.endsWith('.' + d.spec.domainName))
      );
      return {
        gateway: null,
        httpRoute: null,
        endpointSlices: [],
        domains: relatedDomains,
      };
    }

    return apiRequest<HTTPProxyRelatedResources>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/httpproxies/${name}/related`
    );
  }

  async createHTTPProxy(proxy: HTTPProxy): Promise<HTTPProxy> {
    if (isDev) {
      await delay(500);
      const newProxy = {
        ...proxy,
        metadata: {
          ...proxy.metadata,
          uid: `uuid-${Date.now()}`,
          creationTimestamp: new Date().toISOString(),
        },
      };
      mockHTTPProxies.push(newProxy);
      return newProxy;
    }

    const namespace = proxy.metadata.namespace;
    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<HTTPProxy>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/httpproxies`,
      {
        method: 'POST',
        body: JSON.stringify(proxy),
      }
    );
  }

  async updateHTTPProxy(proxy: HTTPProxy): Promise<HTTPProxy> {
    if (isDev) {
      await delay(500);
      const index = mockHTTPProxies.findIndex(
        p => p.metadata.name === proxy.metadata.name && p.metadata.namespace === proxy.metadata.namespace
      );
      if (index >= 0) {
        mockHTTPProxies[index] = proxy;
      }
      return proxy;
    }

    const { namespace, name } = proxy.metadata;
    if (!namespace || !name) {
      throw new Error('namespace and name are required');
    }
    return apiRequest<HTTPProxy>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/httpproxies/${name}`,
      {
        method: 'PUT',
        body: JSON.stringify(proxy),
      }
    );
  }

  async deleteHTTPProxy(name: string, namespace: string): Promise<void> {
    if (isDev) {
      await delay(300);
      const index = mockHTTPProxies.findIndex(
        p => p.metadata.name === name && p.metadata.namespace === namespace
      );
      if (index >= 0) {
        mockHTTPProxies.splice(index, 1);
      }
      return;
    }

    await apiRequest<void>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/httpproxies/${name}`,
      { method: 'DELETE' }
    );
  }

  async testHTTPProxy(
    name: string,
    namespace: string,
    request: TestProxyRequest
  ): Promise<TestProxyResponse> {
    if (isDev) {
      await delay(800);
      // Mock response for development
      return {
        statusCode: 200,
        statusText: 'OK',
        headers: {
          'content-type': 'application/json',
          'x-request-id': 'mock-' + Date.now(),
          'cache-control': 'no-cache',
        },
        body: JSON.stringify({
          message: 'Mock response from development mode',
          method: request.method,
          path: request.path,
          timestamp: new Date().toISOString(),
        }, null, 2),
        latencyMs: Math.floor(Math.random() * 200) + 50,
        timestamp: new Date().toISOString(),
      };
    }

    return apiRequest<TestProxyResponse>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/httpproxies/${name}/test`,
      {
        method: 'POST',
        body: JSON.stringify(request),
      }
    );
  }

  // Domain APIs
  async listDomains(namespace?: string): Promise<ResourceList<Domain>> {
    if (isDev) {
      await delay(300);
      const items = namespace
        ? mockDomains.filter(d => d.metadata.namespace === namespace)
        : mockDomains;
      return {
        apiVersion: 'networking.datumapis.com/v1alpha',
        kind: 'DomainList',
        metadata: {},
        items,
      };
    }

    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<ResourceList<Domain>>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/domains`
    );
  }

  async getDomain(name: string, namespace: string): Promise<Domain | null> {
    if (isDev) {
      await delay(200);
      return mockDomains.find(
        d => d.metadata.name === name && d.metadata.namespace === namespace
      ) || null;
    }

    try {
      return await apiRequest<Domain>(
        `${this.baseUrl}/v1alpha/namespaces/${namespace}/domains/${name}`
      );
    } catch (error) {
      if (error instanceof Error && error.message.includes('not found')) {
        return null;
      }
      throw error;
    }
  }

  async createDomain(domain: Domain): Promise<Domain> {
    if (isDev) {
      await delay(500);
      const newDomain = {
        ...domain,
        metadata: {
          ...domain.metadata,
          uid: `domain-uuid-${Date.now()}`,
          creationTimestamp: new Date().toISOString(),
        },
      };
      mockDomains.push(newDomain);
      return newDomain;
    }

    const namespace = domain.metadata.namespace;
    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<Domain>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/domains`,
      {
        method: 'POST',
        body: JSON.stringify(domain),
      }
    );
  }

  async updateDomain(domain: Domain): Promise<Domain> {
    if (isDev) {
      await delay(500);
      const index = mockDomains.findIndex(
        d => d.metadata.name === domain.metadata.name && d.metadata.namespace === domain.metadata.namespace
      );
      if (index >= 0) {
        mockDomains[index] = domain;
      }
      return domain;
    }

    const { namespace, name } = domain.metadata;
    if (!namespace || !name) {
      throw new Error('namespace and name are required');
    }
    return apiRequest<Domain>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/domains/${name}`,
      {
        method: 'PUT',
        body: JSON.stringify(domain),
      }
    );
  }

  async deleteDomain(name: string, namespace: string): Promise<void> {
    if (isDev) {
      await delay(300);
      const index = mockDomains.findIndex(
        d => d.metadata.name === name && d.metadata.namespace === namespace
      );
      if (index >= 0) {
        mockDomains.splice(index, 1);
      }
      return;
    }

    await apiRequest<void>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/domains/${name}`,
      { method: 'DELETE' }
    );
  }

  // TrafficProtectionPolicy APIs
  async listPolicies(namespace?: string): Promise<ResourceList<TrafficProtectionPolicy>> {
    if (isDev) {
      await delay(300);
      const items = namespace
        ? mockPolicies.filter(p => p.metadata.namespace === namespace)
        : mockPolicies;
      return {
        apiVersion: 'networking.datumapis.com/v1alpha',
        kind: 'TrafficProtectionPolicyList',
        metadata: {},
        items,
      };
    }

    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<ResourceList<TrafficProtectionPolicy>>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/trafficprotectionpolicies`
    );
  }

  async getPolicy(name: string, namespace: string): Promise<TrafficProtectionPolicy | null> {
    if (isDev) {
      await delay(200);
      return mockPolicies.find(
        p => p.metadata.name === name && p.metadata.namespace === namespace
      ) || null;
    }

    try {
      return await apiRequest<TrafficProtectionPolicy>(
        `${this.baseUrl}/v1alpha/namespaces/${namespace}/trafficprotectionpolicies/${name}`
      );
    } catch (error) {
      if (error instanceof Error && error.message.includes('not found')) {
        return null;
      }
      throw error;
    }
  }

  async createPolicy(policy: TrafficProtectionPolicy): Promise<TrafficProtectionPolicy> {
    if (isDev) {
      await delay(500);
      const newPolicy = {
        ...policy,
        metadata: {
          ...policy.metadata,
          uid: `policy-uuid-${Date.now()}`,
          creationTimestamp: new Date().toISOString(),
        },
      };
      mockPolicies.push(newPolicy);
      return newPolicy;
    }

    const namespace = policy.metadata.namespace;
    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<TrafficProtectionPolicy>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/trafficprotectionpolicies`,
      {
        method: 'POST',
        body: JSON.stringify(policy),
      }
    );
  }

  async updatePolicy(policy: TrafficProtectionPolicy): Promise<TrafficProtectionPolicy> {
    if (isDev) {
      await delay(500);
      const index = mockPolicies.findIndex(
        p => p.metadata.name === policy.metadata.name && p.metadata.namespace === policy.metadata.namespace
      );
      if (index >= 0) {
        mockPolicies[index] = policy;
      }
      return policy;
    }

    const { namespace, name } = policy.metadata;
    if (!namespace || !name) {
      throw new Error('namespace and name are required');
    }
    return apiRequest<TrafficProtectionPolicy>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/trafficprotectionpolicies/${name}`,
      {
        method: 'PUT',
        body: JSON.stringify(policy),
      }
    );
  }

  async deletePolicy(name: string, namespace: string): Promise<void> {
    if (isDev) {
      await delay(300);
      const index = mockPolicies.findIndex(
        p => p.metadata.name === name && p.metadata.namespace === namespace
      );
      if (index >= 0) {
        mockPolicies.splice(index, 1);
      }
      return;
    }

    await apiRequest<void>(
      `${this.baseUrl}/v1alpha/namespaces/${namespace}/trafficprotectionpolicies/${name}`,
      { method: 'DELETE' }
    );
  }

  // Connector APIs (read-only)
  async listConnectors(namespace?: string): Promise<ResourceList<Connector>> {
    if (isDev) {
      await delay(300);
      const items = namespace
        ? mockConnectors.filter(c => c.metadata.namespace === namespace)
        : mockConnectors;
      return {
        apiVersion: 'networking.datumapis.com/v1alpha1',
        kind: 'ConnectorList',
        metadata: {},
        items,
      };
    }

    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<ResourceList<Connector>>(
      `${this.baseUrl}/v1alpha1/namespaces/${namespace}/connectors`
    );
  }

  async getConnector(name: string, namespace: string): Promise<Connector | null> {
    if (isDev) {
      await delay(200);
      return mockConnectors.find(
        c => c.metadata.name === name && c.metadata.namespace === namespace
      ) || null;
    }

    try {
      return await apiRequest<Connector>(
        `${this.baseUrl}/v1alpha1/namespaces/${namespace}/connectors/${name}`
      );
    } catch (error) {
      if (error instanceof Error && error.message.includes('not found')) {
        return null;
      }
      throw error;
    }
  }

  // ConnectorAdvertisement APIs (read-only)
  async listConnectorAdvertisements(namespace?: string): Promise<ResourceList<ConnectorAdvertisement>> {
    if (isDev) {
      await delay(300);
      const items = namespace
        ? mockConnectorAdvertisements.filter(a => a.metadata.namespace === namespace)
        : mockConnectorAdvertisements;
      return {
        apiVersion: 'networking.datumapis.com/v1alpha1',
        kind: 'ConnectorAdvertisementList',
        metadata: {},
        items,
      };
    }

    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<ResourceList<ConnectorAdvertisement>>(
      `${this.baseUrl}/v1alpha1/namespaces/${namespace}/connectoradvertisements`
    );
  }

  async getConnectorAdvertisement(name: string, namespace: string): Promise<ConnectorAdvertisement | null> {
    if (isDev) {
      await delay(200);
      return mockConnectorAdvertisements.find(
        a => a.metadata.name === name && a.metadata.namespace === namespace
      ) || null;
    }

    try {
      return await apiRequest<ConnectorAdvertisement>(
        `${this.baseUrl}/v1alpha1/namespaces/${namespace}/connectoradvertisements/${name}`
      );
    } catch (error) {
      if (error instanceof Error && error.message.includes('not found')) {
        return null;
      }
      throw error;
    }
  }

  async getConnectorAdvertisementsByConnector(
    connectorName: string,
    connectorNamespace: string
  ): Promise<ConnectorAdvertisement[]> {
    if (isDev) {
      await delay(200);
      return mockConnectorAdvertisements.filter(
        a =>
          a.spec.connectorRef.name === connectorName &&
          (a.spec.connectorRef.namespace === connectorNamespace || !a.spec.connectorRef.namespace)
      );
    }

    // In production, fetch all advertisements and filter client-side
    // A future improvement could add server-side filtering
    const list = await this.listConnectorAdvertisements(connectorNamespace);
    return list.items.filter(
      a =>
        a.spec.connectorRef.name === connectorName &&
        (a.spec.connectorRef.namespace === connectorNamespace || !a.spec.connectorRef.namespace)
    );
  }

  // SecurityPolicy APIs
  async listSecurityPolicies(namespace?: string): Promise<ResourceList<SecurityPolicy>> {
    if (isDev) {
      await delay(300);
      const items = namespace
        ? mockSecurityPolicies.filter(p => p.metadata.namespace === namespace)
        : mockSecurityPolicies;
      return {
        apiVersion: 'gateway.envoyproxy.io/v1alpha1',
        kind: 'SecurityPolicyList',
        metadata: {},
        items,
      };
    }

    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<ResourceList<SecurityPolicy>>(
      `${this.baseUrl}/v1alpha1/namespaces/${namespace}/securitypolicies`
    );
  }

  async getSecurityPolicy(name: string, namespace: string): Promise<SecurityPolicy | null> {
    if (isDev) {
      await delay(200);
      return mockSecurityPolicies.find(
        p => p.metadata.name === name && p.metadata.namespace === namespace
      ) || null;
    }

    try {
      return await apiRequest<SecurityPolicy>(
        `${this.baseUrl}/v1alpha1/namespaces/${namespace}/securitypolicies/${name}`
      );
    } catch (error) {
      if (error instanceof Error && error.message.includes('not found')) {
        return null;
      }
      throw error;
    }
  }

  async createSecurityPolicy(policy: SecurityPolicy): Promise<SecurityPolicy> {
    if (isDev) {
      await delay(500);
      const newPolicy = {
        ...policy,
        metadata: {
          ...policy.metadata,
          uid: `sp-uuid-${Date.now()}`,
          creationTimestamp: new Date().toISOString(),
        },
      };
      mockSecurityPolicies.push(newPolicy);
      return newPolicy;
    }

    const namespace = policy.metadata.namespace;
    if (!namespace) {
      throw new Error('namespace is required');
    }
    return apiRequest<SecurityPolicy>(
      `${this.baseUrl}/v1alpha1/namespaces/${namespace}/securitypolicies`,
      {
        method: 'POST',
        body: JSON.stringify(policy),
      }
    );
  }

  async updateSecurityPolicy(policy: SecurityPolicy): Promise<SecurityPolicy> {
    if (isDev) {
      await delay(500);
      const index = mockSecurityPolicies.findIndex(
        p => p.metadata.name === policy.metadata.name && p.metadata.namespace === policy.metadata.namespace
      );
      if (index >= 0) {
        mockSecurityPolicies[index] = policy;
      }
      return policy;
    }

    const { namespace, name } = policy.metadata;
    if (!namespace || !name) {
      throw new Error('namespace and name are required');
    }
    return apiRequest<SecurityPolicy>(
      `${this.baseUrl}/v1alpha1/namespaces/${namespace}/securitypolicies/${name}`,
      {
        method: 'PUT',
        body: JSON.stringify(policy),
      }
    );
  }

  async deleteSecurityPolicy(name: string, namespace: string): Promise<void> {
    if (isDev) {
      await delay(300);
      const index = mockSecurityPolicies.findIndex(
        p => p.metadata.name === name && p.metadata.namespace === namespace
      );
      if (index >= 0) {
        mockSecurityPolicies.splice(index, 1);
      }
      return;
    }

    await apiRequest<void>(
      `${this.baseUrl}/v1alpha1/namespaces/${namespace}/securitypolicies/${name}`,
      { method: 'DELETE' }
    );
  }

  // Dashboard APIs
  async getDashboardStats(): Promise<DashboardStats> {
    if (isDev) {
      await delay(400);

      const healthyGateways = mockHTTPProxies.filter(p =>
        p.status?.conditions?.some(c => c.type === 'Programmed' && c.status === 'True')
      ).length;

      const verifiedDomains = mockDomains.filter(d =>
        d.status?.conditions?.some(c => c.type === 'Verified' && c.status === 'True')
      ).length;

      const connectedConnectors = mockConnectors.filter(c =>
        c.status?.conditions?.some(cond => cond.type === 'Ready' && cond.status === 'True')
      ).length;

      return {
        gateways: {
          total: mockHTTPProxies.length,
          healthy: healthyGateways,
          unhealthy: mockHTTPProxies.length - healthyGateways,
        },
        domains: {
          total: mockDomains.length,
          verified: verifiedDomains,
          pending: mockDomains.length - verifiedDomains,
        },
        policies: {
          total: mockPolicies.length,
          enforcing: mockPolicies.filter(p => p.spec.mode === 'Enforce').length,
          observing: mockPolicies.filter(p => p.spec.mode === 'Observe').length,
          disabled: mockPolicies.filter(p => p.spec.mode === 'Disabled').length,
        },
        connectors: {
          total: mockConnectors.length,
          connected: connectedConnectors,
          disconnected: mockConnectors.length - connectedConnectors,
        },
      };
    }

    return apiRequest<DashboardStats>(`${this.baseUrl}/dashboard/stats`);
  }

  async getRecentActivity(): Promise<RecentActivity[]> {
    if (isDev) {
      await delay(300);
      return mockRecentActivity;
    }

    // Recent activity is not currently tracked by the backend
    // Return empty array in production until this feature is implemented
    return [];
  }

  // Namespace APIs
  async listNamespaces(): Promise<{ items: { name: string; creationTimestamp: string }[] }> {
    if (isDev) {
      await delay(200);
      return {
        items: [
          { name: 'production', creationTimestamp: '2024-01-01T00:00:00Z' },
          { name: 'staging', creationTimestamp: '2024-01-01T00:00:00Z' },
          { name: 'development', creationTimestamp: '2024-01-01T00:00:00Z' },
          { name: 'network-system', creationTimestamp: '2024-01-01T00:00:00Z' },
        ],
      };
    }

    return apiRequest<{ items: { name: string; creationTimestamp: string }[] }>(
      `${this.baseUrl}/namespaces`
    );
  }
}

export const apiClient = new ApiClient();
export default apiClient;

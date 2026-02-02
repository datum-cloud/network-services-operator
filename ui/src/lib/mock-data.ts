import type {
  HTTPProxy,
  Domain,
  TrafficProtectionPolicy,
  Connector,
  ConnectorAdvertisement,
  DashboardStats,
  RecentActivity,
} from '@/api/types';

// Mock data for local development
export const mockHTTPProxies: HTTPProxy[] = [
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

export const mockDomains: Domain[] = [
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

export const mockPolicies: TrafficProtectionPolicy[] = [
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

export const mockConnectors: Connector[] = [
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

export const mockConnectorAdvertisements: ConnectorAdvertisement[] = [
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

export const mockRecentActivity: RecentActivity[] = [
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

export function getMockDashboardStats(): DashboardStats {
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

export const mockNamespaces = [
  { name: 'production', creationTimestamp: '2024-01-01T00:00:00Z' },
  { name: 'staging', creationTimestamp: '2024-01-01T00:00:00Z' },
  { name: 'development', creationTimestamp: '2024-01-01T00:00:00Z' },
  { name: 'network-system', creationTimestamp: '2024-01-01T00:00:00Z' },
];

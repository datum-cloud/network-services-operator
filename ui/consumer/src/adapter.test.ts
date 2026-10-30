import {
  toHTTPProxy,
  toHTTPProxyList,
  toNetwork,
  toNetworkInterface,
  toNetworkInterfaceList,
  toNetworkList,
  toNetworkLoadBalancer,
  toNetworkLoadBalancerList,
  toNetworkLoadBalancerRouteList,
  toNetworkService,
  toNetworkServiceList,
  toSubnet,
  toSubnetList,
  type RawHTTPProxy,
  type RawNetwork,
  type RawNetworkInterface,
  type RawNetworkLoadBalancer,
  type RawNetworkService,
  type RawSubnet,
} from './adapter';
import { describe, expect, it } from 'vitest';

const baseRaw: RawNetwork = {
  metadata: {
    uid: 'abc-123',
    name: 'default',
    namespace: 'default',
    resourceVersion: '42',
    creationTimestamp: '2026-01-01T00:00:00Z',
  },
  spec: {
    ipam: { mode: 'Auto' },
    ipFamilies: ['IPv6'],
    mtu: 1460,
  },
  status: {
    conditions: [
      {
        type: 'Ready',
        status: 'True',
        reason: 'Ready',
        message: 'The network is ready for use.',
        lastTransitionTime: '2026-01-01T00:05:00Z',
        observedGeneration: 1,
      },
    ],
    ipam: { ipv6Prefix: 'fd20:1234:5678::/48' },
  },
};

describe('toNetwork', () => {
  it('maps a fully-populated raw Network', () => {
    const network = toNetwork(baseRaw);

    expect(network).toMatchObject({
      uid: 'abc-123',
      name: 'default',
      namespace: 'default',
      resourceVersion: '42',
      ipv6Prefix: 'fd20:1234:5678::/48',
      readyStatus: 'True',
      readyReason: 'Ready',
      readyMessage: 'The network is ready for use.',
      ipFamilies: ['IPv6'],
      ipamMode: 'Auto',
      mtu: 1460,
    });
    expect(network.createdAt).toEqual(new Date('2026-01-01T00:00:00Z'));
    expect(network.conditions).toHaveLength(1);
  });

  it('defaults readyStatus to Unknown when the Ready condition is absent', () => {
    const raw: RawNetwork = { ...baseRaw, status: { conditions: [] } };
    const network = toNetwork(raw);

    expect(network.readyStatus).toBe('Unknown');
    expect(network.readyReason).toBeUndefined();
  });

  it('defaults readyStatus to Unknown when status.conditions is missing entirely', () => {
    const raw: RawNetwork = { ...baseRaw, status: undefined };
    const network = toNetwork(raw);

    expect(network.readyStatus).toBe('Unknown');
    expect(network.ipv6Prefix).toBeUndefined();
  });

  it('handles a False Ready condition with a reason', () => {
    const raw: RawNetwork = {
      ...baseRaw,
      status: {
        conditions: [
          { type: 'Ready', status: 'False', reason: 'IPv6Required', message: 'no ipv6' },
        ],
      },
    };
    const network = toNetwork(raw);

    expect(network.readyStatus).toBe('False');
    expect(network.readyReason).toBe('IPv6Required');
  });

  it('handles an absent status.ipam without throwing', () => {
    const raw: RawNetwork = { ...baseRaw, status: { conditions: baseRaw.status?.conditions } };
    const network = toNetwork(raw);

    expect(network.ipv6Prefix).toBeUndefined();
  });

  it('defaults missing metadata fields safely', () => {
    const network = toNetwork({});

    expect(network.uid).toBe('');
    expect(network.name).toBe('');
    expect(network.ipFamilies).toEqual([]);
    expect(network.readyStatus).toBe('Unknown');
    expect(network.createdAt).toBeInstanceOf(Date);
  });

  it('drops unrecognized ipFamilies and ipamMode values rather than passing them through', () => {
    const raw: RawNetwork = {
      ...baseRaw,
      spec: { ipam: { mode: 'Weird' as never }, ipFamilies: ['IPv6', 'Bogus' as never] },
    };
    const network = toNetwork(raw);

    expect(network.ipFamilies).toEqual(['IPv6']);
    expect(network.ipamMode).toBeUndefined();
  });
});

describe('toNetworkList', () => {
  it('maps every item in the list', () => {
    const list = toNetworkList([baseRaw, { ...baseRaw, metadata: { name: 'second' } }]);
    expect(list).toHaveLength(2);
    expect(list.map((n) => n.name)).toEqual(['default', 'second']);
  });

  it('returns an empty array for an empty list', () => {
    expect(toNetworkList([])).toEqual([]);
  });

  it('leaves out a network that is being deleted', () => {
    const list = toNetworkList([
      baseRaw,
      { ...baseRaw, metadata: { name: 'going', deletionTimestamp: '2026-10-20T00:00:00Z' } },
    ]);
    expect(list.map((n) => n.name)).not.toContain('going');
    expect(list).toHaveLength(1);
  });
});

const baseRawSubnet: RawSubnet = {
  metadata: {
    uid: 'subnet-uid',
    name: 'taptest-us-central-1-ipv6',
    creationTimestamp: '2026-08-25T14:18:34Z',
  },
  spec: {
    location: { name: 'us-central-1' },
    subnetClass: 'private',
    ipFamily: 'IPv6',
    startAddress: 'fd20:0:2::',
    prefixLength: 64,
  },
  status: {
    conditions: [
      { type: 'Ready', status: 'False', reason: 'NotProgrammed', message: 'Subnet is not yet programmed' },
    ],
    startAddress: 'fd20:0:2::',
    prefixLength: 64,
  },
};

describe('toSubnet', () => {
  it('maps a fully-populated raw Subnet', () => {
    const subnet = toSubnet(baseRawSubnet);

    expect(subnet).toMatchObject({
      uid: 'subnet-uid',
      name: 'taptest-us-central-1-ipv6',
      location: 'us-central-1',
      subnetClass: 'private',
      ipFamily: 'IPv6',
      startAddress: 'fd20:0:2::',
      prefixLength: 64,
      readyStatus: 'False',
      readyReason: 'NotProgrammed',
    });
    expect(subnet.createdAt).toEqual(new Date('2026-08-25T14:18:34Z'));
  });

  it('is not allocated when the Allocated condition is absent', () => {
    expect(toSubnet(baseRawSubnet).allocated).toBe(false);
  });

  it('is allocated when the Allocated condition is True', () => {
    const raw: RawSubnet = {
      ...baseRawSubnet,
      status: {
        ...baseRawSubnet.status,
        conditions: [
          { type: 'Allocated', status: 'True', reason: 'PrefixAllocated' },
          { type: 'Ready', status: 'False', reason: 'NotProgrammed' },
        ],
      },
    };

    expect(toSubnet(raw).allocated).toBe(true);
  });

  it('prefers status.startAddress/prefixLength over spec when both are present', () => {
    const raw: RawSubnet = {
      ...baseRawSubnet,
      spec: { ...baseRawSubnet.spec, startAddress: 'spec-value', prefixLength: 48 },
      status: { ...baseRawSubnet.status, startAddress: 'status-value', prefixLength: 64 },
    };
    const subnet = toSubnet(raw);

    expect(subnet.startAddress).toBe('status-value');
    expect(subnet.prefixLength).toBe(64);
  });

  it('falls back to spec.startAddress/prefixLength when status omits them', () => {
    const raw: RawSubnet = { ...baseRawSubnet, status: { conditions: [] } };
    const subnet = toSubnet(raw);

    expect(subnet.startAddress).toBe('fd20:0:2::');
    expect(subnet.prefixLength).toBe(64);
  });

  it('defaults readyStatus to Unknown when the Ready condition is absent', () => {
    const raw: RawSubnet = { ...baseRawSubnet, status: { conditions: [] } };
    const subnet = toSubnet(raw);

    expect(subnet.readyStatus).toBe('Unknown');
  });

  it('defaults missing fields safely', () => {
    const subnet = toSubnet({});

    expect(subnet.uid).toBe('');
    expect(subnet.name).toBe('');
    expect(subnet.location).toBeUndefined();
    expect(subnet.readyStatus).toBe('Unknown');
    expect(subnet.createdAt).toBeInstanceOf(Date);
  });
});

describe('toSubnetList', () => {
  it('maps every item in the list', () => {
    const list = toSubnetList([
      baseRawSubnet,
      { ...baseRawSubnet, metadata: { name: 'second' } },
    ]);
    expect(list).toHaveLength(2);
    expect(list.map((s) => s.name)).toEqual(['taptest-us-central-1-ipv6', 'second']);
  });

  it('returns an empty array for an empty list', () => {
    expect(toSubnetList([])).toEqual([]);
  });
});

const baseRawNetworkInterface: RawNetworkInterface = {
  metadata: {
    uid: 'iface-uid',
    name: 'storefront-abc123',
    creationTimestamp: '2026-08-25T14:18:34Z',
    labels: {
      'compute.datumapis.com/workload-name': 'storefront',
      'compute.datumapis.com/instance-index': '2',
      'networking.datumapis.com/location': 'us-central-1',
    },
  },
  spec: {
    network: { name: 'default' },
    claimRef: { name: 'storefront-abc123' },
    interfaceName: 'eth0',
    attachmentMode: 'Netns',
    addresses: [
      { family: 'IPv6', address: 'fd20:0:2::a/128', gateway: 'fd20:0:2::1', primary: true },
    ],
  },
  status: {
    phase: 'Bound',
    conditions: [
      { type: 'HolderAvailable', status: 'True', reason: 'HolderAvailable', message: 'serving' },
    ],
  },
};

describe('toNetworkInterface', () => {
  it('maps a fully-populated raw NetworkInterface', () => {
    const iface = toNetworkInterface(baseRawNetworkInterface);

    expect(iface).toMatchObject({
      uid: 'iface-uid',
      name: 'storefront-abc123',
      network: 'default',
      claimName: 'storefront-abc123',
      interfaceName: 'eth0',
      attachmentMode: 'Netns',
      phase: 'Bound',
      holderAvailableStatus: 'True',
      holderAvailableReason: 'HolderAvailable',
      workloadName: 'storefront',
      instanceIndex: '2',
      location: 'us-central-1',
    });
    expect(iface.createdAt).toEqual(new Date('2026-08-25T14:18:34Z'));
    expect(iface.addresses).toEqual([
      { family: 'IPv6', address: 'fd20:0:2::a/128', gateway: 'fd20:0:2::1', primary: true },
    ]);
  });

  it('leaves workloadName and location undefined, and labels empty, when the labels are absent', () => {
    const raw: RawNetworkInterface = { ...baseRawNetworkInterface, metadata: { name: 'no-labels' } };
    const iface = toNetworkInterface(raw);

    expect(iface.workloadName).toBeUndefined();
    expect(iface.instanceIndex).toBeUndefined();
    expect(iface.location).toBeUndefined();
    expect(iface.labels).toEqual({});
  });

  it('carries every raw label through verbatim, not just workloadName/location', () => {
    const raw: RawNetworkInterface = {
      ...baseRawNetworkInterface,
      metadata: {
        ...baseRawNetworkInterface.metadata,
        labels: {
          'compute.datumapis.com/workload-name': 'storefront',
          'networking.datumapis.com/location': 'us-central-1',
          'some-other/label': 'kept-too',
        },
      },
    };
    const iface = toNetworkInterface(raw);

    expect(iface.labels).toEqual({
      'compute.datumapis.com/workload-name': 'storefront',
      'networking.datumapis.com/location': 'us-central-1',
      'some-other/label': 'kept-too',
    });
  });

  it('defaults holderAvailableStatus to Unknown when the condition is absent', () => {
    const raw: RawNetworkInterface = { ...baseRawNetworkInterface, status: { phase: 'Available' } };
    const iface = toNetworkInterface(raw);

    expect(iface.holderAvailableStatus).toBe('Unknown');
    expect(iface.holderAvailableReason).toBeUndefined();
  });

  it('drops an unrecognized phase value rather than passing it through', () => {
    const raw: RawNetworkInterface = { ...baseRawNetworkInterface, status: { phase: 'Weird' } };
    const iface = toNetworkInterface(raw);

    expect(iface.phase).toBeUndefined();
  });

  it('defaults missing fields safely', () => {
    const iface = toNetworkInterface({});

    expect(iface.uid).toBe('');
    expect(iface.name).toBe('');
    expect(iface.addresses).toEqual([]);
    expect(iface.holderAvailableStatus).toBe('Unknown');
    expect(iface.createdAt).toBeInstanceOf(Date);
  });
});

describe('toNetworkInterfaceList', () => {
  it('maps every item in the list', () => {
    const list = toNetworkInterfaceList([
      baseRawNetworkInterface,
      { ...baseRawNetworkInterface, metadata: { name: 'second' } },
    ]);
    expect(list).toHaveLength(2);
    expect(list.map((i) => i.name)).toEqual(['storefront-abc123', 'second']);
  });

  it('returns an empty array for an empty list', () => {
    expect(toNetworkInterfaceList([])).toEqual([]);
  });
});

const baseRawNetworkService: RawNetworkService = {
  metadata: {
    uid: 'service-uid',
    name: 'storefront',
    creationTimestamp: '2026-08-25T14:18:34Z',
  },
  spec: {
    networkInterfaces: {
      selector: { matchLabels: { 'compute.datumapis.com/workload-name': 'storefront' } },
    },
    ports: [{ name: 'http', port: 8080, protocol: 'TCP' }],
  },
  status: {
    summary: { locations: 2, members: 4, healthy: 3 },
    locations: [
      { name: 'us-central-1', members: 2, healthy: 2, serving: true },
      { name: 'us-east-1', members: 2, healthy: 1, serving: false },
    ],
    conditions: [
      { type: 'MembersResolved', status: 'True', reason: 'Resolved', message: 'ok' },
      { type: 'Ready', status: 'True', reason: 'Ready', message: 'serving' },
    ],
  },
};

describe('toNetworkService', () => {
  it('maps a fully-populated raw NetworkService', () => {
    const service = toNetworkService(baseRawNetworkService);

    expect(service).toMatchObject({
      uid: 'service-uid',
      name: 'storefront',
      networkInterfaceSelector: {
        matchLabels: { 'compute.datumapis.com/workload-name': 'storefront' },
        matchExpressions: [],
      },
      ports: [{ name: 'http', port: 8080, protocol: 'TCP' }],
      summary: { locations: 2, members: 4, healthy: 3 },
      membersResolvedStatus: 'True',
      membersResolvedReason: 'Resolved',
      readyStatus: 'True',
      readyReason: 'Ready',
    });
    expect(service.locations).toHaveLength(2);
    expect(service.createdAt).toEqual(new Date('2026-08-25T14:18:34Z'));
  });

  it('maps matchExpressions, dropping any with a missing key or unrecognized operator', () => {
    const raw: RawNetworkService = {
      ...baseRawNetworkService,
      spec: {
        networkInterfaces: {
          selector: {
            matchExpressions: [
              { key: 'networking.datumapis.com/location', operator: 'In', values: ['us-central-1'] },
              { key: 'bogus', operator: 'NotARealOperator', values: ['x'] },
              { operator: 'Exists' },
            ],
          },
        },
      },
    };
    const service = toNetworkService(raw);

    expect(service.networkInterfaceSelector.matchExpressions).toEqual([
      { key: 'networking.datumapis.com/location', operator: 'In', values: ['us-central-1'] },
    ]);
  });

  it('defaults membersResolvedStatus and readyStatus to Unknown when conditions are absent', () => {
    const raw: RawNetworkService = { ...baseRawNetworkService, status: { conditions: [] } };
    const service = toNetworkService(raw);

    expect(service.membersResolvedStatus).toBe('Unknown');
    expect(service.readyStatus).toBe('Unknown');
  });

  it('handles a MultipleNetworks MembersResolved failure', () => {
    const raw: RawNetworkService = {
      ...baseRawNetworkService,
      status: {
        conditions: [
          {
            type: 'MembersResolved',
            status: 'False',
            reason: 'MultipleNetworks',
            message: 'spans 2 networks',
          },
        ],
      },
    };
    const service = toNetworkService(raw);

    expect(service.membersResolvedStatus).toBe('False');
    expect(service.membersResolvedReason).toBe('MultipleNetworks');
  });

  it('defaults missing fields safely', () => {
    const service = toNetworkService({});

    expect(service.uid).toBe('');
    expect(service.name).toBe('');
    expect(service.networkInterfaceSelector).toEqual({ matchLabels: {}, matchExpressions: [] });
    expect(service.ports).toEqual([]);
    expect(service.locations).toEqual([]);
    expect(service.summary).toEqual({ locations: 0, members: 0, healthy: 0 });
    expect(service.membersResolvedStatus).toBe('Unknown');
    expect(service.readyStatus).toBe('Unknown');
    expect(service.createdAt).toBeInstanceOf(Date);
  });
});

describe('toNetworkServiceList', () => {
  it('maps every item in the list', () => {
    const list = toNetworkServiceList([
      baseRawNetworkService,
      { ...baseRawNetworkService, metadata: { name: 'second' } },
    ]);
    expect(list).toHaveLength(2);
    expect(list.map((s) => s.name)).toEqual(['storefront', 'second']);
  });

  it('returns an empty array for an empty list', () => {
    expect(toNetworkServiceList([])).toEqual([]);
  });
});

const baseRawHTTPProxy: RawHTTPProxy = {
  metadata: {
    uid: 'proxy-uid',
    name: 'gw-public',
    creationTimestamp: '2026-08-25T14:18:34Z',
  },
  spec: {
    rules: [{ backends: [{ networkService: { name: 'storefront', port: 'http' } }] }],
  },
  status: {
    hostnames: ['app.example.com'],
    canonicalHostname: 'abc123.datumproxy.net',
    conditions: [{ type: 'Programmed', status: 'True', reason: 'Programmed', message: 'ok' }],
  },
};

describe('toHTTPProxy', () => {
  it('maps a fully-populated raw HTTPProxy', () => {
    const proxy = toHTTPProxy(baseRawHTTPProxy);

    expect(proxy).toMatchObject({
      uid: 'proxy-uid',
      name: 'gw-public',
      hostnames: ['app.example.com'],
      canonicalHostname: 'abc123.datumproxy.net',
      networkServiceNames: ['storefront'],
      programmedStatus: 'True',
      programmedReason: 'Programmed',
    });
    expect(proxy.createdAt).toEqual(new Date('2026-08-25T14:18:34Z'));
  });

  it('collects the distinct NetworkService names across every rule and backend', () => {
    const raw: RawHTTPProxy = {
      ...baseRawHTTPProxy,
      spec: {
        rules: [
          { backends: [{ networkService: { name: 'storefront', port: 'http' } }] },
          {
            backends: [
              { networkService: { name: 'storefront', port: 'http' } },
              { networkService: { name: 'checkout', port: 'http' } },
              {}, // a connector/instance/endpoint backend carries no networkService
            ],
          },
        ],
      },
    };
    expect(toHTTPProxy(raw).networkServiceNames).toEqual(['storefront', 'checkout']);
  });

  it('returns an empty networkServiceNames when no backend references a NetworkService', () => {
    const raw: RawHTTPProxy = { ...baseRawHTTPProxy, spec: { rules: [{ backends: [{}] }] } };
    expect(toHTTPProxy(raw).networkServiceNames).toEqual([]);
  });

  it('defaults programmedStatus to Unknown when the condition is absent', () => {
    const raw: RawHTTPProxy = { ...baseRawHTTPProxy, status: { conditions: [] } };
    expect(toHTTPProxy(raw).programmedStatus).toBe('Unknown');
  });

  it('defaults missing fields safely', () => {
    const proxy = toHTTPProxy({});

    expect(proxy.uid).toBe('');
    expect(proxy.name).toBe('');
    expect(proxy.hostnames).toEqual([]);
    expect(proxy.canonicalHostname).toBeUndefined();
    expect(proxy.networkServiceNames).toEqual([]);
    expect(proxy.programmedStatus).toBe('Unknown');
    expect(proxy.createdAt).toBeInstanceOf(Date);
  });
});

describe('toHTTPProxyList', () => {
  it('maps every item in the list', () => {
    const list = toHTTPProxyList([
      baseRawHTTPProxy,
      { ...baseRawHTTPProxy, metadata: { name: 'gw-internal' } },
    ]);
    expect(list).toHaveLength(2);
    expect(list.map((p) => p.name)).toEqual(['gw-public', 'gw-internal']);
  });

  it('returns an empty array for an empty list', () => {
    expect(toHTTPProxyList([])).toEqual([]);
  });
});

const baseRawNLB: RawNetworkLoadBalancer = {
  metadata: { uid: 'nlb-uid', name: 'edge-nlb', creationTimestamp: '2026-10-29T00:00:00Z' },
  spec: {
    networkRef: { name: 'default-vpc' },
    listeners: [
      { name: 'pg', protocol: 'TCP', port: 5432 },
      { name: 'game', protocol: 'UDP', port: 7777 },
    ],
  },
  status: {
    conditions: [{ type: 'Ready', status: 'False', reason: 'NoRoutes', message: 'No listener has a route.' }],
  },
};

describe('toNetworkLoadBalancer', () => {
  it('maps the network, listeners and Ready condition', () => {
    expect(toNetworkLoadBalancer(baseRawNLB)).toMatchObject({
      uid: 'nlb-uid',
      name: 'edge-nlb',
      createdAt: new Date('2026-10-29T00:00:00Z'),
      networkName: 'default-vpc',
      listeners: [
        { name: 'pg', protocol: 'TCP', port: 5432 },
        { name: 'game', protocol: 'UDP', port: 7777 },
      ],
      readyStatus: 'False',
      readyReason: 'NoRoutes',
      readyMessage: 'No listener has a route.',
    });
  });

  it('defaults to Unknown with no listeners when status and spec are empty', () => {
    const nlb = toNetworkLoadBalancer({ metadata: { name: 'bare' } });
    expect(nlb.readyStatus).toBe('Unknown');
    expect(nlb.listeners).toEqual([]);
    expect(nlb.networkName).toBeUndefined();
  });

  it('drops listeners with a protocol it does not know', () => {
    const nlb = toNetworkLoadBalancer({
      spec: { listeners: [{ name: 'odd', protocol: 'SCTP', port: 9 }, { name: 'dns', protocol: 'UDP', port: 53 }] },
    });
    expect(nlb.listeners.map((l) => l.name)).toEqual(['dns']);
  });
});

describe('toNetworkLoadBalancerList', () => {
  it('maps every item and leaves out one being deleted', () => {
    const list = toNetworkLoadBalancerList([
      baseRawNLB,
      { ...baseRawNLB, metadata: { name: 'going', deletionTimestamp: '2026-10-30T00:00:00Z' } },
    ]);
    expect(list.map((n) => n.name)).toEqual(['edge-nlb']);
  });

  it('returns an empty array for an empty list', () => {
    expect(toNetworkLoadBalancerList([])).toEqual([]);
  });
});

describe('toNetworkLoadBalancerRouteList', () => {
  it('maps parents and distinct backend names, leaving out a route being deleted', () => {
    const list = toNetworkLoadBalancerRouteList([
      {
        metadata: { uid: 'r1', name: 'edge-udp', resourceVersion: '7' },
        spec: {
          parentRefs: [{ name: 'edge-nlb', listener: 'udp-7777' }, { name: 'other', port: 53 }],
          backends: [
            { networkService: { name: 'game-server' } },
            { networkService: { name: 'game-server' } },
            {},
          ],
        },
      },
      { metadata: { name: 'going', deletionTimestamp: '2026-10-30T00:00:00Z' } },
    ]);
    expect(list).toEqual([
      {
        uid: 'r1',
        name: 'edge-udp',
        resourceVersion: '7',
        parents: [
          { name: 'edge-nlb', listener: 'udp-7777', port: undefined },
          { name: 'other', listener: undefined, port: 53 },
        ],
        backendNames: ['game-server'],
        backends: [
          { name: 'game-server', port: '', healthCheck: undefined },
          { name: 'game-server', port: '', healthCheck: undefined },
        ],
        parentStatuses: [],
      },
    ]);
  });

  it('maps backends with health checks and rolls each parent status into one', () => {
    const [route] = toNetworkLoadBalancerRouteList([
      {
        metadata: { name: 'edge-udp' },
        spec: {
          backends: [
            {
              networkService: { name: 'game-server', port: 'game' },
              healthCheck: { protocol: 'TCP', port: 'health' },
            },
          ],
        },
        status: {
          parents: [
            {
              name: 'edge-nlb',
              listener: 'udp-7777',
              effective: true,
              conditions: [
                { type: 'Accepted', status: 'True', reason: 'Attached' },
                { type: 'ResolvedRefs', status: 'True', reason: 'Resolved' },
                { type: 'Programmed', status: 'True', reason: 'Programmed' },
              ],
            },
            {
              name: 'other',
              listener: 'udp-53',
              conditions: [
                { type: 'Accepted', status: 'True', reason: 'Attached' },
                {
                  type: 'ResolvedRefs',
                  status: 'False',
                  reason: 'HealthCheckInvalid',
                  message: 'needs a TCP port',
                },
              ],
            },
          ],
        },
      },
    ]);
    expect(route.backends).toEqual([
      { name: 'game-server', port: 'game', healthCheck: { protocol: 'TCP', port: 'health' } },
    ]);
    expect(route.parentStatuses).toEqual([
      { name: 'edge-nlb', listener: 'udp-7777', effective: true, status: 'True', reason: undefined, message: undefined },
      {
        name: 'other',
        listener: 'udp-53',
        effective: false,
        status: 'False',
        reason: 'HealthCheckInvalid',
        message: 'needs a TCP port',
      },
    ]);
  });
});

describe('toNetworkLoadBalancer status', () => {
  it('keeps allocated addresses per endpoint and drops pending ones', () => {
    const nlb = toNetworkLoadBalancer({
      spec: { listeners: [{ name: 'udp-7777', protocol: 'UDP', port: 7777, addressRefs: ['udp-7777'] }] },
      status: {
        endpoints: [
          {
            requestName: 'udp-7777',
            location: 'iad',
            addresses: [{ family: 'IPv6', value: '2001:db8::3' }, { family: 'IPv4' }],
          },
        ],
      },
    });
    expect(nlb.listeners[0].addressRef).toBe('udp-7777');
    expect(nlb.endpoints).toEqual([
      { requestName: 'udp-7777', location: 'iad', addresses: [{ family: 'IPv6', value: '2001:db8::3' }] },
    ]);
  });
});

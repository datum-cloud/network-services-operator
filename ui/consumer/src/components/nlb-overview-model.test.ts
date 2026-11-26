import {
  cleanConditionMessage,
  ipv4Issues,
  nlbIpv4State,
  overviewSummary,
  toListenerRows,
} from './nlb-overview-model';
import type { NetworkLoadBalancer, NetworkLoadBalancerRoute, NetworkService } from '../schema';
import { describe, expect, it } from 'vitest';

type ParentStatus = NetworkLoadBalancerRoute['parentStatuses'][number];

function makeNlb(overrides: Partial<NetworkLoadBalancer> = {}): NetworkLoadBalancer {
  return {
    uid: 'nlb',
    name: 'edge-nlb',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    networkName: 'default',
    listeners: [{ name: 'udp-7777', protocol: 'UDP', port: 7777, addressRef: 'udp-7777' }],
    endpoints: [
      {
        requestName: 'udp-7777',
        location: 'iad',
        addresses: [{ family: 'IPv6', value: '2001:db8::3' }],
      },
      {
        requestName: 'udp-7777',
        location: 'lhr',
        addresses: [{ family: 'IPv6', value: '2001:db8::4' }],
      },
    ],
    readyStatus: 'True',
    ...overrides,
  };
}

function parent(overrides: Partial<ParentStatus> = {}): ParentStatus {
  return { name: 'edge-nlb', listener: 'udp-7777', effective: true, status: 'True', ...overrides };
}

function makeRoute(
  name: string,
  parentStatuses: ParentStatus[],
  overrides: Partial<NetworkLoadBalancerRoute> = {}
): NetworkLoadBalancerRoute {
  return {
    uid: name,
    name,
    parents: [{ name: 'edge-nlb', listener: 'udp-7777' }],
    backendNames: ['game-server'],
    backends: [
      { name: 'game-server', port: 'game', healthCheck: { protocol: 'TCP', port: 'health' } },
    ],
    parentStatuses,
    ...overrides,
  };
}

function makeService(healthy: number, members: number): NetworkService {
  return {
    uid: 'svc',
    name: 'game-server',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    networkInterfaceSelector: { matchLabels: {}, matchExpressions: [] },
    ports: [],
    summary: { locations: 2, members, healthy },
    locations: [],
    membersResolvedStatus: 'True',
    readyStatus: 'True',
  };
}

function statusOf(
  nlb: NetworkLoadBalancer,
  routes: NetworkLoadBalancerRoute[],
  services: NetworkService[]
) {
  return toListenerRows(nlb, routes, services)[0].status.label;
}

describe('toListenerRows', () => {
  it('joins a serving listener with its address, route, backend and service', () => {
    const [row] = toListenerRows(makeNlb(), [makeRoute('r', [parent()])], [makeService(6, 6)]);
    expect(row).toMatchObject({
      address: '2001:db8::3',
      locations: 2,
      route: { name: 'r' },
      backend: { name: 'game-server', port: 'game' },
      service: { name: 'game-server' },
      status: { label: 'Serving', type: 'success' },
    });
  });

  it('flags a listener no route attaches to', () => {
    expect(statusOf(makeNlb(), [], [])).toBe('No route');
  });

  it('shows why the route was refused', () => {
    const route = makeRoute('r', [
      parent({ status: 'False', reason: 'HealthCheckInvalid', message: 'needs a TCP port' }),
    ]);
    const [row] = toListenerRows(makeNlb(), [route], []);
    expect(row.status).toMatchObject({
      label: 'Health check invalid',
      type: 'danger',
      detail: 'needs a TCP port',
    });
  });

  it('shows a route the controller has not reported on yet as attaching', () => {
    expect(statusOf(makeNlb(), [makeRoute('r', [])], [])).toBe('Attaching');
  });

  it('matches a route that picked the listener by port', () => {
    const route = makeRoute('r', [], { parents: [{ name: 'edge-nlb', port: 7777 }] });
    expect(toListenerRows(makeNlb(), [route], [])[0].route?.name).toBe('r');
  });

  it('uses the effective route when several attach', () => {
    const rows = toListenerRows(
      makeNlb(),
      [makeRoute('newer', [parent({ effective: false })]), makeRoute('older', [parent()])],
      [makeService(1, 1)]
    );
    expect(rows[0].route?.name).toBe('older');
    expect(rows[0].status.label).toBe('Serving');
  });

  it('shows an address with no allocated value as pending', () => {
    const nlb = makeNlb({
      endpoints: [{ requestName: 'udp-7777', location: 'iad', addresses: [] }],
    });
    const [row] = toListenerRows(nlb, [makeRoute('r', [parent()])], [makeService(1, 1)]);
    expect(row.address).toBeUndefined();
    expect(row.status.label).toBe('Address pending');
  });

  it.each([
    [0, 3, 'Unhealthy'],
    [5, 6, 'Degraded'],
  ])('shows a backend with %i of %i healthy as %s', (healthy, members, label) => {
    expect(statusOf(makeNlb(), [makeRoute('r', [parent()])], [makeService(healthy, members)])).toBe(
      label
    );
  });
});

describe('overviewSummary', () => {
  it('counts listeners when all are serving, and asks for attention otherwise', () => {
    const serving = toListenerRows(makeNlb(), [makeRoute('r', [parent()])], [makeService(1, 1)]);
    expect(overviewSummary(serving).label).toBe('Forwarding on 1 listener');
    expect(overviewSummary(toListenerRows(makeNlb(), [], [])).label).toBe('Needs attention');
  });
});

describe('listener status while the data plane catches up', () => {
  it('shows a new listener as programming, not a fault', () => {
    for (const status of ['False', 'Unknown'] as const) {
      const route = makeRoute('r', [parent({ status, reason: 'AwaitingDataPlane' })]);
      const [row] = toListenerRows(makeNlb(), [route], []);
      expect(row.status).toMatchObject({ label: 'Programming', type: 'muted', pending: true });
      expect(overviewSummary([row])).toMatchObject({ label: 'Starting up' });
    }
  });

  it('spells out a reason it has no copy for', () => {
    const route = makeRoute('r', [parent({ status: 'False', reason: 'SomethingNewBroke' })]);
    expect(toListenerRows(makeNlb(), [route], [])[0].status.label).toBe('Something new broke');
  });
});

const QUOTA_MESSAGE =
  'ipclaims.ipam.miloapis.com "c" is forbidden: You\'ve reached your quota for this resource type (1/1).';

type AddressStatus = NonNullable<NetworkLoadBalancer['endpoints'][number]['addressStatuses']>[number];

function v4(overrides: Partial<AddressStatus> = {}): AddressStatus {
  return { family: 'IPv4', conditions: [], ...overrides };
}

function failed(message: string): AddressStatus {
  return v4({ conditions: [{ type: 'Allocated', status: 'False', reason: 'AllocationFailed', message }] });
}

function dualStack(iad: AddressStatus, lhr: AddressStatus): NetworkLoadBalancer {
  const ep = (location: string, v6: string, ipv4: AddressStatus) => ({
    requestName: 'udp-7777',
    location,
    addresses: [
      ...(ipv4.value ? [{ family: 'IPv4', value: ipv4.value }] : []),
      { family: 'IPv6', value: v6 },
    ],
    addressStatuses: [ipv4, { family: 'IPv6', value: v6, conditions: [] }],
  });
  return makeNlb({
    addressFamilies: { 'udp-7777': ['IPv6', 'IPv4'] },
    endpoints: [ep('iad', '2001:db8::3', iad), ep('lhr', '2001:db8::4', lhr)],
  });
}

describe('IPv4 on listener rows', () => {
  it('picks each address by family, whatever order the status lists them in', () => {
    const [row] = toListenerRows(dualStack(v4({ value: '203.0.113.7' }), v4({ value: '203.0.113.8' })), [], []);
    expect(row).toMatchObject({
      address: '2001:db8::3',
      ipv6Address: '2001:db8::3',
      ipv4Address: '203.0.113.7',
      ipv4Requested: true,
      ipv4: 'allocated',
    });
  });

  it('is pending when IPv4 is requested and not yet allocated', () => {
    const [row] = toListenerRows(makeNlb({ addressFamilies: { 'udp-7777': ['IPv6', 'IPv4'] } }), [], []);
    expect(row).toMatchObject({ ipv4Requested: true, ipv4: 'pending', ipv4Address: undefined });
  });

  it('is pending while Allocated is still Unknown', () => {
    const allocating = v4({ conditions: [{ type: 'Allocated', status: 'Unknown', reason: 'Pending' }] });
    expect(toListenerRows(dualStack(allocating, allocating), [], [])[0].ipv4).toBe('pending');
  });

  it('marks a quota refusal, keeping the address another location got', () => {
    const [row] = toListenerRows(dualStack(v4({ value: '203.0.113.7' }), failed(QUOTA_MESSAGE)), [], []);
    expect(row).toMatchObject({ ipv4: 'refused', ipv4Address: '203.0.113.7' });
  });

  it('marks an allocation failure that is not about quota', () => {
    const [row] = toListenerRows(dualStack(failed('pool exhausted'), failed('pool exhausted')), [], []);
    expect(row.ipv4).toBe('failed');
  });

  it('leaves an IPv6-only listener as it was', () => {
    const [row] = toListenerRows(makeNlb(), [], []);
    expect(row).toMatchObject({ address: '2001:db8::3', ipv4Requested: false, ipv4: 'none' });
    expect(nlbIpv4State(makeNlb())).toBe('none');
  });
});

describe('ipv4Issues', () => {
  it('lists refused locations and non-quota failures with their cleaned message', () => {
    const nlb = dualStack(
      failed('ipclaims.ipam.miloapis.com "c" is forbidden: pool exhausted'),
      failed(QUOTA_MESSAGE)
    );
    expect(ipv4Issues(nlb)).toEqual({
      refused: ['lhr'],
      failed: [{ location: 'iad', message: 'pool exhausted' }],
    });
    expect(nlbIpv4State(nlb)).toBe('refused');
  });

  it('is empty when every IPv4 address is allocated', () => {
    const nlb = dualStack(v4({ value: '203.0.113.7' }), v4({ value: '203.0.113.8' }));
    expect(ipv4Issues(nlb)).toEqual({ refused: [], failed: [] });
    expect(nlbIpv4State(nlb)).toBe('allocated');
  });
});

describe('cleanConditionMessage', () => {
  it('drops the API refusal lead and keeps plain messages', () => {
    expect(cleanConditionMessage('x "y" is forbidden: no')).toBe('no');
    expect(cleanConditionMessage('plain words')).toBe('plain words');
    expect(cleanConditionMessage('  ')).toBeUndefined();
  });
});

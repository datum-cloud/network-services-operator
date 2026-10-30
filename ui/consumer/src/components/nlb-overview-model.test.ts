import { overviewSummary, toListenerRows } from './nlb-overview-model';
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

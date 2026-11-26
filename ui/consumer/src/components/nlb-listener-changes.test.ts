import {
  ipv4Locations,
  listenerFamilies,
  listenerHasIpv4,
  planDeleteListener,
  routeOnlyServes,
  routesForListener,
  toAddressFamiliesPatch,
  toEditListenerPatch,
  toRouteBackendPatch,
} from './nlb-listener-changes';
import type { NetworkLoadBalancer, NetworkLoadBalancerRoute } from '../schema';
import { describe, expect, it } from 'vitest';

const nlb: NetworkLoadBalancer = {
  uid: 'nlb',
  name: 'edge-nlb',
  resourceVersion: '42',
  createdAt: new Date('2026-01-01T00:00:00Z'),
  listeners: [
    { name: 'tcp-80', protocol: 'TCP', port: 80, addressRef: 'tcp-80', index: 0 },
    // index 1 is a listener the portal doesn't model, e.g. TLS.
    { name: 'udp-7777', protocol: 'UDP', port: 7777, addressRef: 'udp-7777', index: 2 },
  ],
  addressNames: ['tcp-80', 'tls-443', 'udp-7777'],
  endpoints: [],
  readyStatus: 'True',
};
const [tcp, udp] = nlb.listeners;

function makeRoute(
  name: string,
  parents: NetworkLoadBalancerRoute['parents']
): NetworkLoadBalancerRoute {
  return {
    uid: name,
    name,
    resourceVersion: '7',
    parents,
    backendNames: ['game-server'],
    backends: [{ name: 'game-server', port: 'game' }],
    parentStatuses: [],
  };
}

describe('routesForListener', () => {
  it('finds routes naming the listener, or picking it by port', () => {
    const routes = [
      makeRoute('by-name', [{ name: 'edge-nlb', listener: 'udp-7777' }]),
      makeRoute('by-port', [{ name: 'edge-nlb', port: 7777 }]),
      makeRoute('other-nlb', [{ name: 'other', listener: 'udp-7777' }]),
      makeRoute('other-listener', [{ name: 'edge-nlb', listener: 'tcp-80' }]),
    ];
    expect(routesForListener('edge-nlb', udp, routes).map((r) => r.name)).toEqual([
      'by-name',
      'by-port',
    ]);
  });
});

describe('planDeleteListener', () => {
  it('removes the listener and its address at their own indexes, checking each first', () => {
    const plan = planDeleteListener(nlb, udp, []);
    expect(plan.patch).toEqual([
      { op: 'test', path: '/spec/listeners/2/name', value: 'udp-7777' },
      { op: 'remove', path: '/spec/listeners/2' },
      { op: 'test', path: '/spec/exposure/addresses/2/name', value: 'udp-7777' },
      { op: 'remove', path: '/spec/exposure/addresses/2' },
    ]);
    expect(plan.releasedAddress).toBe('udp-7777');
  });

  it('keeps an address another listener still uses', () => {
    const shared = {
      ...nlb,
      listeners: [tcp, { ...udp, addressRef: 'tcp-80' }],
    };
    const plan = planDeleteListener(shared, shared.listeners[1], []);
    expect(plan.releasedAddress).toBeUndefined();
    expect(plan.patch).not.toContainEqual(
      expect.objectContaining({ path: expect.stringContaining('addresses') })
    );
  });

  it('deletes routes only this listener uses and detaches it from shared ones', () => {
    const plan = planDeleteListener(nlb, udp, [
      makeRoute('only', [{ name: 'edge-nlb', listener: 'udp-7777' }]),
      makeRoute('shared', [
        { name: 'edge-nlb', listener: 'udp-7777' },
        { name: 'edge-nlb', listener: 'tcp-80' },
      ]),
    ]);
    expect(plan.deleteRoutes.map((r) => r.name)).toEqual(['only']);
    expect(plan.detachRoutes).toEqual([
      {
        route: expect.objectContaining({ name: 'shared' }),
        parents: [{ name: 'edge-nlb', listener: 'tcp-80' }],
      },
    ]);
  });
});

describe('toEditListenerPatch', () => {
  it('does nothing when the protocol and port are unchanged', () => {
    expect(toEditListenerPatch(nlb, udp, { protocol: 'UDP', port: 7777 })).toEqual([]);
  });

  it('replaces the protocol and port in place, keeping the name', () => {
    expect(toEditListenerPatch(nlb, tcp, { protocol: 'UDP', port: 53 })).toEqual([
      { op: 'test', path: '/spec/listeners/0/name', value: 'tcp-80' },
      { op: 'replace', path: '/spec/listeners/0/protocol', value: 'UDP' },
      { op: 'replace', path: '/spec/listeners/0/port', value: 53 },
    ]);
  });
});

describe('toRouteBackendPatch', () => {
  it('points the route at the new backend and names the listener instead of its port', () => {
    const route = makeRoute('by-port', [{ name: 'edge-nlb', port: 7777 }]);
    expect(routeOnlyServes(route, 'edge-nlb', udp)).toBe(true);
    expect(
      toRouteBackendPatch(route, 'edge-nlb', udp, {
        protocol: 'UDP',
        port: 7777,
        backend: 'dns',
        backendPort: 'dns',
        healthPort: 'dns-tcp',
      })
    ).toEqual({
      metadata: { resourceVersion: '7' },
      spec: {
        parentRefs: [{ name: 'edge-nlb', listener: 'udp-7777' }],
        backends: [
          {
            networkService: { name: 'dns', port: 'dns' },
            healthCheck: { protocol: 'TCP', port: 'dns-tcp' },
          },
        ],
      },
    });
  });
});

describe('address families', () => {
  const dual = {
    ...nlb,
    addressFamilies: { 'tcp-80': ['IPv6'], 'udp-7777': ['IPv6', 'IPv4'] },
    endpoints: [
      {
        requestName: 'udp-7777',
        location: 'us-east-1',
        addresses: [
          { family: 'IPv6', value: '2001:db8::1' },
          { family: 'IPv4', value: '203.0.113.7' },
        ],
      },
      { requestName: 'udp-7777', location: 'eu-west-1', addresses: [{ family: 'IPv6', value: '2001:db8::2' }] },
      { requestName: 'tcp-80', location: 'us-east-1', addresses: [{ family: 'IPv6', value: '2001:db8::3' }] },
    ],
  } satisfies NetworkLoadBalancer & { addressFamilies: Record<string, ('IPv4' | 'IPv6')[]> };

  it('reads an address without families as IPv6 only', () => {
    expect(listenerFamilies(nlb, udp)).toEqual(['IPv6']);
    expect(listenerHasIpv4(nlb, udp)).toBe(false);
    expect(listenerHasIpv4(dual, udp)).toBe(true);
    expect(listenerHasIpv4(dual, tcp)).toBe(false);
  });

  it('lists only the locations holding an allocated IPv4 address', () => {
    expect(ipv4Locations(dual, udp)).toEqual(['us-east-1']);
    expect(ipv4Locations(dual, tcp)).toEqual([]);
  });

  it('adds IPv4 to the listener’s own address, guarded by its name and families', () => {
    expect(toAddressFamiliesPatch(nlb, udp, true)).toEqual([
      { op: 'test', path: '/spec/exposure/addresses/2/name', value: 'udp-7777' },
      { op: 'test', path: '/spec/exposure/addresses/2/families', value: ['IPv6'] },
      { op: 'replace', path: '/spec/exposure/addresses/2/families', value: ['IPv6', 'IPv4'] },
    ]);
  });

  it('removes IPv4 back to IPv6 only', () => {
    expect(toAddressFamiliesPatch(dual, udp, false)).toEqual([
      { op: 'test', path: '/spec/exposure/addresses/2/name', value: 'udp-7777' },
      { op: 'test', path: '/spec/exposure/addresses/2/families', value: ['IPv6', 'IPv4'] },
      { op: 'replace', path: '/spec/exposure/addresses/2/families', value: ['IPv6'] },
    ]);
  });

  it('patches nothing when the families don’t change or the address is unknown', () => {
    expect(toAddressFamiliesPatch(nlb, udp, false)).toEqual([]);
    expect(toAddressFamiliesPatch(dual, udp, true)).toEqual([]);
    expect(toAddressFamiliesPatch({ ...nlb, addressNames: [] }, udp, true)).toEqual([]);
  });
});

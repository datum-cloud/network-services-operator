import {
  backendPortOptions,
  healthPortOptions,
  nlbCreateSchema,
  SERVING_PORT,
  toNlbCreateErrors,
  toNlbCreateRequests,
  type NlbCreateValues,
} from './nlb-create-form';
import { ApiError } from '../lib/api';
import type { NetworkService } from '../schema';
import { describe, expect, it } from 'vitest';

function makeService(ports: NetworkService['ports']): NetworkService {
  return {
    uid: 'svc',
    name: 'game-server',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    networkInterfaceSelector: { matchLabels: {}, matchExpressions: [] },
    ports,
    summary: { locations: 1, members: 1, healthy: 1 },
    locations: [],
    membersResolvedStatus: 'True',
    readyStatus: 'True',
  };
}

const tcpValues: NlbCreateValues = {
  name: 'db',
  network: 'default',
  protocol: 'TCP',
  port: 5432,
  backend: 'postgres',
  backendPort: 'pg',
  healthPort: SERVING_PORT,
};

const udpValues: NlbCreateValues = {
  name: 'edge-nlb',
  network: 'default',
  protocol: 'UDP',
  port: 7777,
  backend: 'game-server',
  backendPort: 'game',
  healthPort: 'health',
};

describe('toNlbCreateRequests', () => {
  it('builds a TCP NLB and a route that probes the serving port', () => {
    expect(toNlbCreateRequests(tcpValues)).toEqual({
      nlb: {
        apiVersion: 'networking.datumapis.com/v1alpha',
        kind: 'NetworkLoadBalancer',
        metadata: { name: 'db', namespace: 'default' },
        spec: {
          networkRef: { name: 'default' },
          exposure: { addresses: [{ name: 'tcp-5432', families: ['IPv6'] }] },
          listeners: [{ name: 'tcp-5432', protocol: 'TCP', port: 5432, addressRefs: ['tcp-5432'] }],
        },
      },
      route: {
        apiVersion: 'networking.datumapis.com/v1alpha',
        kind: 'NetworkLoadBalancerRoute',
        metadata: { name: 'db-tcp-5432', namespace: 'default' },
        spec: {
          parentRefs: [{ name: 'db', listener: 'tcp-5432' }],
          backends: [
            { networkService: { name: 'postgres', port: 'pg' }, healthCheck: { protocol: 'TCP' } },
          ],
        },
      },
    });
  });

  it('builds a UDP NLB whose route probes a named TCP port', () => {
    const { nlb, route } = toNlbCreateRequests(udpValues);
    expect(nlb.spec).toMatchObject({
      listeners: [{ name: 'udp-7777', protocol: 'UDP', port: 7777, addressRefs: ['udp-7777'] }],
    });
    expect(route).toMatchObject({
      metadata: { name: 'edge-nlb-udp-7777' },
      spec: {
        parentRefs: [{ name: 'edge-nlb', listener: 'udp-7777' }],
        backends: [
          {
            networkService: { name: 'game-server', port: 'game' },
            healthCheck: { protocol: 'TCP', port: 'health' },
          },
        ],
      },
    });
  });
});

describe('nlbCreateSchema', () => {
  it('accepts a TCP listener that probes the serving port', () => {
    expect(nlbCreateSchema.safeParse(tcpValues).success).toBe(true);
  });

  it.each([undefined, SERVING_PORT])(
    'needs a TCP probe port on a UDP listener (healthPort %s)',
    (healthPort) => {
      const result = nlbCreateSchema.safeParse({ ...udpValues, healthPort });
      expect(result.success).toBe(false);
      expect(result.error?.issues[0]).toMatchObject({ path: ['healthPort'] });
    }
  );
});

describe('port options', () => {
  const service = makeService([
    { name: 'game', port: 7777, protocol: 'UDP' },
    { name: 'health', port: 8080, protocol: 'TCP' },
    { name: 'admin', port: 9000 },
  ]);

  it('offers only the backend ports on the listener protocol, treating no protocol as TCP', () => {
    expect(backendPortOptions(service, 'UDP').map((p) => p.name)).toEqual(['game']);
    expect(backendPortOptions(service, 'TCP').map((p) => p.name)).toEqual(['health', 'admin']);
  });

  it('offers only TCP ports to probe', () => {
    expect(healthPortOptions(service).map((p) => p.name)).toEqual(['health', 'admin']);
  });
});

describe('toNlbCreateErrors', () => {
  it('puts a name conflict on the name field', () => {
    expect(toNlbCreateErrors(new ApiError(409, 'already exists'), 'nlb')).toEqual({
      fields: { name: 'A load balancer with this name already exists.' },
    });
  });

  it('puts each NLB cause next to its field', () => {
    const error = new ApiError(422, 'invalid', [
      { field: 'spec.listeners[0].port', message: 'port is out of range' },
      { field: 'spec.networkRef.name', message: 'network not found' },
    ]);
    expect(toNlbCreateErrors(error, 'nlb')).toEqual({
      fields: { port: 'port is out of range', network: 'network not found' },
      general: undefined,
    });
  });

  it('puts route causes on the backend fields', () => {
    const error = new ApiError(422, 'invalid', [
      { field: 'spec.backends[0].networkService.port', message: 'no such port' },
      { field: 'spec.backends[0].healthCheck.protocol', message: 'TCP only' },
    ]);
    expect(toNlbCreateErrors(error, 'route').fields).toEqual({
      backendPort: 'no such port',
      healthPort: 'TCP only',
    });
  });

  it('shows causes on fields the dialog fills in, and errors with no causes, for the whole form', () => {
    const error = new ApiError(403, 'forbidden', [
      { field: 'spec.exposure.addresses[0].families', message: 'IPv4 is unavailable' },
    ]);
    expect(toNlbCreateErrors(error, 'nlb')).toEqual({ fields: {}, general: 'IPv4 is unavailable' });
    expect(toNlbCreateErrors(new ApiError(500, 'boom'), 'route')).toEqual({
      fields: {},
      general: 'boom',
    });
  });
});

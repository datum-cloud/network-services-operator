import { AddListenerDialog, checkListener } from './add-listener-dialog';
import { toAddListenerRequests, toNlbCreateErrors } from './nlb-create-form';
import type { ListenerRow } from './nlb-overview-model';
import * as api from '../lib/api';
import type { NetworkLoadBalancer, NetworkService } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Radix Select is slow to unmount in jsdom; see create-network-service-dialog.test.tsx.
const SELECT_TEST_TIMEOUT = 30_000;

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetworkServices: vi.fn() };
});

const nlb: NetworkLoadBalancer = {
  uid: 'nlb',
  name: 'edge-nlb',
  resourceVersion: '42',
  createdAt: new Date('2026-01-01T00:00:00Z'),
  networkName: 'default',
  listeners: [{ name: 'tcp-80', protocol: 'TCP', port: 80, addressRef: 'tcp-80' }],
  endpoints: [],
  readyStatus: 'True',
};

const game: NetworkService = {
  uid: 'game',
  name: 'game-server',
  createdAt: new Date('2026-01-01T00:00:00Z'),
  networkInterfaceSelector: { matchLabels: {}, matchExpressions: [] },
  ports: [
    { name: 'game', port: 7777, protocol: 'UDP' },
    { name: 'health', port: 8080, protocol: 'TCP' },
  ],
  summary: { locations: 1, members: 1, healthy: 1 },
  locations: [],
  membersResolvedStatus: 'True',
  readyStatus: 'True',
};

const originalFetch = globalThis.fetch;

beforeEach(() => {
  vi.mocked(api.useNetworkServices).mockReturnValue({
    data: [game],
    isLoading: false,
    error: null,
  } as never);
});

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe('checkListener', () => {
  it('refuses a protocol and port the NLB already listens on', () => {
    expect(checkListener({ protocol: 'TCP', port: 80 }, nlb).fields.port).toBe(
      'This load balancer already has a TCP 80 listener.'
    );
    expect(checkListener({ protocol: 'UDP', port: 80 }, nlb).fields).toEqual({});
  });

  it('refuses a 17th listener', () => {
    const full = {
      ...nlb,
      listeners: Array.from({ length: 16 }, (_, i) => ({
        name: `tcp-${i + 1}`,
        protocol: 'TCP' as const,
        port: i + 1,
      })),
    };
    expect(checkListener({ protocol: 'TCP', port: 9000 }, full).general).toMatch(/at most 16/);
  });
});

describe('toAddListenerRequests', () => {
  it('appends an address and a listener after testing the resourceVersion, and builds the route', () => {
    const requests = toAddListenerRequests(nlb, {
      protocol: 'UDP',
      port: 7777,
      backend: 'game-server',
      backendPort: 'game',
      healthPort: 'health',
    });
    expect(requests.patch).toEqual([
      { op: 'test', path: '/metadata/resourceVersion', value: '42' },
      {
        op: 'add',
        path: '/spec/exposure/addresses/-',
        value: { name: 'udp-7777', families: ['IPv6'] },
      },
      {
        op: 'add',
        path: '/spec/listeners/-',
        value: { name: 'udp-7777', protocol: 'UDP', port: 7777, addressRefs: ['udp-7777'] },
      },
    ]);
    expect(requests.route).toMatchObject({
      metadata: { name: 'edge-nlb-udp-7777' },
      spec: { parentRefs: [{ name: 'edge-nlb', listener: 'udp-7777' }] },
    });
  });

  it('puts an error on the added listener next to the form field', () => {
    const error = new api.ApiError(422, 'invalid', [
      { field: 'spec.listeners[1].port', message: 'port is reserved' },
    ]);
    expect(toNlbCreateErrors(error, 'nlb').fields).toEqual({ port: 'port is reserved' });
  });
});

describe('AddListenerDialog', () => {
  function renderDialog() {
    const onOpenChange = vi.fn();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <AddListenerDialog projectId="demo-project" nlb={nlb} open onOpenChange={onOpenChange} />
      </QueryClientProvider>
    );
    return { onOpenChange };
  }

  // Comboboxes in form order: protocol, backend, backend port, probe port.
  function choose(index: number, option: string) {
    fireEvent.click(screen.getAllByRole('combobox')[index]);
    fireEvent.click(screen.getByRole('option', { name: option }));
  }

  it(
    'patches the NLB, then creates the route',
    async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      const { onOpenChange } = renderDialog();

      choose(0, 'UDP');
      fireEvent.change(screen.getByPlaceholderText('e.g. 7777'), { target: { value: '7777' } });
      choose(1, 'game-server');
      choose(2, 'game · 7777/UDP');
      choose(3, 'health · 8080/TCP');
      fireEvent.click(screen.getByRole('button', { name: 'Add listener' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      const [[patchUrl, patch], [routeUrl, route]] = fetchMock.mock.calls as [
        string,
        RequestInit,
      ][];
      expect(patchUrl).toMatch(/\/networkloadbalancers\/edge-nlb$/);
      expect(patch.method).toBe('PATCH');
      expect((patch.headers as Record<string, string>)['Content-Type']).toBe(
        'application/json-patch+json'
      );
      expect(JSON.parse(patch.body as string)).toHaveLength(3);
      expect(routeUrl).toMatch(/\/networkloadbalancerroutes$/);
      expect(JSON.parse(route.body as string).metadata.name).toBe('edge-nlb-udp-7777');
      await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'refuses a listener the NLB already has without calling the API',
    async () => {
      const fetchMock = vi.fn();
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderDialog();

      fireEvent.change(screen.getByPlaceholderText('e.g. 7777'), { target: { value: '80' } });
      choose(1, 'game-server');
      choose(2, 'health · 8080/TCP');
      fireEvent.click(screen.getByRole('button', { name: 'Add listener' }));

      expect(
        await screen.findByText('This load balancer already has a TCP 80 listener.')
      ).toBeInTheDocument();
      expect(fetchMock).not.toHaveBeenCalled();
    },
    SELECT_TEST_TIMEOUT
  );
});

describe('AddListenerDialog editing', () => {
  const dns: NetworkService = {
    ...game,
    uid: 'dns',
    name: 'dns',
    ports: [
      { name: 'dns', port: 53, protocol: 'UDP' },
      { name: 'dns-tcp', port: 53, protocol: 'TCP' },
    ],
  };

  const route = {
    uid: 'r',
    name: 'edge-nlb-udp-7777',
    resourceVersion: '5',
    parents: [{ name: 'edge-nlb', listener: 'udp-7777' }],
    backendNames: ['game-server'],
    backends: [
      { name: 'game-server', port: 'game', healthCheck: { protocol: 'TCP', port: 'health' } },
    ],
    parentStatuses: [],
  };

  const udpListener = {
    name: 'udp-7777',
    protocol: 'UDP' as const,
    port: 7777,
    addressRef: 'udp-7777',
    index: 1,
  };
  const withUdp: NetworkLoadBalancer = { ...nlb, listeners: [...nlb.listeners, udpListener] };

  function row(overrides: Partial<ListenerRow> = {}): ListenerRow {
    return {
      listener: udpListener,
      locations: 0,
      route,
      backend: route.backends[0],
      status: { label: 'Serving', type: 'success' },
      ...overrides,
    };
  }

  function renderEdit(editing: ListenerRow) {
    vi.mocked(api.useNetworkServices).mockReturnValue({
      data: [game, dns],
      isLoading: false,
      error: null,
    } as never);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <AddListenerDialog
          projectId="demo-project"
          nlb={withUdp}
          open
          onOpenChange={vi.fn()}
          editing={editing}
        />
      </QueryClientProvider>
    );
  }

  function choose(index: number, option: string) {
    fireEvent.click(screen.getAllByRole('combobox')[index]);
    fireEvent.click(screen.getByRole('option', { name: option }));
  }

  it(
    'only patches the route when just the backend changes',
    async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderEdit(row());

      choose(1, 'dns');
      choose(2, 'dns · 53/UDP');
      choose(3, 'dns-tcp · 53/TCP');
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
      const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
      expect(url).toMatch(/\/networkloadbalancerroutes\/edge-nlb-udp-7777$/);
      expect((init.headers as Record<string, string>)['Content-Type']).toBe(
        'application/merge-patch+json'
      );
      expect(JSON.parse(init.body as string).spec.backends).toEqual([
        {
          networkService: { name: 'dns', port: 'dns' },
          healthCheck: { protocol: 'TCP', port: 'dns-tcp' },
        },
      ]);
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'patches the listener in place when its port changes, then the route',
    async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderEdit(row());

      fireEvent.change(screen.getByPlaceholderText('e.g. 7777'), { target: { value: '7778' } });
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      const [[nlbUrl, nlbInit], [routeUrl]] = fetchMock.mock.calls as [string, RequestInit][];
      expect(nlbUrl).toMatch(/\/networkloadbalancers\/edge-nlb$/);
      expect(JSON.parse(nlbInit.body as string)).toContainEqual({
        op: 'replace',
        path: '/spec/listeners/1/port',
        value: 7778,
      });
      expect(routeUrl).toMatch(/\/networkloadbalancerroutes\/edge-nlb-udp-7777$/);
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'won’t change a route that also serves other listeners',
    async () => {
      const fetchMock = vi.fn();
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderEdit(
        row({
          route: {
            ...route,
            parents: [...route.parents, { name: 'edge-nlb', listener: 'tcp-80' }],
          },
        })
      );

      fireEvent.click(screen.getByRole('button', { name: 'Save' }));

      expect(await screen.findByText(/also serves other listeners/)).toBeInTheDocument();
      expect(fetchMock).not.toHaveBeenCalled();
    },
    SELECT_TEST_TIMEOUT
  );
});

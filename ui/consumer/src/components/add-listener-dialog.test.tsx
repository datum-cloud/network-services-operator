import { AddListenerDialog, checkListener } from './add-listener-dialog';
import { toAddListenerRequests, toNlbCreateErrors } from './nlb-create-form';
import type { ListenerRow } from './nlb-overview-model';
import * as api from '../lib/api';
import { IPV4_COPY } from '../lib/ipv4-quota';
import type { NetworkLoadBalancer, NetworkService } from '../schema';
import { fakePortalHost, IPV4_VERDICTS } from '../test/portal-host';
import type { ResourceQuotaVerdict } from '@datum-cloud/portal-plugin-sdk';
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
  addressNames: ['tcp-80'],
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

const wide: NetworkService = {
  ...game,
  uid: 'wide',
  name: 'wide',
  summary: { locations: 3, members: 3, healthy: 3 },
};

function ipv4Box() {
  return screen.getByRole('checkbox', { name: IPV4_COPY.optionLabel });
}

function ok() {
  return { ok: true, status: 200, json: async () => ({}) };
}

const originalFetch = globalThis.fetch;

beforeEach(() => {
  vi.mocked(api.useNetworkServices).mockReturnValue({
    data: [game, wide],
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
  it('appends an address and a listener, and builds the route', () => {
    const requests = toAddListenerRequests(nlb, {
      protocol: 'UDP',
      port: 7777,
      backend: 'game-server',
      backendPort: 'game',
      healthPort: 'health',
    });
    expect(requests.patch).toEqual([
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
  function renderDialog(verdict: ResourceQuotaVerdict = IPV4_VERDICTS.available) {
    const onOpenChange = vi.fn();
    const host = fakePortalHost(verdict);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <host.Wrapper>
        <QueryClientProvider client={client}>
          <AddListenerDialog projectId="demo-project" nlb={nlb} open onOpenChange={onOpenChange} />
        </QueryClientProvider>
      </host.Wrapper>
    );
    return { onOpenChange, host };
  }

  function addUdp7777() {
    choose(0, 'UDP');
    fireEvent.change(screen.getByPlaceholderText('e.g. 7777'), { target: { value: '7777' } });
    choose(1, 'game-server');
    choose(2, 'game · 7777/UDP');
    choose(3, 'health · 8080/TCP');
  }

  function patchBody(fetchMock: ReturnType<typeof vi.fn>) {
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    return JSON.parse(init.body as string) as { value?: { families?: string[] } }[];
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
      expect(JSON.parse(patch.body as string)).toHaveLength(2);
      expect(JSON.parse(patch.body as string)[0].value.families).toEqual(['IPv6']);
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

  it(
    'gives the new listener’s address IPv6 and IPv4 when IPv4 is checked',
    async () => {
      const fetchMock = vi.fn().mockResolvedValue(ok());
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderDialog();

      expect(ipv4Box()).toBeEnabled();
      expect(ipv4Box()).not.toBeChecked();
      addUdp7777();
      fireEvent.click(ipv4Box());
      fireEvent.click(screen.getByRole('button', { name: 'Add listener' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      expect(patchBody(fetchMock)[0].value?.families).toEqual(['IPv6', 'IPv4']);
    },
    SELECT_TEST_TIMEOUT
  );

  it.each([
    ['missing bucket', IPV4_VERDICTS.missing, IPV4_COPY.noAllocation],
    ['zero allowed', IPV4_VERDICTS.noAllocation, IPV4_COPY.noAllocation],
    ['used up', IPV4_VERDICTS.usedUp, IPV4_COPY.usedUp],
  ] satisfies [string, ResourceQuotaVerdict, string][])(
    'disables IPv4 and offers the request for this NLB: %s',
    (_, verdict, reason) => {
      const { host } = renderDialog(verdict);
      expect(ipv4Box()).toBeDisabled();
      expect(screen.getByText(reason)).toBeInTheDocument();
      expect(screen.getByText(IPV4_COPY.paidAddOn)).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: IPV4_COPY.requestAction }));
      expect(host.requestQuotaIncrease).toHaveBeenCalledWith(
        expect.objectContaining({ context: 'Public IPv4 address for network load balancer edge-nlb.' })
      );
    }
  );

  it('disables IPv4 without explanation while the allocation loads', () => {
    renderDialog(IPV4_VERDICTS.loading);
    expect(ipv4Box()).toBeDisabled();
    expect(screen.queryByText(IPV4_COPY.requestAction)).not.toBeInTheDocument();
  });

  it(
    'warns but allows IPv4 when the backend runs in more locations than addresses left',
    async () => {
      const fetchMock = vi.fn().mockResolvedValue(ok());
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderDialog();

      fireEvent.change(screen.getByPlaceholderText('e.g. 7777'), { target: { value: '8080' } });
      choose(1, 'wide');
      choose(2, 'health · 8080/TCP');
      fireEvent.click(ipv4Box());

      expect(screen.getByText(/serves 3 locations and the project has 1 public IPv4 address left/)).toBeInTheDocument();
      expect(screen.getByRole('button', { name: IPV4_COPY.requestAction })).toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: 'Add listener' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      expect(patchBody(fetchMock)[0].value?.families).toEqual(['IPv6', 'IPv4']);
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
  const withUdp: NetworkLoadBalancer = {
    ...nlb,
    listeners: [...nlb.listeners, udpListener],
    addressNames: ['tcp-80', 'udp-7777'],
  };

  const withUdpIpv4 = {
    ...withUdp,
    addressFamilies: { 'tcp-80': ['IPv6'], 'udp-7777': ['IPv6', 'IPv4'] },
    endpoints: ['us-east-1', 'eu-west-1'].map((location, i) => ({
      requestName: 'udp-7777',
      location,
      addresses: [
        { family: 'IPv6', value: `2001:db8::${i + 1}` },
        { family: 'IPv4', value: `203.0.113.${i + 1}` },
      ],
    })),
  } satisfies NetworkLoadBalancer;

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

  function renderEdit(
    editing: ListenerRow,
    target: NetworkLoadBalancer = withUdp,
    verdict: ResourceQuotaVerdict = IPV4_VERDICTS.available
  ) {
    vi.mocked(api.useNetworkServices).mockReturnValue({
      data: [game, dns],
      isLoading: false,
      error: null,
    } as never);
    const host = fakePortalHost(verdict);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <host.Wrapper>
        <QueryClientProvider client={client}>
          <AddListenerDialog
            projectId="demo-project"
            nlb={target}
            open
            onOpenChange={vi.fn()}
            editing={editing}
          />
        </QueryClientProvider>
      </host.Wrapper>
    );
    return { host };
  }

  function nlbPatch(fetchMock: ReturnType<typeof vi.fn>) {
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toMatch(/\/networkloadbalancers\/edge-nlb$/);
    return JSON.parse(init.body as string) as object[];
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

  it(
    'adds IPv4 to the listener’s existing address',
    async () => {
      const fetchMock = vi.fn().mockResolvedValue(ok());
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderEdit(row());

      expect(ipv4Box()).not.toBeChecked();
      fireEvent.click(ipv4Box());
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      expect(nlbPatch(fetchMock)).toEqual([
        { op: 'test', path: '/spec/exposure/addresses/1/name', value: 'udp-7777' },
        { op: 'test', path: '/spec/exposure/addresses/1/families', value: ['IPv6'] },
        { op: 'replace', path: '/spec/exposure/addresses/1/families', value: ['IPv6', 'IPv4'] },
      ]);
    },
    SELECT_TEST_TIMEOUT
  );

  it('won’t add IPv4 to an existing listener when the project has none left', () => {
    const { host } = renderEdit(row(), withUdp, IPV4_VERDICTS.usedUp);
    expect(ipv4Box()).toBeDisabled();
    expect(screen.getByText(IPV4_COPY.usedUp)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: IPV4_COPY.requestAction }));
    expect(host.requestQuotaIncrease).toHaveBeenCalledWith(
      expect.objectContaining({ context: 'Public IPv4 address for network load balancer edge-nlb.' })
    );
  });

  it('keeps an existing IPv4 address checked and changeable when the project has none left', () => {
    renderEdit(row(), withUdpIpv4, IPV4_VERDICTS.usedUp);
    expect(ipv4Box()).toBeChecked();
    expect(ipv4Box()).toBeEnabled();
    expect(screen.queryByText(IPV4_COPY.usedUp)).not.toBeInTheDocument();
  });

  it(
    'asks before releasing IPv4, and keeps it when the user changes their mind',
    async () => {
      const fetchMock = vi.fn().mockResolvedValue(ok());
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderEdit(row(), withUdpIpv4);

      fireEvent.click(ipv4Box());
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));

      expect(
        await screen.findByText(
          'Saving releases its public IPv4 addresses in us-east-1, eu-west-1. The same address can’t be kept, and isn’t guaranteed back if you add IPv4 again later. Its IPv6 address isn’t affected.'
        )
      ).toBeInTheDocument();
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(fetchMock).not.toHaveBeenCalled();

      fireEvent.click(screen.getByRole('button', { name: 'Keep IPv4' }));
      await waitFor(() => expect(ipv4Box()).toBeChecked());
      expect(screen.queryByRole('button', { name: 'Release IPv4 and save' })).not.toBeInTheDocument();
      expect(fetchMock).not.toHaveBeenCalled();

      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
      expect(fetchMock.mock.calls[0][0]).toMatch(/\/networkloadbalancerroutes\/edge-nlb-udp-7777$/);
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'releases IPv4 once confirmed',
    async () => {
      const fetchMock = vi.fn().mockResolvedValue(ok());
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderEdit(row(), withUdpIpv4);

      fireEvent.click(ipv4Box());
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));
      fireEvent.click(await screen.findByRole('button', { name: 'Release IPv4 and save' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      expect(nlbPatch(fetchMock)).toEqual([
        { op: 'test', path: '/spec/exposure/addresses/1/name', value: 'udp-7777' },
        { op: 'test', path: '/spec/exposure/addresses/1/families', value: ['IPv6', 'IPv4'] },
        { op: 'replace', path: '/spec/exposure/addresses/1/families', value: ['IPv6'] },
      ]);
    },
    SELECT_TEST_TIMEOUT
  );
});

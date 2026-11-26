import NlbDetail from './nlb-detail';
import { ApiError } from '../lib/api';
import { IPV4_FAILED_COPY, IPV4_RETRY_COPY } from '../components/ipv4-quota-callout';
import { buildIpv4QuotaIncreaseRequest, IPV4_COPY } from '../lib/ipv4-quota';
import { fakePortalHost } from '../test/portal-host';
import * as api from '../lib/api';
import type { NetworkLoadBalancer, NetworkLoadBalancerRoute, NetworkService } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return {
    ...actual,
    useNetworkLoadBalancer: vi.fn(),
    useNetworkLoadBalancerRoutes: vi.fn(),
    useNetworkServices: vi.fn(),
  };
});

const useNlbMock = vi.mocked(api.useNetworkLoadBalancer);
const useRoutesMock = vi.mocked(api.useNetworkLoadBalancerRoutes);
const useServicesMock = vi.mocked(api.useNetworkServices);

const nlb: NetworkLoadBalancer = {
  uid: 'nlb',
  name: 'edge-nlb',
  createdAt: new Date('2026-01-01T00:00:00Z'),
  networkName: 'kevnett',
  listeners: [
    { name: 'udp-7777', protocol: 'UDP', port: 7777, addressRef: 'udp-7777' },
    { name: 'tcp-80', protocol: 'TCP', port: 80, addressRef: 'tcp-80' },
  ],
  endpoints: [
    {
      requestName: 'udp-7777',
      location: 'iad',
      hostname: 'us-east-1.plea-fried-wgyt2.prism.example',
      addresses: [{ family: 'IPv6', value: '2001:db8::3' }],
    },
    {
      requestName: 'udp-7777',
      location: 'lhr',
      addresses: [{ family: 'IPv6', value: '2001:db8::4' }],
    },
  ],
  readyStatus: 'True',
};

const route: NetworkLoadBalancerRoute = {
  uid: 'r',
  name: 'edge-nlb-udp-7777',
  parents: [{ name: 'edge-nlb', listener: 'udp-7777' }],
  backendNames: ['game-server'],
  backends: [
    { name: 'game-server', port: 'game', healthCheck: { protocol: 'TCP', port: 'health' } },
  ],
  parentStatuses: [{ name: 'edge-nlb', listener: 'udp-7777', effective: true, status: 'True' }],
};

const service: NetworkService = {
  uid: 'svc',
  name: 'game-server',
  createdAt: new Date('2026-01-01T00:00:00Z'),
  networkInterfaceSelector: { matchLabels: {}, matchExpressions: [] },
  ports: [],
  summary: { locations: 2, members: 6, healthy: 5 },
  locations: [],
  membersResolvedStatus: 'True',
  readyStatus: 'True',
};

function mockQuery(
  mock: typeof useNlbMock | typeof useRoutesMock | typeof useServicesMock,
  value: object
) {
  mock.mockReturnValue({ isLoading: false, error: null, refetch: vi.fn(), ...value } as never);
}

const NLB_PATH = '/project/demo-project/services/networking/nlb/edge-nlb';

function renderPage(path = NLB_PATH, host = fakePortalHost()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <host.Wrapper>
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route
              path="/project/:projectId/services/:serviceSlug/nlb/:nlbName/*"
              element={<NlbDetail />}
            />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    </host.Wrapper>
  );
}

function listenerRow(name: string): HTMLElement {
  return screen
    .getByTestId('networking-plugin-nlb-listeners')
    .querySelector(`[data-listener="${name}"]`)
    ?.closest('tr') as HTMLElement;
}

beforeEach(() => {
  // Charts read an empty matrix unless a test says otherwise.
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify({ status: 'success', data: { resultType: 'matrix', result: [] } }), {
        status: 200,
      })
    )
  );
  mockQuery(useNlbMock, { data: nlb });
  mockQuery(useRoutesMock, { data: [route] });
  mockQuery(useServicesMock, { data: [service] });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('NlbDetail', () => {
  it('shows each listener with its address, backend, health and health check', () => {
    renderPage();
    // Two listeners on two addresses: the addresses show per listener.
    const udp = within(listenerRow('udp-7777'));
    expect(udp.getByText('UDP')).toBeInTheDocument();
    expect(udp.getByText('7777')).toBeInTheDocument();
    expect(udp.getByText('us-east-1.plea-fried-wgyt2.prism.example')).toBeInTheDocument();
    // Bare, so it pastes into a client; brackets are only for URLs.
    expect(udp.getByText('2001:db8::3')).toBeInTheDocument();
    expect(udp.getByRole('button', { name: 'Copy 2001:db8::3' })).toBeInTheDocument();
    expect(udp.getByText('+1 city')).toBeInTheDocument();
    expect(udp.getByRole('link', { name: 'game-server' })).toHaveAttribute(
      'href',
      '/project/demo-project/services/networking/networks/kevnett/services'
    );
    expect(udp.getByText('TCP :health')).toBeInTheDocument();
    expect(udp.getByText('Degraded')).toBeInTheDocument();

    const tcp = within(listenerRow('tcp-80'));
    expect(tcp.getByLabelText('Allocating an address')).toBeInTheDocument();
    expect(tcp.getByText('No route')).toBeInTheDocument();
  });

  it('shows each backend once with its health and the listeners that use it', () => {
    renderPage();
    const strip = screen.getByTestId('networking-plugin-nlb-backends');
    const card = within(strip.querySelector('[data-backend="game-server"]') as HTMLElement);
    expect(card.getByText('5/6 healthy')).toBeInTheDocument();
    expect(card.getByText('UDP 7777')).toBeInTheDocument();
    expect(card.getByText('NetworkService · port game')).toBeInTheDocument();
    expect(within(strip).getByRole('link', { name: 'Manage backends →' })).toHaveAttribute(
      'href',
      '/project/demo-project/services/networking/networks/kevnett/services'
    );
  });

  it('sums up the public addresses beside the listeners', () => {
    mockQuery(useNlbMock, { data: { ...nlb, listeners: [nlb.listeners[0]] } });
    mockQuery(useServicesMock, {
      data: [{ ...service, summary: { locations: 2, members: 6, healthy: 6 } }],
    });
    renderPage();
    const info = within(screen.getByTestId('networking-plugin-nlb-info'));
    expect(info.getByRole('link', { name: 'kevnett' })).toHaveAttribute(
      'href',
      '/project/demo-project/services/networking/networks/kevnett'
    );
    expect(info.getByText('Follows backends · 2 cities')).toBeInTheDocument();
    expect(info.getByText('IPv6')).toBeInTheDocument();
    expect(info.queryByText('IPv4 coming soon')).not.toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-nlb-ipv4-quota')).not.toBeInTheDocument();
    expect(screen.getByTestId('networking-plugin-nlb-summary')).toHaveTextContent(
      'Forwarding on 1 listener'
    );
  });

  it('says why a listener isn’t serving, and names the serving port probe', () => {
    mockQuery(useRoutesMock, {
      data: [
        {
          ...route,
          backends: [{ name: 'game-server', port: 'game', healthCheck: { protocol: 'TCP' } }],
          parentStatuses: [
            {
              name: 'edge-nlb',
              listener: 'udp-7777',
              effective: true,
              status: 'False',
              reason: 'NoEligibleEndpoints',
            },
          ],
        },
      ],
    });
    renderPage();
    const udp = within(listenerRow('udp-7777'));
    expect(udp.getByText('No reachable backends')).toBeInTheDocument();
    expect(udp.getByText('TCP · serving port')).toBeInTheDocument();
  });

  it('links back to the list and sums up the listeners', () => {
    renderPage();
    expect(screen.getByRole('link', { name: 'NLB' })).toHaveAttribute(
      'href',
      '/project/demo-project/services/networking/nlb'
    );
    expect(screen.getByTestId('networking-plugin-nlb-summary')).toHaveTextContent(
      'Needs attention'
    );
  });

  it('opens the add listener dialog from the listeners header', async () => {
    renderPage();
    fireEvent.click(screen.getByRole('button', { name: 'Add listener' }));
    expect(await screen.findByRole('dialog')).toHaveTextContent('Add listener');
  });

  it('edits a listener from its row menu', async () => {
    renderPage();
    const row = listenerRow('udp-7777');
    fireEvent.click(within(row).getByRole('button', { name: 'Actions' }));
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Edit UDP 7777');
    expect(within(dialog).getByPlaceholderText('e.g. 7777')).toHaveValue(7777);
  });

  it('deletes a listener and its route, then its listener and address from the NLB', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    const originalFetch = globalThis.fetch;
    globalThis.fetch = fetchMock as unknown as typeof fetch;
    mockQuery(useNlbMock, {
      data: { ...nlb, resourceVersion: '9', addressNames: ['udp-7777', 'tcp-80'] },
    });
    renderPage();

    const row = listenerRow('udp-7777');
    fireEvent.click(within(row).getByRole('button', { name: 'Actions' }));
    // The title's Delete comes first; the row menu's opens last.
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete' }).at(-1)!);
    const dialog = within(await screen.findByRole('dialog'));
    expect(
      dialog.getByText(
        /releases its public address\. It also deletes its route edge-nlb-udp-7777\./
      )
    ).toBeInTheDocument();
    fireEvent.change(dialog.getByPlaceholderText('Type in here...'), {
      target: { value: 'DELETE' },
    });
    fireEvent.click(dialog.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
    const [[routeUrl, routeInit], [nlbUrl, nlbInit]] = fetchMock.mock.calls as [
      string,
      RequestInit,
    ][];
    expect([routeInit.method, routeUrl.split('/default/')[1]]).toEqual([
      'DELETE',
      'networkloadbalancerroutes/edge-nlb-udp-7777',
    ]);
    expect([nlbInit.method, nlbUrl.split('/default/')[1]]).toEqual([
      'PATCH',
      'networkloadbalancers/edge-nlb',
    ]);
    expect(JSON.parse(nlbInit.body as string)).toContainEqual({
      op: 'remove',
      path: '/spec/listeners/0',
    });
    globalThis.fetch = originalFetch;
  });

  it('deletes the load balancer from the title, then goes back to the list', async () => {
    const mutateAsync = vi.fn().mockResolvedValue(undefined);
    const deleteSpy = vi
      .spyOn(api, 'useDeleteNetworkLoadBalancer')
      .mockReturnValue({ mutateAsync } as never);
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    const dialog = within(await screen.findByRole('dialog'));
    expect(dialog.getByText(/It also deletes its route edge-nlb-udp-7777\./)).toBeInTheDocument();
    fireEvent.change(dialog.getByPlaceholderText('Type in here...'), {
      target: { value: 'DELETE' },
    });
    fireEvent.click(dialog.getByRole('button', { name: 'Delete' }));

    await waitFor(() =>
      expect(mutateAsync).toHaveBeenCalledWith({ name: 'edge-nlb', routes: [route] })
    );
    deleteSpy.mockRestore();
  });

  it('won’t delete the only listener', () => {
    mockQuery(useNlbMock, { data: { ...nlb, listeners: [nlb.listeners[0]] } });
    renderPage();
    fireEvent.click(
      within(screen.getByTestId('networking-plugin-nlb-listeners')).getByRole('button', {
        name: 'Actions',
      })
    );
    expect(screen.getAllByRole('button', { name: 'Delete' }).at(-1)).toBeDisabled();
  });

  it('waits for routes before showing listeners', () => {
    mockQuery(useRoutesMock, { data: undefined, isLoading: true });
    renderPage();
    expect(screen.queryByTestId('networking-plugin-nlb-overview')).not.toBeInTheDocument();
  });

  it('shows an error when the NLB can’t be read', () => {
    mockQuery(useNlbMock, { data: undefined, error: new ApiError(404, 'not found') });
    renderPage();
    expect(screen.getByTestId('networking-plugin-error')).toHaveTextContent('not found');
  });

  it('shows Overview and Metrics tabs, with traffic on the overview', async () => {
    renderPage();
    const tabs = screen.getByTestId('networking-plugin-nlb-tabs');
    expect(within(tabs).getByRole('tab', { name: 'Metrics' })).toHaveAttribute('href', `${NLB_PATH}/metrics`);
    expect(await screen.findByTestId('networking-plugin-nlb-traffic-card')).toBeInTheDocument();
  });

  it('opens the metrics tab', async () => {
    renderPage(`${NLB_PATH}/metrics`);
    expect(await screen.findByTestId('networking-plugin-nlb-metrics')).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-nlb-listeners')).not.toBeInTheDocument();
  });

  it('sends an unknown tab back to the overview', async () => {
    renderPage(`${NLB_PATH}/nope`);
    expect(await screen.findByTestId('networking-plugin-nlb-listeners')).toBeInTheDocument();
  });
});

const QUOTA_403 =
  'ipclaims.ipam.miloapis.com "nlb-1-udp-7777-b" is forbidden: You\'ve reached your quota for this resource type (public-ipv4claims 1/1). Delete unused resources to free up capacity, or contact support to request a higher limit.';

function ipv4Nlb(
  lhrIpv4: NonNullable<NetworkLoadBalancer['endpoints'][number]['addressStatuses']>[number]
): NetworkLoadBalancer {
  return {
    ...nlb,
    listeners: [nlb.listeners[0]],
    addressFamilies: { 'udp-7777': ['IPv6', 'IPv4'] },
    endpoints: [
      {
        ...nlb.endpoints[0],
        addresses: [
          { family: 'IPv4', value: '203.0.113.7' },
          { family: 'IPv6', value: '2001:db8::3' },
        ],
        addressStatuses: [
          { family: 'IPv4', value: '203.0.113.7', conditions: [] },
          { family: 'IPv6', value: '2001:db8::3', conditions: [] },
        ],
      },
      {
        ...nlb.endpoints[1],
        addressStatuses: [{ family: 'IPv6', value: '2001:db8::4', conditions: [] }, lhrIpv4],
      },
    ],
  };
}

describe('NlbDetail IPv4', () => {
  it('shows the IPv4 address under the IPv6 one, picked by family not position', () => {
    mockQuery(useNlbMock, {
      data: ipv4Nlb({ family: 'IPv4', value: '203.0.113.8', conditions: [] }),
    });
    renderPage();
    const row = within(listenerRow('udp-7777'));
    const v6 = row.getByText('2001:db8::3');
    const v4 = row.getByText('203.0.113.7');
    expect(v6.compareDocumentPosition(v4) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(row.getByRole('button', { name: 'Copy 203.0.113.7' })).toBeInTheDocument();
    const info = within(screen.getByTestId('networking-plugin-nlb-info'));
    expect(info.getByText('IPv6 + IPv4')).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-nlb-ipv4-quota')).not.toBeInTheDocument();
  });

  it('shows a pending IPv4 address while it is allocated', () => {
    mockQuery(useNlbMock, {
      data: {
        ...nlb,
        listeners: [nlb.listeners[0]],
        addressFamilies: { 'udp-7777': ['IPv6', 'IPv4'] },
      },
    });
    renderPage();
    expect(
      within(listenerRow('udp-7777')).getByLabelText('Allocating an IPv4 address')
    ).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-nlb-ipv4-quota')).not.toBeInTheDocument();
  });

  it('explains a quota refusal, names the location and requests IPv4 for this NLB', () => {
    const host = fakePortalHost();
    mockQuery(useNlbMock, {
      data: ipv4Nlb({
        family: 'IPv4',
        conditions: [
          { type: 'Allocated', status: 'False', reason: 'AllocationFailed', message: QUOTA_403 },
        ],
      }),
    });
    renderPage(NLB_PATH, host);
    const callout = within(screen.getByTestId('networking-plugin-nlb-ipv4-quota'));
    expect(callout.getByText(IPV4_COPY.quotaRefused)).toBeInTheDocument();
    expect(callout.getByText('lhr')).toBeInTheDocument();
    expect(callout.getByText(IPV4_RETRY_COPY)).toBeInTheDocument();
    expect(callout.getByText(IPV4_COPY.paidAddOn)).toBeInTheDocument();
    expect(screen.queryByText(/is forbidden/)).not.toBeInTheDocument();
    fireEvent.click(callout.getByRole('button', { name: IPV4_COPY.requestAction }));
    expect(host.requestQuotaIncrease).toHaveBeenCalledWith(buildIpv4QuotaIncreaseRequest('edge-nlb'));
    expect(within(listenerRow('udp-7777')).getByText('203.0.113.7')).toBeInTheDocument();
    expect(within(listenerRow('udp-7777')).getByText('IPv4 refused')).toBeInTheDocument();
  });

  it('shows a non-quota failure with the condition text and no raw 403 lead', () => {
    mockQuery(useNlbMock, {
      data: ipv4Nlb({
        family: 'IPv4',
        conditions: [
          {
            type: 'Allocated',
            status: 'False',
            reason: 'AllocationFailed',
            message: 'ipclaims.ipam.miloapis.com "nlb-1" is forbidden: pool exhausted in lhr',
          },
        ],
      }),
    });
    renderPage();
    const callout = within(screen.getByTestId('networking-plugin-nlb-ipv4-failed'));
    expect(callout.getByText(IPV4_FAILED_COPY)).toBeInTheDocument();
    expect(callout.getByText(/pool exhausted in lhr/)).toBeInTheDocument();
    expect(screen.queryByText(/is forbidden/)).not.toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-nlb-ipv4-quota')).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: IPV4_COPY.requestAction })
    ).not.toBeInTheDocument();
  });
});

describe('NlbDetail edit listener with IPv4', () => {
  const redis: NetworkLoadBalancer = {
    uid: 'redis-uid',
    name: 'redisnlb',
    resourceVersion: '900',
    createdAt: new Date('2026-11-26T00:00:00Z'),
    networkName: 'kevnett',
    listeners: [{ name: 'tcp-6565', protocol: 'TCP', port: 6565, addressRef: 'tcp-6565', index: 0 }],
    addressNames: ['tcp-6565'],
    addressFamilies: { 'tcp-6565': ['IPv6', 'IPv4'] },
    endpoints: [
      {
        requestName: 'tcp-6565',
        location: 'us-central-1',
        addresses: [
          { family: 'IPv6', value: '2607:ed40:1fe:1000::4' },
          { family: 'IPv4', value: '67.14.170.2' },
        ],
      },
    ],
    readyStatus: 'True',
  };
  const redisRoute: NetworkLoadBalancerRoute = {
    uid: 'rr',
    name: 'redisnlb-tcp-6565',
    resourceVersion: '1',
    parents: [{ name: 'redisnlb', listener: 'tcp-6565' }],
    backendNames: ['redis'],
    backends: [{ name: 'redis', port: 'redis', healthCheck: { protocol: 'TCP' } }],
    parentStatuses: [{ name: 'redisnlb', listener: 'tcp-6565', effective: true, status: 'True' }],
  };
  const redisService: NetworkService = {
    ...service,
    uid: 'redis',
    name: 'redis',
    ports: [{ name: 'redis', port: 6379, protocol: 'TCP' }],
    summary: { locations: 1, members: 1, healthy: 1 },
  };

  function openEdit() {
    mockQuery(useNlbMock, { data: redis });
    mockQuery(useRoutesMock, { data: [redisRoute] });
    mockQuery(useServicesMock, { data: [redisService] });
    renderPage('/project/demo-project/services/networking/nlb/redisnlb');
    fireEvent.click(within(listenerRow('tcp-6565')).getByRole('button', { name: 'Actions' }));
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
  }

  it('opens with IPv4 checked when the listener’s address has IPv4', async () => {
    openEdit();
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Edit TCP 6565');
    expect(within(dialog).getByRole('checkbox', { name: IPV4_COPY.optionLabel })).toBeChecked();
  });

  it('asks before releasing IPv4, before any write', async () => {
    openEdit();
    const dialog = await screen.findByRole('dialog');
    const fetchMock = vi.mocked(globalThis.fetch);
    fetchMock.mockClear();
    fireEvent.click(within(dialog).getByRole('checkbox', { name: IPV4_COPY.optionLabel }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    expect(await screen.findByRole('button', { name: 'Release IPv4 and save' })).toBeInTheDocument();
    const writes = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method);
    expect(writes).toEqual([]);
  });
});

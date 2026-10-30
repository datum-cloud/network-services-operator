import NlbDetail from './nlb-detail';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { NetworkLoadBalancer, NetworkLoadBalancerRoute, NetworkService } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

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

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/project/demo-project/services/networking/nlb/edge-nlb']}>
        <Routes>
          <Route
            path="/project/:projectId/services/:serviceSlug/nlb/:nlbName/*"
            element={<NlbDetail />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  mockQuery(useNlbMock, { data: nlb });
  mockQuery(useRoutesMock, { data: [route] });
  mockQuery(useServicesMock, { data: [service] });
});

describe('NlbDetail', () => {
  it('shows each listener with its address, backend, health check and status', () => {
    renderPage();
    const listeners = within(screen.getByTestId('networking-plugin-nlb-listeners'));
    const udp = within(listeners.getByText('7777').closest('tr') as HTMLElement);
    expect(udp.getByText('UDP')).toBeInTheDocument();
    expect(udp.getByText('[2001:db8::3]')).toBeInTheDocument();
    expect(udp.getByText('+1 city')).toBeInTheDocument();
    expect(udp.getByText('game-server')).toBeInTheDocument();
    expect(udp.getByText(':health')).toBeInTheDocument();
    expect(udp.getByText('Degraded')).toBeInTheDocument();

    const tcp = within(listeners.getByText('80').closest('tr') as HTMLElement);
    expect(tcp.getByText('Pending')).toBeInTheDocument();
    expect(tcp.getByText('No route')).toBeInTheDocument();
  });

  it('says a backend with no members has none, and names the serving port probe', () => {
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
    mockQuery(useServicesMock, {
      data: [{ ...service, summary: { locations: 0, members: 0, healthy: 0 } }],
    });
    renderPage();
    expect(
      within(screen.getByTestId('networking-plugin-nlb-backends')).getByText('No members')
    ).toBeInTheDocument();
    expect(screen.getByText('· serving port', { exact: false })).toBeInTheDocument();
    expect(screen.getByText('No reachable backends')).toBeInTheDocument();
  });

  it('shows backend health and links backends to their network’s services', () => {
    renderPage();
    const backends = within(screen.getByTestId('networking-plugin-nlb-backends'));
    expect(backends.getByText('5/6 healthy')).toBeInTheDocument();
    expect(backends.getByRole('link')).toHaveAttribute(
      'href',
      '/project/demo-project/services/networking/networks/kevnett/services'
    );
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
    const row = within(screen.getByTestId('networking-plugin-nlb-listeners'))
      .getByText('7777')
      .closest('tr') as HTMLElement;
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

    const row = within(screen.getByTestId('networking-plugin-nlb-listeners'))
      .getByText('7777')
      .closest('tr') as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: 'Actions' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
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

  it('won’t delete the only listener', () => {
    mockQuery(useNlbMock, { data: { ...nlb, listeners: [nlb.listeners[0]] } });
    renderPage();
    fireEvent.click(
      within(screen.getByTestId('networking-plugin-nlb-listeners')).getByRole('button', {
        name: 'Actions',
      })
    );
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled();
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
});

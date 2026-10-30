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

function listenerRow(name: string): HTMLElement {
  return screen
    .getByTestId('networking-plugin-nlb-listeners')
    .querySelector(`[data-listener="${name}"]`)
    ?.closest('tr') as HTMLElement;
}

beforeEach(() => {
  mockQuery(useNlbMock, { data: nlb });
  mockQuery(useRoutesMock, { data: [route] });
  mockQuery(useServicesMock, { data: [service] });
});

describe('NlbDetail', () => {
  it('shows each listener with its address, backend, health and health check', () => {
    renderPage();
    // Two listeners on two addresses: the addresses show per listener.
    const udp = within(listenerRow('udp-7777'));
    expect(udp.getByText('UDP 7777')).toBeInTheDocument();
    expect(udp.getByText('us-east-1.plea-fried-wgyt2.prism.example')).toBeInTheDocument();
    // Bare, so it pastes into a client; brackets are only for URLs.
    expect(udp.getByText('2001:db8::3')).toBeInTheDocument();
    expect(udp.getByRole('button', { name: 'Copy 2001:db8::3' })).toBeInTheDocument();
    expect(udp.getByText('+1 city')).toBeInTheDocument();
    expect(udp.getByRole('link', { name: 'game-server' })).toHaveAttribute(
      'href',
      '/project/demo-project/services/networking/networks/kevnett/services'
    );
    expect(udp.getByText(':health')).toBeInTheDocument();
    expect(udp.getByText('5/6 endpoints healthy')).toBeInTheDocument();

    const tcp = within(listenerRow('tcp-80'));
    expect(tcp.getByLabelText('Allocating an address')).toBeInTheDocument();
    expect(tcp.getByText('No route')).toBeInTheDocument();
  });

  it('puts a single listener’s hostname and address in the info strip', () => {
    mockQuery(useNlbMock, { data: { ...nlb, listeners: [nlb.listeners[0]] } });
    mockQuery(useServicesMock, {
      data: [{ ...service, summary: { locations: 2, members: 6, healthy: 6 } }],
    });
    renderPage();
    const info = within(screen.getByTestId('networking-plugin-nlb-info'));
    expect(info.getByText('us-east-1.plea-fried-wgyt2.prism.example')).toBeInTheDocument();
    expect(info.getByText('2001:db8::3')).toBeInTheDocument();
    expect(info.getByRole('link', { name: 'kevnett' })).toHaveAttribute(
      'href',
      '/project/demo-project/services/networking/networks/kevnett'
    );
    expect(
      within(screen.getByTestId('networking-plugin-nlb-listeners')).queryByText('Address')
    ).not.toBeInTheDocument();
    expect(screen.getByTestId('networking-plugin-nlb-summary')).toHaveTextContent('Forwarding');
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
    expect(udp.getByText('· serving port', { exact: false })).toBeInTheDocument();
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
});

import NetworkDetail from './network-detail';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return {
    ...actual,
    useNetwork: vi.fn(),
    useSubnets: vi.fn(),
    useNetworkInterfaces: vi.fn(),
    useDeleteNetwork: vi.fn(),
  };
});

const useNetworkMock = vi.mocked(api.useNetwork);
const useSubnetsMock = vi.mocked(api.useSubnets);
const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);
const useDeleteNetworkMock = vi.mocked(api.useDeleteNetwork);

const emptyListResult = { data: [], isLoading: false, error: null, refetch: vi.fn() } as never;

const DETAIL_PATH = '/project/demo-project/services/networking/networks/prod-net';

function LocationDisplay() {
  return <div data-testid="location-display">{useLocation().pathname}</div>;
}

function renderPage(path = DETAIL_PATH) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <LocationDisplay />
        <Routes>
          <Route
            path="/project/:projectId/services/:serviceSlug/networks/:networkName/*"
            element={<NetworkDetail />}
          />
          <Route
            path="/project/:projectId/services/:serviceSlug/networks"
            element={<div data-testid="stub-networks-list">Networks list</div>}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

function makeNetwork(overrides: Partial<Network> = {}): Network {
  return {
    uid: 'prod-net',
    name: 'prod-net',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    readyStatus: 'True',
    ipFamilies: ['IPv6'],
    conditions: [],
    ...overrides,
  };
}

const deleteMutateAsyncMock = vi.fn();

beforeEach(() => {
  useNetworkMock.mockReset();
  useSubnetsMock.mockReset().mockReturnValue(emptyListResult);
  useNetworkInterfacesMock.mockReset().mockReturnValue(emptyListResult);
  deleteMutateAsyncMock.mockReset().mockResolvedValue(undefined);
  useDeleteNetworkMock.mockReset().mockReturnValue({
    mutateAsync: deleteMutateAsyncMock,
    isPending: false,
  } as never);
});

describe('NetworkDetail', () => {
  function loaded(overrides: Partial<Network> = {}) {
    useNetworkMock.mockReturnValue({
      data: makeNetwork(overrides),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
  }

  const tabs = () => within(screen.getByTestId('networking-plugin-network-tabs'));

  it('keeps the title and tabs mounted while the network loads', () => {
    useNetworkMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
    expect(screen.getAllByText('prod-net').length).toBeGreaterThanOrEqual(2);
    expect(tabs().getByText('Overview')).toBeInTheDocument();
  });

  it('shows the restricted state on a 403 ApiError', () => {
    useNetworkMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new ApiError(403, 'forbidden'),
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
  });

  it('shows the name, prefix, and every tab once loaded', () => {
    loaded({ ipv6Prefix: '2001:db8::/48' });

    renderPage();

    expect(screen.getAllByText('prod-net').length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText('2001:db8::/48 · IPv6')).toBeInTheDocument();
    for (const tab of ['Overview', 'Resources', 'Services', 'Configuration']) {
      expect(tabs().getByText(tab)).toBeInTheDocument();
    }
  });

  it('lands on the Overview tab with the network health', () => {
    loaded();

    renderPage();

    expect(screen.getByTestId('networking-plugin-network-overview')).toBeInTheDocument();
    expect(screen.getByText('Network is ready')).toBeInTheDocument();
  });

  it('shows plain-language reason text on the Overview when not ready', () => {
    loaded({ readyStatus: 'False', readyReason: 'IPv6Required', ipFamilies: ['IPv4'] });

    renderPage();

    expect(screen.getAllByText('IPv6 required').length).toBeGreaterThan(0);
    expect(screen.queryByText('IPv6Required')).not.toBeInTheDocument();
  });

  it('opens a tab directly from its URL', () => {
    loaded();

    renderPage(`${DETAIL_PATH}/resources`);

    expect(screen.getByTestId('networking-plugin-network-resources')).toBeInTheDocument();
  });

  it('navigates between tabs by URL', () => {
    loaded();

    renderPage();
    fireEvent.click(tabs().getByText('Configuration'));

    expect(screen.getByTestId('location-display')).toHaveTextContent(`${DETAIL_PATH}/configuration`);
    expect(screen.getByText('Resource name')).toBeInTheDocument();
  });

  it('sends an unknown tab back to the Overview', () => {
    loaded();

    renderPage(`${DETAIL_PATH}/nope`);

    expect(screen.getByTestId('location-display')).toHaveTextContent(new RegExp(`${DETAIL_PATH}$`));
    expect(screen.getByTestId('networking-plugin-network-overview')).toBeInTheDocument();
  });

  it('deletes the network from Configuration and navigates back to the networks list', async () => {
    loaded();

    renderPage(`${DETAIL_PATH}/configuration`);
    fireEvent.click(screen.getByRole('button', { name: 'Delete network' }));

    fireEvent.change(await screen.findByPlaceholderText('Type in here...'), {
      target: { value: 'DELETE' },
    });
    const dialogButtons = await screen.findAllByRole('button', { name: 'Delete' });
    fireEvent.click(dialogButtons[dialogButtons.length - 1]);

    await waitFor(() => expect(deleteMutateAsyncMock).toHaveBeenCalledWith('prod-net'));
    await waitFor(() => expect(screen.getByTestId('stub-networks-list')).toBeInTheDocument());
  });
});

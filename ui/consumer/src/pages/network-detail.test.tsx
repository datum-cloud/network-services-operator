import NetworkDetail from './network-detail';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
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

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter
        initialEntries={['/project/demo-project/services/networking/networks/prod-net']}>
        <Routes>
          <Route
            path="/project/:projectId/services/:serviceSlug/networks/:networkName"
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
  it('shows the loading skeleton while fetching', () => {
    useNetworkMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
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

  it('shows the network name, Ready badge, and tab bar once loaded', () => {
    useNetworkMock.mockReturnValue({
      data: makeNetwork({ readyStatus: 'True' }),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-network-detail')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'prod-net' })).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    for (const tab of ['Resources', 'Services', 'Settings']) {
      expect(screen.getByRole('tab', { name: tab })).toBeInTheDocument();
    }
  });

  it('shows plain-language reason text in the header badge when not ready', () => {
    useNetworkMock.mockReturnValue({
      data: makeNetwork({ readyStatus: 'False', readyReason: 'IPv6Required', ipFamilies: ['IPv4'] }),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByText('IPv6 required')).toBeInTheDocument();
  });

  it('shows Regions and Connected workloads together on the default Resources tab', () => {
    useNetworkMock.mockReturnValue({
      data: makeNetwork(),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-network-resources')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Regions' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Connected workloads' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Routes' })).not.toBeInTheDocument();
  });

  it('shows the Settings panel and header actions only once the Settings tab is active', () => {
    useNetworkMock.mockReturnValue({
      data: makeNetwork(),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.queryByRole('button', { name: 'Save changes' })).not.toBeInTheDocument();

    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Settings' }), { button: 0 });

    expect(screen.getByText('Resource name')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled();
  });

  it('deletes the network from the settings Danger Zone and navigates back to the networks list', async () => {
    useNetworkMock.mockReturnValue({
      data: makeNetwork(),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    fireEvent.mouseDown(screen.getByRole('tab', { name: 'Settings' }), { button: 0 });
    fireEvent.click(screen.getByRole('button', { name: 'Delete network' }));

    const dialogButtons = await screen.findAllByRole('button', { name: 'Delete' });
    fireEvent.click(dialogButtons[dialogButtons.length - 1]);

    await waitFor(() => expect(deleteMutateAsyncMock).toHaveBeenCalledWith('prod-net'));
    await waitFor(() => expect(screen.getByTestId('stub-networks-list')).toBeInTheDocument());
  });
});

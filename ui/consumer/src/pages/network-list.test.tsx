import NetworkList from './network-list';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import { ALL_NETWORK_PERMISSIONS, READ_ONLY_NETWORK_PERMISSIONS } from '../test/permissions';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetworkPermissions: vi.fn(), useNetworks: vi.fn() };
});

const useNetworkPermissionsMock = vi.mocked(api.useNetworkPermissions);
const useNetworksMock = vi.mocked(api.useNetworks);

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/project/demo-project/services/networking/networks']}>
        <Routes>
          <Route
            path="/project/:projectId/services/:serviceSlug/networks"
            element={<NetworkList />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

function makeNetwork(name: string): Network {
  return {
    uid: name,
    name,
    createdAt: new Date('2026-01-01T00:00:00Z'),
    readyStatus: 'True',
    ipFamilies: ['IPv6'],
    conditions: [],
  };
}

beforeEach(() => {
  useNetworkPermissionsMock.mockReset().mockReturnValue(ALL_NETWORK_PERMISSIONS);
  useNetworksMock.mockReset();
});

describe('NetworkList state machine', () => {
  it('shows the loading skeleton while fetching', () => {
    useNetworksMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
    // Breadcrumb + title stay mounted through loading.
    expect(screen.getAllByText('VPC Networks').length).toBeGreaterThan(0);
  });

  it('shows the restricted state on a 403 ApiError', () => {
    useNetworksMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new ApiError(403, 'forbidden'),
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
  });

  it('shows a retryable error state on a non-403 error', () => {
    useNetworksMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new ApiError(500, 'boom'),
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-error')).toBeInTheDocument();
  });

  it('shows the empty state with a datumctl pointer when there are zero networks', () => {
    useNetworksMock.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-network-empty')).toBeInTheDocument();
    expect(screen.getAllByText(/datumctl/i).length).toBeGreaterThan(0);
  });

  it('opens the create dialog from the empty state', () => {
    useNetworksMock.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.queryByRole('button', { name: 'Create network' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'New' }));
    expect(screen.getByText('Create a network')).toBeInTheDocument();
  });

  it('shows the table when networks are present', () => {
    useNetworksMock.mockReturnValue({
      data: [makeNetwork('default')],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByTestId('networking-plugin-network-table')).toBeInTheDocument();
    expect(screen.getByText('default')).toBeInTheDocument();
  });


  it('offers no create action to a read-only user', () => {
    useNetworkPermissionsMock.mockReturnValue(READ_ONLY_NETWORK_PERMISSIONS);
    useNetworksMock.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    renderPage();

    expect(screen.getByText(/no networks yet/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'New' })).not.toBeInTheDocument();
  });
});

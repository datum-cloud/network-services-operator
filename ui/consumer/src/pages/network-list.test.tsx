import NetworkList from './network-list';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetworks: vi.fn() };
});

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
    expect(screen.getAllByText('Networks').length).toBeGreaterThan(0);
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
});

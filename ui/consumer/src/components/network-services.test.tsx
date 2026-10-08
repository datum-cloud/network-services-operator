import { NetworkServices } from './network-services';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { NetworkService } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return {
    ...actual,
    useNetworkServices: vi.fn(),
    useNetworkInterfaces: vi.fn(),
    useDeleteNetworkService: vi.fn(),
  };
});

const useNetworkServicesMock = vi.mocked(api.useNetworkServices);
const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);
const useDeleteNetworkServiceMock = vi.mocked(api.useDeleteNetworkService);

function render(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return rtlRender(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

function makeService(overrides: Partial<NetworkService> = {}): NetworkService {
  return {
    uid: 'storefront-uid',
    name: 'storefront',
    createdAt: new Date('2026-08-25T14:18:34Z'),
    networkInterfaceSelector: { matchLabels: { 'compute.datumapis.com/workload-name': 'storefront' }, matchExpressions: [] },
    ports: [{ name: 'http', port: 8080, protocol: 'TCP' }],
    summary: { locations: 2, members: 4, healthy: 3 },
    locations: [],
    membersResolvedStatus: 'True',
    readyStatus: 'True',
    ...overrides,
  };
}

const deleteMutateAsyncMock = vi.fn();

beforeEach(() => {
  useNetworkServicesMock.mockReset();
  useNetworkInterfacesMock.mockReset();
  useNetworkInterfacesMock.mockReturnValue({
    data: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as never);
  deleteMutateAsyncMock.mockReset().mockResolvedValue(undefined);
  useDeleteNetworkServiceMock.mockReset().mockReturnValue({
    mutateAsync: deleteMutateAsyncMock,
    isPending: false,
  } as never);
});

describe('NetworkServices', () => {
  it('shows the loading skeleton while fetching', () => {
    useNetworkServicesMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
  });

  it('shows the restricted state on a 403 ApiError', () => {
    useNetworkServicesMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new ApiError(403, 'forbidden'),
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
  });

  it('shows an empty state pointing at how services appear, not a blank table', () => {
    useNetworkServicesMock.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByText(/you don't have any services yet/)).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-service-table')).not.toBeInTheDocument();
  });

  it('shows real fields per service: name, ports, locations, members, healthy, age', () => {
    useNetworkServicesMock.mockReturnValue({
      data: [makeService()],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    const row = screen.getByTestId('network-service-table-row');
    expect(within(row).getByText('storefront')).toBeInTheDocument();
    expect(within(row).getByText('http:8080')).toBeInTheDocument();
    expect(within(row).getByText('2')).toBeInTheDocument();
    expect(within(row).getByText('4')).toBeInTheDocument();
    expect(within(row).getByText('3')).toBeInTheDocument();
  });

  it('shows plain-language reason text for an unresolved service, not the raw reason', () => {
    useNetworkServicesMock.mockReturnValue({
      data: [makeService({ membersResolvedStatus: 'False', membersResolvedReason: 'NoMatchingInterfaces' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByText('No matching interfaces yet')).toBeInTheDocument();
    expect(screen.queryByText('NoMatchingInterfaces')).not.toBeInTheDocument();
  });

  it('notes that the list is not limited to a single network', () => {
    useNetworkServicesMock.mockReturnValue({
      data: [makeService()],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByText(/not limited to this network/)).toBeInTheDocument();
  });

  it('counts total, ready, not-ready, and total members in the stat tiles', () => {
    useNetworkServicesMock.mockReturnValue({
      data: [
        makeService({ uid: '1', name: 'a', readyStatus: 'True', summary: { locations: 1, members: 2, healthy: 2 } }),
        makeService({ uid: '2', name: 'b', readyStatus: 'False', summary: { locations: 1, members: 3, healthy: 0 } }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByText('Total services')).toBeInTheDocument();
    const stats = screen.getByTestId('networking-plugin-service-stats');
    expect(stats).toHaveTextContent('2');
    expect(stats).toHaveTextContent('1');
    expect(stats).toHaveTextContent('5');
  });

  it('deletes a service on confirm', async () => {
    useNetworkServicesMock.mockReturnValue({
      data: [makeService()],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    fireEvent.click(screen.getByRole('button', { name: 'Delete storefront' }));

    const dialogButtons = await screen.findAllByRole('button', { name: 'Delete' });
    fireEvent.click(dialogButtons[dialogButtons.length - 1]);

    await waitFor(() => expect(deleteMutateAsyncMock).toHaveBeenCalledWith('storefront'));
  });
});

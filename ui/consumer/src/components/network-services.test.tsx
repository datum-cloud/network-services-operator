import { NetworkServices } from './network-services';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import { ALL_NETWORK_PERMISSIONS, READ_ONLY_NETWORK_PERMISSIONS } from '../test/permissions';
import type { NetworkInterface, NetworkService } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return {
    ...actual,
    useNetworkPermissions: vi.fn(),
    useNetworkServices: vi.fn(),
    useNetworkInterfaces: vi.fn(),
    useDeleteNetworkService: vi.fn(),
    useUpdateNetworkService: vi.fn(),
  };
});

const useNetworkPermissionsMock = vi.mocked(api.useNetworkPermissions);
const useNetworkServicesMock = vi.mocked(api.useNetworkServices);
const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);
const useDeleteNetworkServiceMock = vi.mocked(api.useDeleteNetworkService);
const useUpdateNetworkServiceMock = vi.mocked(api.useUpdateNetworkService);

function render(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return rtlRender(ui, {
    wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
  });
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

function makeInterface(overrides: Partial<NetworkInterface> = {}): NetworkInterface {
  return {
    uid: 'iface-storefront-abc123',
    name: 'iface-storefront-abc123',
    createdAt: new Date('2026-08-25T14:18:34Z'),
    network: 'default',
    claimName: 'storefront-abc123',
    interfaceName: 'eth0',
    attachmentMode: 'Netns',
    workloadName: 'storefront',
    location: 'us-central-1',
    labels: { 'compute.datumapis.com/workload-name': 'storefront' },
    phase: 'Bound',
    addresses: [],
    holderAvailableStatus: 'True',
    holderAvailableReason: 'HolderAvailable',
    ...overrides,
  };
}

function servicesReturn(data: NetworkService[]) {
  useNetworkServicesMock.mockReturnValue({ data, isLoading: false, error: null, refetch: vi.fn() } as never);
}

const deleteMutateAsyncMock = vi.fn();

beforeEach(() => {
  useNetworkPermissionsMock.mockReset().mockReturnValue(ALL_NETWORK_PERMISSIONS);
  useNetworkServicesMock.mockReset();
  useNetworkInterfacesMock.mockReset();
  useNetworkInterfacesMock.mockReturnValue({
    data: [makeInterface()],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as never);
  deleteMutateAsyncMock.mockReset().mockResolvedValue(undefined);
  useUpdateNetworkServiceMock.mockReset().mockReturnValue({ mutateAsync: vi.fn(), isPending: false } as never);
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

    expect(screen.getByText(/No services yet/)).toBeInTheDocument();
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

    const row = screen.getByText('storefront').closest('tr') as HTMLElement;
    expect(within(row).getByText('storefront')).toBeInTheDocument();
    expect(within(row).getByText('http:8080/TCP')).toBeInTheDocument();
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

  it('lists only services whose selector matches an interface on this network', () => {
    servicesReturn([
      makeService(),
      makeService({
        uid: 'billing-uid',
        name: 'billing',
        networkInterfaceSelector: { matchLabels: { 'compute.datumapis.com/workload-name': 'billing' }, matchExpressions: [] },
      }),
    ]);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    const table = screen.getByTestId('networking-plugin-service-table');
    expect(within(table).getByText('storefront')).toBeInTheDocument();
    expect(within(table).queryByText('billing')).not.toBeInTheDocument();
    const elsewhere = screen.getByTestId('networking-plugin-elsewhere-services');
    expect(elsewhere).toHaveTextContent("1 service in this project isn't on this network");
    expect(within(elsewhere).getByText('billing')).toBeInTheDocument();
  });

  it('shows an on-network empty state when every service is elsewhere', () => {
    useNetworkInterfacesMock.mockReturnValue({ data: [], isLoading: false, error: null, refetch: vi.fn() } as never);
    servicesReturn([makeService()]);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByText('No services on this network')).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-service-table')).not.toBeInTheDocument();
    expect(within(screen.getByTestId('networking-plugin-elsewhere-services')).getByText('storefront')).toBeInTheDocument();
  });

  it('lists every service when the network interfaces fail to load', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new ApiError(403, 'forbidden'),
      refetch: vi.fn(),
    } as never);
    servicesReturn([makeService({ networkInterfaceSelector: { matchLabels: { app: 'other' }, matchExpressions: [] } })]);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByTestId('networking-plugin-services-unfiltered')).toBeInTheDocument();
    expect(within(screen.getByTestId('networking-plugin-service-table')).getByText('storefront')).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-elsewhere-services')).not.toBeInTheDocument();
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

    fireEvent.click(screen.getByRole('button', { name: 'Actions for storefront' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    fireEvent.change(await screen.findByPlaceholderText('Type in here...'), {
      target: { value: 'DELETE' },
    });
    const dialogButtons = await screen.findAllByRole('button', { name: 'Delete' });
    fireEvent.click(dialogButtons[dialogButtons.length - 1]);

    await waitFor(() => expect(deleteMutateAsyncMock).toHaveBeenCalledWith('storefront'));
  });

  it('opens the edit dialog from the row actions', async () => {
    useNetworkServicesMock.mockReturnValue({
      data: [makeService()],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    fireEvent.click(screen.getByRole('button', { name: 'Actions for storefront' }));
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));

    expect(await screen.findByText('Edit service storefront')).toBeInTheDocument();
  });

  it('keeps the edit dialog open when a refetch moves the service off this network', async () => {
    servicesReturn([makeService()]);
    const { rerender } = render(<NetworkServices projectId="demo-project" networkName="default" />);

    fireEvent.click(screen.getByRole('button', { name: 'Actions for storefront' }));
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    await screen.findByText('Edit service storefront');

    useNetworkInterfacesMock.mockReturnValue({ data: [], isLoading: false, error: null, refetch: vi.fn() } as never);
    rerender(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByText('No services on this network')).toBeInTheDocument();
    expect(screen.getByText('Edit service storefront')).toBeInTheDocument();
  });

  it('disables edit for a service the form cannot represent', () => {
    useNetworkServicesMock.mockReturnValue({
      data: [makeService({ networkInterfaceSelector: { matchLabels: { app: 'web' }, matchExpressions: [] } })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    fireEvent.click(screen.getByRole('button', { name: 'Actions for storefront' }));
    expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled();
  });

  it('shows only edit for a user who can patch but not delete', () => {
    useNetworkPermissionsMock.mockReturnValue({ ...ALL_NETWORK_PERMISSIONS, canDeleteService: false });
    useNetworkServicesMock.mockReturnValue({
      data: [makeService()],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    fireEvent.click(screen.getByRole('button', { name: 'Actions for storefront' }));
    expect(screen.getByRole('button', { name: 'Edit' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('hides create and delete for a read-only user', () => {
    useNetworkPermissionsMock.mockReturnValue(READ_ONLY_NETWORK_PERMISSIONS);
    useNetworkServicesMock.mockReturnValue({
      data: [makeService()],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkServices projectId="demo-project" networkName="default" />);

    expect(screen.getByText('storefront')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Actions for storefront' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /create service/i })).not.toBeInTheDocument();
  });
});

import { NetworkInterfaces } from './network-interfaces';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { NetworkInterface } from '../schema';
import { render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetworkInterfaces: vi.fn() };
});

const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);

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
    labels: {},
    phase: 'Bound',
    addresses: [{ family: 'IPv6', address: 'fd20:0:2::a/128', primary: true }],
    holderAvailableStatus: 'True',
    holderAvailableReason: 'HolderAvailable',
    ...overrides,
  };
}

beforeEach(() => {
  useNetworkInterfacesMock.mockReset();
});

describe('NetworkInterfaces', () => {
  it('shows the loading skeleton while fetching', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkInterfaces projectId="demo-project" networkName="default" />);

    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
  });

  it('shows the restricted state on a 403 ApiError', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new ApiError(403, 'forbidden'),
      refetch: vi.fn(),
    } as never);

    render(<NetworkInterfaces projectId="demo-project" networkName="default" />);

    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
  });

  it('shows an empty state pointing at deploying a workload, not a blank table', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkInterfaces projectId="demo-project" networkName="default" />);

    expect(screen.getByText(/you don't have any workloads on this network yet/)).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-interface-table')).not.toBeInTheDocument();
  });

  it('shows the workload name (not the raw interface name), location, address, and status', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface()],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkInterfaces projectId="demo-project" networkName="default" />);

    const row = screen.getByTestId('network-interface-table-row');
    expect(within(row).getByText('storefront')).toBeInTheDocument();
    expect(within(row).getByText('us-central-1')).toBeInTheDocument();
    expect(within(row).getByText('fd20:0:2::a/128')).toBeInTheDocument();
    expect(within(row).getByText('Serving')).toBeInTheDocument();
    expect(screen.queryByText('iface-storefront-abc123')).not.toBeInTheDocument();
  });

  it('falls back to the raw interface name when no workload-name label is present', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ workloadName: undefined })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkInterfaces projectId="demo-project" networkName="default" />);

    expect(screen.getByText('iface-storefront-abc123')).toBeInTheDocument();
  });

  it('shows plain-language reason text for a non-serving interface, not the raw reason', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ holderAvailableStatus: 'False', holderAvailableReason: 'HolderUnavailable' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkInterfaces projectId="demo-project" networkName="default" />);

    expect(screen.getByText('Not serving')).toBeInTheDocument();
    expect(screen.queryByText('HolderUnavailable')).not.toBeInTheDocument();
  });

  it('humanizes an opaque, holder-defined reason rather than showing it verbatim', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ holderAvailableStatus: 'False', holderAvailableReason: 'ImageUnavailable' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkInterfaces projectId="demo-project" networkName="default" />);

    expect(screen.getByText('Image unavailable')).toBeInTheDocument();
    expect(screen.queryByText('ImageUnavailable')).not.toBeInTheDocument();
  });

  it('counts distinct workloads, total instances, serving, and needs-attention in the stat tiles', () => {
    useNetworkInterfacesMock.mockReturnValue({
      data: [
        makeInterface({ uid: '1', workloadName: 'storefront', holderAvailableStatus: 'True' }),
        makeInterface({ uid: '2', workloadName: 'storefront', holderAvailableStatus: 'False' }),
        makeInterface({ uid: '3', workloadName: 'checkout', holderAvailableStatus: 'True' }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkInterfaces projectId="demo-project" networkName="default" />);

    const stats = screen.getByTestId('networking-plugin-interface-stats');
    expect(within(stats).getByText('Workloads')).toBeInTheDocument();
    expect(stats).toHaveTextContent('2'); // distinct workloads
    expect(stats).toHaveTextContent('3'); // total instances
    expect(stats).toHaveTextContent('1'); // needs attention
  });
});

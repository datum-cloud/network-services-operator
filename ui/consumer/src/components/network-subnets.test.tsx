import { NetworkSubnets } from './network-subnets';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { Subnet } from '../schema';
import { render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useSubnets: vi.fn() };
});

const useSubnetsMock = vi.mocked(api.useSubnets);

function makeSubnet(overrides: Partial<Subnet> = {}): Subnet {
  return {
    uid: 'default-us-central-1-ipv6',
    name: 'default-us-central-1-ipv6',
    createdAt: new Date('2026-08-25T14:18:34Z'),
    location: 'us-central-1',
    subnetClass: 'private',
    ipFamily: 'IPv6',
    startAddress: 'fd20:0:2::',
    prefixLength: 64,
    readyStatus: 'True',
    ...overrides,
  };
}

beforeEach(() => {
  useSubnetsMock.mockReset();
});

describe('NetworkSubnets', () => {
  it('shows the loading skeleton while fetching', () => {
    useSubnetsMock.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkSubnets projectId="demo-project" networkName="default" />);

    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
  });

  it('shows the restricted state on a 403 ApiError', () => {
    useSubnetsMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new ApiError(403, 'forbidden'),
      refetch: vi.fn(),
    } as never);

    render(<NetworkSubnets projectId="demo-project" networkName="default" />);

    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
  });

  it('shows an empty state pointing at how a network appears in a location, not a blank table', () => {
    useSubnetsMock.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkSubnets projectId="demo-project" networkName="default" />);

    expect(screen.getByText(/isn't present anywhere yet/)).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-subnet-table')).not.toBeInTheDocument();
  });

  it('shows real fields per location: location, address range, status, age — not the internal subnet name or class', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet()],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkSubnets projectId="demo-project" networkName="default" />);

    const row = screen.getByTestId('subnet-table-row');
    expect(within(row).getByText('us-central-1')).toBeInTheDocument();
    expect(within(row).getByText('fd20:0:2::/64')).toBeInTheDocument();
    expect(within(row).getByText('Ready')).toBeInTheDocument();
    expect(screen.queryByText('default-us-central-1-ipv6')).not.toBeInTheDocument();
    expect(screen.queryByText('private')).not.toBeInTheDocument();
  });

  it('shows plain-language reason text for a not-ready location, not the raw reason', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ readyStatus: 'False', readyReason: 'NotProgrammed' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkSubnets projectId="demo-project" networkName="default" />);

    expect(screen.getByText('Not yet programmed')).toBeInTheDocument();
    expect(screen.queryByText('NotProgrammed')).not.toBeInTheDocument();
  });

  it('counts total, ready, and needs-attention locations in the stat tiles', () => {
    useSubnetsMock.mockReturnValue({
      data: [
        makeSubnet({ uid: '1', location: 'us-central-1', readyStatus: 'True' }),
        makeSubnet({ uid: '2', location: 'us-east-1', readyStatus: 'False' }),
        makeSubnet({ uid: '3', location: 'eu-west-1', readyStatus: 'True' }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkSubnets projectId="demo-project" networkName="default" />);

    const stats = screen.getByTestId('networking-plugin-subnet-stats');
    expect(within(stats).getByText('Regions')).toBeInTheDocument();
    expect(stats).toHaveTextContent('3');
    expect(stats).toHaveTextContent('2');
    expect(stats).toHaveTextContent('1');
  });
});

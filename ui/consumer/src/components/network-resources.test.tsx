import { NetworkResources, groupByRegion } from './network-resources';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { NetworkInterface, NetworkService, Subnet } from '../schema';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return {
    ...actual,
    useSubnets: vi.fn(),
    useNetworkInterfaces: vi.fn(),
    useNetworkServices: vi.fn(),
  };
});

const useSubnetsMock = vi.mocked(api.useSubnets);
const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);
const useNetworkServicesMock = vi.mocked(api.useNetworkServices);

function result<T>(data: T) {
  return { data, isLoading: false, error: null, refetch: vi.fn() } as never;
}

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
    allocated: true,
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
    addresses: [{ family: 'IPv6', address: 'fd20:0:2::a/128', primary: true }],
    holderAvailableStatus: 'True',
    holderAvailableReason: 'HolderAvailable',
    ...overrides,
  };
}

function makeService(overrides: Partial<NetworkService> = {}): NetworkService {
  return {
    uid: 'storefront-uid',
    name: 'storefront-svc',
    createdAt: new Date('2026-08-25T14:18:34Z'),
    networkInterfaceSelector: {
      matchLabels: { 'compute.datumapis.com/workload-name': 'storefront' },
      matchExpressions: [],
    },
    ports: [],
    summary: { locations: 1, members: 1, healthy: 1 },
    locations: [],
    membersResolvedStatus: 'True',
    readyStatus: 'True',
    ...overrides,
  };
}

beforeEach(() => {
  useSubnetsMock.mockReset().mockReturnValue(result([]));
  useNetworkInterfacesMock.mockReset().mockReturnValue(result([]));
  useNetworkServicesMock.mockReset().mockReturnValue(result([]));
});

function regionHeader(region: string) {
  return screen.getByRole('button', { name: new RegExp(region) });
}

function render_() {
  render(
    <MemoryRouter>
      <NetworkResources projectId="demo-project" networkName="default" />
    </MemoryRouter>
  );
}

describe('NetworkResources', () => {
  it('shows the loading skeleton while either list is fetching', () => {
    useNetworkInterfacesMock.mockReturnValue({ data: undefined, isLoading: true, error: null } as never);
    render_();

    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
  });

  it('shows the restricted state on a 403 ApiError', () => {
    useSubnetsMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new ApiError(403, 'forbidden'),
      refetch: vi.fn(),
    } as never);
    render_();

    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
  });

  it('shows an empty state when the network is in no region and has no workloads', () => {
    render_();

    expect(screen.getByText(/isn't present anywhere yet/)).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-resources-table')).not.toBeInTheDocument();
  });

  it('shows one table with each region followed by its workloads, not stat tiles or a topology graph', () => {
    useSubnetsMock.mockReturnValue(result([makeSubnet()]));
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface()]));
    render_();

    const header = regionHeader('us-central-1');
    expect(within(header).getByText('fd20:0:2::/64')).toBeInTheDocument();
    expect(within(header).getByText('1 instance')).toBeInTheDocument();
    expect(within(header).getByText('Ready')).toBeInTheDocument();
    const row = screen.getByTestId('network-interface-table-row').closest('tr') as HTMLElement;
    expect(within(row).getByText('storefront')).toBeInTheDocument();
    expect(within(row).getByText('fd20:0:2::a/128')).toBeInTheDocument();
    expect(within(row).getByText('Serving')).toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-subnet-stats')).not.toBeInTheDocument();
    expect(screen.queryByTestId('networking-plugin-network-topology')).not.toBeInTheDocument();
  });

  it('shows a region with an allocated address range as ready, even before anything marks it programmed', () => {
    useSubnetsMock.mockReturnValue(
      result([makeSubnet({ readyStatus: 'False', readyReason: 'NotProgrammed', allocated: true })])
    );
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface()]));
    render_();

    expect(within(regionHeader('us-central-1')).getByText('Ready')).toBeInTheDocument();
    expect(screen.queryByText('Not yet programmed')).not.toBeInTheDocument();
  });

  it('shows plain-language reason text for a region that is neither ready nor allocated', () => {
    useSubnetsMock.mockReturnValue(
      result([makeSubnet({ readyStatus: 'False', readyReason: 'NotProgrammed', allocated: false })])
    );
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface()]));
    render_();

    expect(screen.getByText('Not yet programmed')).toBeInTheDocument();
    expect(screen.queryByText('NotProgrammed')).not.toBeInTheDocument();
  });

  it('shows plain-language reason text for a non-serving workload', () => {
    useSubnetsMock.mockReturnValue(result([makeSubnet()]));
    useNetworkInterfacesMock.mockReturnValue(
      result([makeInterface({ holderAvailableStatus: 'False', holderAvailableReason: 'HolderUnavailable' })])
    );
    render_();

    expect(screen.getByText('Not serving')).toBeInTheDocument();
  });

  it('falls back to the raw interface name when no workload name is known', () => {
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface({ workloadName: undefined })]));
    render_();

    expect(screen.getByText('iface-storefront-abc123')).toBeInTheDocument();
  });

  it('links each workload to its page in the compute plugin', () => {
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface()]));
    render_();

    expect(screen.getByRole('link', { name: 'storefront' })).toHaveAttribute(
      'href',
      '/project/demo-project/services/compute-datumapis-com/storefront'
    );
  });

  it('shows the raw interface name without a link when no workload name is known', () => {
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface({ workloadName: undefined })]));
    render_();

    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('labels each row with its instance number so instances of one workload are told apart', () => {
    useNetworkInterfacesMock.mockReturnValue(
      result([makeInterface({ uid: 'a', instanceIndex: '0' }), makeInterface({ uid: 'b', instanceIndex: '1' })])
    );
    render_();

    const rows = screen.getAllByTestId('network-interface-table-row');
    expect(rows[0]).toHaveTextContent('storefront · instance 0');
    expect(rows[1]).toHaveTextContent('storefront · instance 1');
  });

  it('leaves out the instance number when a workload has one instance in a region', () => {
    useNetworkInterfacesMock.mockReturnValue(
      result([
        makeInterface({ uid: 'a', instanceIndex: '0', location: 'us-central-1' }),
        makeInterface({ uid: 'b', instanceIndex: '0', location: 'us-east-1' }),
      ])
    );
    render_();

    expect(screen.queryByText(/instance 0/)).not.toBeInTheDocument();
  });

  it('shows regions expanded and collapses one on click, keeping its header and status', () => {
    useSubnetsMock.mockReturnValue(result([makeSubnet()]));
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface()]));
    render_();

    const header = regionHeader('us-central-1');
    expect(header).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByTestId('network-interface-table-row')).toBeInTheDocument();

    fireEvent.click(header);

    expect(regionHeader('us-central-1')).toHaveAttribute('aria-expanded', 'false');
    expect(within(regionHeader('us-central-1')).getByText('Ready')).toBeInTheDocument();
  });

  it('says there are no workloads yet when the network has regions but no instances', () => {
    useSubnetsMock.mockReturnValue(result([makeSubnet()]));
    render_();

    expect(screen.getByText(/no workloads on this network yet/)).toBeInTheDocument();
    expect(screen.getByTestId('networking-plugin-resources-summary')).toHaveTextContent('1 region');
  });

  it('hides the Services column when no workload on the network has a service', () => {
    useSubnetsMock.mockReturnValue(result([makeSubnet()]));
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface()]));
    render_();

    expect(screen.queryAllByRole('columnheader', { name: 'Services' })).toHaveLength(0);
  });

  it('lists the services that select each workload', () => {
    useSubnetsMock.mockReturnValue(result([makeSubnet()]));
    useNetworkInterfacesMock.mockReturnValue(result([makeInterface()]));
    useNetworkServicesMock.mockReturnValue(
      result([
        makeService(),
        makeService({
          uid: 'other',
          name: 'other-svc',
          networkInterfaceSelector: { matchLabels: { app: 'other' }, matchExpressions: [] },
        }),
      ])
    );
    render_();

    expect(screen.getAllByRole('columnheader', { name: 'Services' }).length).toBeGreaterThan(0);
    const row = screen.getByTestId('network-interface-table-row').closest('tr') as HTMLElement;
    expect(within(row).getByText('storefront-svc')).toBeInTheDocument();
    expect(within(row).queryByText(/other-svc/)).not.toBeInTheDocument();
  });

  it('summarizes regions, workloads, instances, serving, and what needs attention in one line', () => {
    useSubnetsMock.mockReturnValue(
      result([
        makeSubnet({ uid: '1', location: 'us-central-1' }),
        makeSubnet({ uid: '2', location: 'us-east-1', readyStatus: 'False', allocated: false }),
      ])
    );
    useNetworkInterfacesMock.mockReturnValue(
      result([
        makeInterface({ uid: 'a' }),
        makeInterface({ uid: 'b', location: 'us-east-1', holderAvailableStatus: 'False' }),
        makeInterface({ uid: 'c', workloadName: 'checkout' }),
      ])
    );
    render_();

    expect(screen.getByTestId('networking-plugin-resources-summary')).toHaveTextContent(
      '2 regions · 2 workloads · 3 instances · 2 serving · 2 need attention'
    );
  });
});

describe('groupByRegion', () => {
  it('sorts regions and attaches workloads to the region they run in', () => {
    const groups = groupByRegion(
      [makeSubnet({ location: 'us-east-1' }), makeSubnet({ location: 'eu-west-1' })],
      [makeInterface({ location: 'us-east-1' })]
    );

    expect(groups.map((g) => g.region)).toEqual(['eu-west-1', 'us-east-1']);
    expect(groups[1].interfaces).toHaveLength(1);
  });

  it('still shows a workload whose region has no subnet', () => {
    const groups = groupByRegion([], [makeInterface({ location: 'ap-south-1' })]);

    expect(groups).toHaveLength(1);
    expect(groups[0].region).toBe('ap-south-1');
    expect(groups[0].subnet).toBeUndefined();
    expect(groups[0].interfaces).toHaveLength(1);
  });
});

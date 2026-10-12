import { NetworkOverview } from './network-overview';
import * as api from '../lib/api';
import type { Network, NetworkInterface, Subnet } from '../schema';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useSubnets: vi.fn(), useNetworkInterfaces: vi.fn(), useNetworkServices: vi.fn() };
});

const useSubnetsMock = vi.mocked(api.useSubnets);
const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);
const useNetworkServicesMock = vi.mocked(api.useNetworkServices);

function result<T>(data: T, isLoading = false) {
  return { data, isLoading, error: null, refetch: vi.fn() } as never;
}

function makeNetwork(overrides: Partial<Network> = {}): Network {
  return {
    uid: 'prod-net',
    name: 'prod-net',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    readyStatus: 'True',
    ipv6Prefix: '2001:db8::/48',
    ipFamilies: ['IPv6'],
    ipamMode: 'Auto',
    mtu: 1440,
    conditions: [],
    ...overrides,
  };
}

function makeSubnet(location: string, overrides: Partial<Subnet> = {}): Subnet {
  return {
    uid: `prod-net-${location}`,
    name: `prod-net-${location}`,
    createdAt: new Date('2026-01-01T00:00:00Z'),
    location,
    subnetClass: 'private',
    ipFamily: 'IPv6',
    startAddress: 'fd20:0:2::',
    prefixLength: 64,
    readyStatus: 'True',
    allocated: true,
    ...overrides,
  };
}

function makeInterface(location: string, name: string): NetworkInterface {
  return {
    uid: name,
    name,
    createdAt: new Date('2026-01-01T00:00:00Z'),
    network: 'prod-net',
    claimName: name,
    interfaceName: 'eth0',
    attachmentMode: 'Netns',
    workloadName: 'storefront',
    location,
    labels: {},
    phase: 'Bound',
    addresses: [],
    holderAvailableStatus: 'True',
  };
}

function renderOverview(network = makeNetwork()) {
  return render(
    <MemoryRouter>
      <NetworkOverview network={network} projectId="demo-project" />
    </MemoryRouter>
  );
}

beforeEach(() => {
  useSubnetsMock.mockReset().mockReturnValue(result([]));
  useNetworkInterfacesMock.mockReset().mockReturnValue(result([]));
  useNetworkServicesMock.mockReset().mockReturnValue(result([]));
});

describe('NetworkOverview', () => {
  it('shows the network configuration', () => {
    renderOverview();

    const ip = within(screen.getByTestId('networking-plugin-ip-card'));
    expect(ip.getByText('2001:db8::/48')).toBeInTheDocument();
    expect(ip.getByText('Auto')).toBeInTheDocument();
    expect(ip.getByText('1440')).toBeInTheDocument();
  });

  it('explains why a network is not ready', () => {
    renderOverview(
      makeNetwork({ readyStatus: 'False', readyReason: 'IPv6Required', readyMessage: 'Add IPv6 to continue.' })
    );

    const health = within(screen.getByTestId('networking-plugin-network-health'));
    expect(health.getByText('IPv6 required')).toBeInTheDocument();
    expect(health.getByText('Add IPv6 to continue.')).toBeInTheDocument();
  });

  it('lists regions and their workloads directly below the health strip', () => {
    useSubnetsMock.mockReturnValue(result([makeSubnet('us-central-1'), makeSubnet('eu-west-1')]));
    useNetworkInterfacesMock.mockReturnValue(
      result([makeInterface('us-central-1', 'a'), makeInterface('us-central-1', 'b')])
    );

    renderOverview();

    const health = screen.getByTestId('networking-plugin-network-health');
    const resources = screen.getByTestId('networking-plugin-network-resources');
    expect(health.compareDocumentPosition(resources) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(resources).getByText('us-central-1')).toBeInTheDocument();
    expect(within(resources).getByText('eu-west-1')).toBeInTheDocument();
    expect(within(resources).getByText('2 instances')).toBeInTheDocument();
    expect(health).toHaveTextContent('2 regions');
  });

  it('flags regions that are not usable in the health strip', () => {
    useSubnetsMock.mockReturnValue(
      result([
        makeSubnet('us-central-1'),
        makeSubnet('eu-west-1', { readyStatus: 'False', readyReason: 'Pending', allocated: false }),
      ])
    );

    renderOverview();

    expect(screen.getByTestId('networking-plugin-network-health')).toHaveTextContent(
      '1 of 2 regions need attention'
    );
  });

  it('shows an empty resources state before anything is deployed', () => {
    renderOverview();

    expect(screen.getByText(/Not in any region yet/)).toBeInTheDocument();
  });
});

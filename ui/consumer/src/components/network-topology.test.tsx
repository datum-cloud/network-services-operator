import { NetworkTopology, gatewayLayout, layout, networkServiceNamesOnNetwork } from './network-topology';
import * as api from '../lib/api';
import type { HTTPProxy, NetworkInterface, NetworkService, Subnet } from '../schema';
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return {
    ...actual,
    useSubnets: vi.fn(),
    useNetworkInterfaces: vi.fn(),
    useHTTPProxies: vi.fn(),
    useNetworkServices: vi.fn(),
  };
});

// Stubbed rather than exercised against a real navigator.clipboard: this
// component's job is just to call copy() with the right value, and the
// hook's own clipboard/toast behavior belongs to datum-ui's own tests.
const { copyMock } = vi.hoisted(() => ({ copyMock: vi.fn() }));
vi.mock('@datum-cloud/datum-ui/hooks', () => ({
  useCopyToClipboard: () => [false, copyMock],
}));

const useSubnetsMock = vi.mocked(api.useSubnets);
const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);
const useHTTPProxiesMock = vi.mocked(api.useHTTPProxies);
const useNetworkServicesMock = vi.mocked(api.useNetworkServices);

function makeSubnet(overrides: Partial<Subnet> = {}): Subnet {
  return {
    uid: 'u',
    name: 'default-us-central-1-ipv6',
    createdAt: new Date(),
    location: 'us-central-1',
    readyStatus: 'True',
    ...overrides,
  };
}

function makeInterface(overrides: Partial<NetworkInterface> = {}): NetworkInterface {
  return {
    uid: 'i',
    name: 'kevload-default-us-central-1-0-eth0',
    createdAt: new Date(),
    location: 'us-central-1',
    workloadName: 'kevload',
    labels: { 'compute.datumapis.com/workload-name': 'kevload' },
    addresses: [],
    holderAvailableStatus: 'True',
    ...overrides,
  };
}

function makeHTTPProxy(overrides: Partial<HTTPProxy> = {}): HTTPProxy {
  return {
    uid: 'p',
    name: 'gw-public',
    createdAt: new Date(),
    hostnames: [],
    networkServiceNames: ['storefront'],
    programmedStatus: 'True',
    ...overrides,
  };
}

function makeNetworkService(overrides: Partial<NetworkService> = {}): NetworkService {
  return {
    uid: 's',
    name: 'storefront',
    createdAt: new Date(),
    networkInterfaceSelector: {
      matchLabels: { 'compute.datumapis.com/workload-name': 'kevload' },
      matchExpressions: [],
    },
    ports: [],
    summary: { locations: 0, members: 0, healthy: 0 },
    locations: [],
    membersResolvedStatus: 'True',
    readyStatus: 'True',
    ...overrides,
  };
}

beforeEach(() => {
  useSubnetsMock.mockReset();
  useNetworkInterfacesMock.mockReset().mockReturnValue({
    data: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as never);
  useHTTPProxiesMock.mockReset().mockReturnValue({
    data: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as never);
  useNetworkServicesMock.mockReset().mockReturnValue({
    data: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as never);
  copyMock.mockReset();
});

describe('NetworkTopology', () => {
  it('renders nothing when there are no locations', () => {
    useSubnetsMock.mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.queryByTestId('networking-plugin-network-topology')).not.toBeInTheDocument();
  });

  it('renders a node for the network hub and each location', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' }), makeSubnet({ uid: 'u2', location: 'us-east-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.getByTestId('networking-plugin-network-topology')).toBeInTheDocument();
    expect(screen.getByText('default')).toBeInTheDocument();
    expect(screen.getByText('us-central-1')).toBeInTheDocument();
    expect(screen.getByText('us-east-1')).toBeInTheDocument();
  });

  it('nests workloads under the location they are attached to', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ workloadName: 'kevload' }), makeInterface({ uid: 'i2', workloadName: 'hello-kevin' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.getByText('kevload')).toBeInTheDocument();
    expect(screen.getByText('hello-kevin')).toBeInTheDocument();
  });

  it('renders a gateway node for each HTTPProxy fronting the network, plus an Internet node', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ workloadName: 'kevload' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkServicesMock.mockReturnValue({
      data: [makeNetworkService({ name: 'storefront' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useHTTPProxiesMock.mockReturnValue({
      data: [makeHTTPProxy({ name: 'gw-public' }), makeHTTPProxy({ uid: 'p2', name: 'gw-internal' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.getByText('Internet')).toBeInTheDocument();
    expect(screen.getByText('gw-public')).toBeInTheDocument();
    expect(screen.getByText('gw-internal')).toBeInTheDocument();
  });

  it('opens a popover with gateway details when a gateway node is clicked', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ workloadName: 'kevload' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkServicesMock.mockReturnValue({
      data: [makeNetworkService({ name: 'storefront' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useHTTPProxiesMock.mockReturnValue({
      data: [
        makeHTTPProxy({
          name: 'gw-public',
          hostnames: ['app.example.com'],
          programmedStatus: 'True',
          programmedReason: 'Programmed',
        }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.queryByTestId('gateway-info-popover')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'gw-public details' }));

    const popover = screen.getByTestId('gateway-info-popover');
    expect(popover).toBeInTheDocument();
    expect(popover).toHaveTextContent('gw-public');
    expect(popover).toHaveTextContent('app.example.com');
    expect(popover).toHaveTextContent('storefront');
  });

  it('copies the (possibly truncated) hostname to the clipboard from the gateway popover', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ workloadName: 'kevload' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkServicesMock.mockReturnValue({
      data: [makeNetworkService({ name: 'storefront' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useHTTPProxiesMock.mockReturnValue({
      data: [makeHTTPProxy({ name: 'gw-public', hostnames: ['a-very-long-hostname.example.com'] })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);
    fireEvent.click(screen.getByRole('button', { name: 'gw-public details' }));
    fireEvent.click(screen.getByRole('button', { name: 'Copy hostname' }));

    expect(copyMock).toHaveBeenCalledWith('a-very-long-hostname.example.com', {
      withToast: true,
      toastMessage: 'Hostname copied to clipboard',
    });
  });

  it('opens a popover with workload details when a workload node is clicked', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkInterfacesMock.mockReturnValue({
      data: [
        makeInterface({
          workloadName: 'kevload',
          location: 'us-central-1',
          holderAvailableStatus: 'True',
          addresses: [{ family: 'IPv6', address: 'fd20:0:2::a/128', primary: true }],
        }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.queryByTestId('workload-info-popover')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'kevload details' }));

    const popover = screen.getByTestId('workload-info-popover');
    expect(popover).toBeInTheDocument();
    expect(popover).toHaveTextContent('kevload');
    expect(popover).toHaveTextContent('us-central-1');
    expect(popover).toHaveTextContent('fd20:0:2::a/128');
  });

  it('renders zoom and reset controls for the diagram', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.getByRole('button', { name: 'Zoom in' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Zoom out' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reset view' })).toBeInTheDocument();
  });

  it('zooms via the toolbar buttons', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    const stage = screen.getByTestId('network-topology-stage');
    const initialTransform = stage.style.transform;
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));

    expect(stage.style.transform).not.toBe(initialTransform);
  });

  it('zooms in response to a wheel event over the diagram viewport, not just the toolbar', () => {
    // Regression test: the wheel listener used to be attached via a plain
    // useRef whose effect ran once, before the viewport div existed (this
    // component renders null until subnets load) — so it never actually
    // attached. See the callback-ref comment above viewportEl.
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    const stage = screen.getByTestId('network-topology-stage');
    const initialTransform = stage.style.transform;
    fireEvent.wheel(screen.getByTestId('network-topology-viewport'), { deltaY: -100 });

    expect(stage.style.transform).not.toBe(initialTransform);
  });

  it('renders no Internet or gateway nodes when there are no HTTPProxies', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.queryByText('Internet')).not.toBeInTheDocument();
  });

  it('omits HTTPProxies with no NetworkService-backed rule, since they are not fronting this network', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ workloadName: 'kevload' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkServicesMock.mockReturnValue({
      data: [makeNetworkService({ name: 'storefront' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useHTTPProxiesMock.mockReturnValue({
      data: [
        makeHTTPProxy({ name: 'gw-public', networkServiceNames: ['storefront'] }),
        makeHTTPProxy({ uid: 'p2', name: 'gw-unrelated-connector', networkServiceNames: [] }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.getByText('gw-public')).toBeInTheDocument();
    expect(screen.queryByText('gw-unrelated-connector')).not.toBeInTheDocument();
  });

  it('renders no Internet or gateway nodes when every HTTPProxy lacks a NetworkService backend', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useHTTPProxiesMock.mockReturnValue({
      data: [makeHTTPProxy({ networkServiceNames: [] })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.queryByText('Internet')).not.toBeInTheDocument();
  });

  it('omits an HTTPProxy whose NetworkService resolves to a different network, not this one', () => {
    useSubnetsMock.mockReturnValue({
      data: [makeSubnet({ location: 'us-central-1' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    // This network's only interface belongs to "kevload", but the
    // NetworkService's selector targets a different workload — so it resolves
    // to members on some other network, not this one.
    useNetworkInterfacesMock.mockReturnValue({
      data: [makeInterface({ workloadName: 'kevload' })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useNetworkServicesMock.mockReturnValue({
      data: [
        makeNetworkService({
          name: 'other-storefront',
          networkInterfaceSelector: {
            matchLabels: { 'compute.datumapis.com/workload-name': 'unrelated-workload' },
            matchExpressions: [],
          },
        }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);
    useHTTPProxiesMock.mockReturnValue({
      data: [makeHTTPProxy({ name: 'gw-other-network', networkServiceNames: ['other-storefront'] })],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as never);

    render(<NetworkTopology projectId="demo-project" networkName="default" />);

    expect(screen.queryByText('Internet')).not.toBeInTheDocument();
    expect(screen.queryByText('gw-other-network')).not.toBeInTheDocument();
  });
});

describe('networkServiceNamesOnNetwork', () => {
  it('includes a NetworkService whose selector matches one of this network\'s interfaces', () => {
    const service = makeNetworkService({ name: 'storefront' });
    const iface = makeInterface({ workloadName: 'kevload' });

    expect(networkServiceNamesOnNetwork([service], [iface])).toEqual(new Set(['storefront']));
  });

  it('excludes a NetworkService whose selector matches none of this network\'s interfaces', () => {
    const service = makeNetworkService({
      name: 'other-storefront',
      networkInterfaceSelector: {
        matchLabels: { 'compute.datumapis.com/workload-name': 'unrelated-workload' },
        matchExpressions: [],
      },
    });
    const iface = makeInterface({ workloadName: 'kevload' });

    expect(networkServiceNamesOnNetwork([service], [iface])).toEqual(new Set());
  });
});

describe('layout', () => {
  it('spreads the workload angle wider as more workloads share one location, so nodes do not stack on top of each other', () => {
    const subnet = makeSubnet();
    const oneWorkload = layout([subnet], [makeInterface({ uid: '1' })]);
    const fourWorkloads = layout(
      [subnet],
      [
        makeInterface({ uid: '1' }),
        makeInterface({ uid: '2' }),
        makeInterface({ uid: '3' }),
        makeInterface({ uid: '4' }),
      ]
    );

    const positions = fourWorkloads[0].workloads.map((w) => `${w.position.x},${w.position.y}`);
    expect(new Set(positions).size).toBe(4); // no two workloads land on the same point

    const ySpreadOne = 0; // a single workload sits on the location's own angle, no spread
    const ys = fourWorkloads[0].workloads.map((w) => w.position.y);
    const ySpreadFour = Math.max(...ys) - Math.min(...ys);
    expect(ySpreadFour).toBeGreaterThan(ySpreadOne);
    expect(oneWorkload[0].workloads).toHaveLength(1);
  });

  it('keeps every node within a reasonable bound of the canvas regardless of location count', () => {
    const subnets = [
      makeSubnet({ uid: '1', location: 'a' }),
      makeSubnet({ uid: '2', location: 'b' }),
      makeSubnet({ uid: '3', location: 'c' }),
      makeSubnet({ uid: '4', location: 'd' }),
    ];
    const interfaces = subnets.flatMap((s, i) =>
      Array.from({ length: 3 }, (_, j) => makeInterface({ uid: `${i}-${j}`, location: s.location }))
    );

    const nodes = layout(subnets, interfaces);

    for (const node of nodes) {
      // Canvas is 380px tall; every node should stay comfortably inside it.
      expect(node.position.y).toBeGreaterThan(-20);
      expect(node.position.y).toBeLessThan(400);
      for (const { position } of node.workloads) {
        expect(position.y).toBeGreaterThan(-20);
        expect(position.y).toBeLessThan(400);
      }
    }
  });
});

describe('gatewayLayout', () => {
  function makeHTTPProxy(overrides: Partial<HTTPProxy> = {}): HTTPProxy {
    return {
      uid: 'p',
      name: 'gw-public',
      createdAt: new Date(),
      hostnames: [],
      networkServiceNames: ['storefront'],
      programmedStatus: 'True',
      ...overrides,
    };
  }

  it('returns one node per HTTPProxy, positioned to the left of the hub', () => {
    const gateways = gatewayLayout([
      makeHTTPProxy({ uid: '1', name: 'gw-public' }),
      makeHTTPProxy({ uid: '2', name: 'gw-internal' }),
    ]);

    expect(gateways).toHaveLength(2);
    // The hub sits at CENTER.x (380); gateways fan out to its left.
    for (const gateway of gateways) {
      expect(gateway.position.x).toBeLessThan(380);
    }
  });

  it('spreads gateways apart so they do not stack on top of each other', () => {
    const gateways = gatewayLayout([
      makeHTTPProxy({ uid: '1' }),
      makeHTTPProxy({ uid: '2' }),
      makeHTTPProxy({ uid: '3' }),
    ]);

    const positions = gateways.map((g) => `${g.position.x},${g.position.y}`);
    expect(new Set(positions).size).toBe(3);
  });

  it('returns nothing when there are no HTTPProxies', () => {
    expect(gatewayLayout([])).toHaveLength(0);
  });
});

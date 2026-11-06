import { NetworkMetrics } from './network-metrics';
import * as api from '../lib/api';
import type { NetworkInterface } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetworkInterfaces: vi.fn() };
});

const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);

function iface(index: number, location = 'us-east'): NetworkInterface {
  return {
    uid: `u${index}`,
    name: `web-${index}-eth0`,
    createdAt: new Date(0),
    network: 'prod-net',
    workloadName: 'web',
    instanceIndex: String(index),
    location,
    labels: {},
    addresses: [],
    holderAvailableStatus: 'True',
  } as NetworkInterface;
}

function interfacesResult(data: NetworkInterface[]) {
  return { data, isLoading: false, error: null, refetch: vi.fn() } as never;
}

function matrixFor(body: string, perInterface: (name: string, index: number) => number) {
  const params = new URLSearchParams(body);
  const start = Number(params.get('start'));
  const end = Number(params.get('end'));
  const step = parseInt(params.get('step') ?? '60', 10);
  const names = [...(params.get('query') ?? '').matchAll(/web-\d+-eth0/g)].map((m) => m[0]);
  return {
    status: 'success',
    data: {
      resultType: 'matrix',
      result: names.map((name, i) => {
        const values: Array<[number, string]> = [];
        for (let t = start; t <= end; t += step) values.push([t, String(perInterface(name, i))]);
        return { metric: { network: name }, values };
      }),
    },
  };
}

const fetchMock = vi.fn();

function serveTraffic(perInterface: (name: string, index: number) => number = (_n, i) => (i + 1) * 1000) {
  fetchMock.mockImplementation(async (_url: string, init: RequestInit) =>
    new Response(JSON.stringify(matrixFor(String(init.body), perInterface)), { status: 200 })
  );
}

function sentQueries(): string[] {
  return fetchMock.mock.calls.map(([, init]) => new URLSearchParams(String(init.body)).get('query') ?? '');
}

function renderMetrics(search = '') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/metrics${search}`]}>
        <NetworkMetrics projectId="demo" networkName="prod-net" />
      </MemoryRouter>
    </QueryClientProvider>
  );
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('NetworkMetrics', () => {
  it('says so when no instance is on the network', () => {
    useNetworkInterfacesMock.mockReturnValue(interfacesResult([]));
    renderMetrics();
    expect(screen.getByTestId('networking-plugin-metrics-empty')).toHaveTextContent(
      'No instances on this network yet'
    );
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('queries traffic for every interface and shows totals', async () => {
    useNetworkInterfacesMock.mockReturnValue(interfacesResult([iface(0), iface(1)]));
    serveTraffic();

    renderMetrics();

    const kpis = screen.getByTestId('networking-plugin-metrics-kpis');
    await waitFor(() => expect(kpis).toHaveTextContent('Traffic inLast 1h3.00 kb/s'));
    expect(screen.getByTestId('networking-plugin-traffic')).toHaveTextContent('2 instances');
    expect(sentQueries()).toEqual(
      expect.arrayContaining([
        expect.stringContaining('datum_vpc_interface_receive_bytes_total{'),
        expect.stringContaining('datum_vpc_interface_transmit_bytes_total{'),
      ])
    );
    expect(sentQueries()[0]).toContain('network=~"web-0-eth0|web-1-eth0"');
    const legend = screen.getByTestId('networking-plugin-traffic-legend');
    expect(within(legend).getByText('web · instance 0 · us-east')).toBeInTheDocument();
  });

  it('draws the busiest instances and rolls the rest into Other', async () => {
    useNetworkInterfacesMock.mockReturnValue(interfacesResult(Array.from({ length: 12 }, (_, i) => iface(i))));
    serveTraffic((name) => Number(name.split('-')[1]) * 1000);

    renderMetrics();

    const legend = await screen.findByTestId('networking-plugin-traffic-legend');
    await waitFor(() => expect(within(legend).getByText('Other')).toBeInTheDocument());
    expect(within(legend).getAllByRole('button')).toHaveLength(8);
    expect(within(legend).getByText('web · instance 11 · us-east')).toBeInTheDocument();
    expect(within(legend).queryByText('web · instance 0 · us-east')).not.toBeInTheDocument();
    expect(screen.getByTestId('networking-plugin-traffic')).toHaveTextContent(
      '12 instances · busiest 7 shown · 5 more in Other'
    );
  });

  it('isolates a series from the legend and restores it on a second click', async () => {
    useNetworkInterfacesMock.mockReturnValue(interfacesResult([iface(0), iface(1)]));
    serveTraffic();

    renderMetrics();

    const legend = await screen.findByTestId('networking-plugin-traffic-legend');
    const first = within(legend).getByText('web · instance 0 · us-east');
    const second = within(legend).getByText('web · instance 1 · us-east');
    fireEvent.click(first);
    expect(second).toHaveClass('line-through');
    expect(first).not.toHaveClass('line-through');
    fireEvent.click(first);
    expect(second).not.toHaveClass('line-through');
  });

  it('scopes queries to the instances picked in the URL', async () => {
    useNetworkInterfacesMock.mockReturnValue(
      interfacesResult([iface(0), iface(1, 'eu-west'), iface(2, 'eu-west')])
    );
    serveTraffic();

    renderMetrics('?regions=eu-west&instances=web-2-eth0');

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    for (const query of sentQueries()) expect(query).toContain('network="web-2-eth0"');
    expect(screen.getByTestId('networking-plugin-traffic')).toHaveTextContent('1 of 3 instances');
  });

  it('explains a metrics permission denial', async () => {
    useNetworkInterfacesMock.mockReturnValue(interfacesResult([iface(0)]));
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ message: 'forbidden' }), { status: 403 }));

    renderMetrics();

    await waitFor(() =>
      expect(screen.getAllByText("You don't have permission to view metrics")).toHaveLength(2)
    );
  });
});

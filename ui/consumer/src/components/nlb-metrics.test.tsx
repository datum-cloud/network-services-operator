import { NlbMetrics, NlbTrafficCard } from './nlb-metrics';
import type { NetworkLoadBalancer } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const nlb: NetworkLoadBalancer = {
  uid: 'nlb',
  name: 'edge-nlb',
  createdAt: new Date('2026-01-01T00:00:00Z'),
  networkName: 'kevnett',
  listeners: [
    { name: 'tcp-80', protocol: 'TCP', port: 80 },
    { name: 'udp-7777', protocol: 'UDP', port: 7777 },
  ],
  endpoints: [],
} as unknown as NetworkLoadBalancer;

/** Per-second rates for each listener and region, by metric. */
type Reported = Record<string, Array<{ listener: string; region: string; value: number }>>;

const DEFAULT_REPORTED: Reported = {
  receive_bytes: [
    { listener: 'tcp-80', region: 'us-east-1', value: 1000 },
    { listener: 'udp-7777', region: 'us-east-1', value: 250 },
  ],
  receive_packets: [
    { listener: 'tcp-80', region: 'us-east-1', value: 10 },
    { listener: 'udp-7777', region: 'us-east-1', value: 5 },
  ],
  receive_dropped_packets: [],
  backends: [
    { listener: 'tcp-80', region: 'us-east-1', value: 2 },
    { listener: 'udp-7777', region: 'us-east-1', value: 1 },
  ],
};

const fetchMock = vi.fn();

function serve(reported: Reported = DEFAULT_REPORTED) {
  fetchMock.mockImplementation(async (_url: string, init: RequestInit) => {
    const params = new URLSearchParams(String(init.body));
    const query = params.get('query') ?? '';
    const start = Number(params.get('start'));
    const end = Number(params.get('end'));
    const step = parseInt(params.get('step') ?? '60', 10);
    const metric = Object.keys(reported).find((m) => query.includes(`datum_nlb_listener_${m}`));
    const scale = query.includes('* 8') ? 8 : 1;
    const result = (metric ? reported[metric] : []).map(({ listener, region, value }) => {
      const values: Array<[number, string]> = [];
      for (let t = start; t <= end; t += step) values.push([t, String(value * scale)]);
      return { metric: { listener, region }, values };
    });
    return new Response(JSON.stringify({ status: 'success', data: { resultType: 'matrix', result } }), {
      status: 200,
    });
  });
}

function sentQueries(): string[] {
  return fetchMock.mock.calls.map(([, init]) => new URLSearchParams(String(init.body)).get('query') ?? '');
}

function renderWith(element: React.ReactNode, path = '/metrics') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>{element}</MemoryRouter>
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

describe('NlbMetrics', () => {
  it('queries inbound traffic and backends for this load balancer and shows totals', async () => {
    serve();
    renderWith(<NlbMetrics projectId="demo" nlb={nlb} />);

    const kpis = screen.getByTestId('networking-plugin-nlb-kpis');
    await waitFor(() => expect(kpis).toHaveTextContent('Throughput inLast 1h10.0 kb/s'));
    expect(kpis).toHaveTextContent('Packets inLast 1h15 pkt/s');
    expect(sentQueries()).toHaveLength(4);
    for (const query of sentQueries()) {
      expect(query).toContain('resource_name="edge-nlb"');
      expect(query).not.toContain('transmit');
    }
    expect(screen.getByTestId('networking-plugin-nlb-traffic')).toHaveTextContent('2 listeners');
  });

  it('draws each listener in the legend and isolates one on click', async () => {
    serve();
    renderWith(<NlbMetrics projectId="demo" nlb={nlb} />);

    const legend = await screen.findByTestId('networking-plugin-nlb-legend');
    const tcp = within(legend).getByText('TCP 80');
    const udp = within(legend).getByText('UDP 7777');
    fireEvent.click(tcp);
    expect(udp).toHaveClass('line-through');
    expect(tcp).not.toHaveClass('line-through');
    fireEvent.click(tcp);
    expect(udp).not.toHaveClass('line-through');
  });

  it('names the region when the load balancer reports from more than one', async () => {
    serve({
      ...DEFAULT_REPORTED,
      receive_bytes: [
        { listener: 'tcp-80', region: 'us-east-1', value: 1000 },
        { listener: 'tcp-80', region: 'eu-west-1', value: 500 },
      ],
    });
    renderWith(<NlbMetrics projectId="demo" nlb={nlb} />);

    const legend = await screen.findByTestId('networking-plugin-nlb-legend');
    await waitFor(() => expect(within(legend).getByText('TCP 80 · eu-west-1')).toBeInTheDocument());
  });

  it('keeps only the listeners picked in the URL', async () => {
    serve();
    renderWith(<NlbMetrics projectId="demo" nlb={nlb} />, '/metrics?listeners=udp-7777');

    const kpis = screen.getByTestId('networking-plugin-nlb-kpis');
    await waitFor(() => expect(kpis).toHaveTextContent('Throughput inLast 1h2.00 kb/s'));
    expect(screen.getByTestId('networking-plugin-nlb-traffic')).toHaveTextContent('1 of 2 listeners');
    // One listener left, so there's nothing to toggle.
    expect(screen.queryByTestId('networking-plugin-nlb-legend')).not.toBeInTheDocument();
  });

  it('says so when nothing was dropped', async () => {
    serve();
    renderWith(<NlbMetrics projectId="demo" nlb={nlb} />);

    await waitFor(() =>
      expect(screen.getByTestId('networking-plugin-nlb-dropped')).toHaveTextContent(
        'No dropped packets in this window'
      )
    );
  });

  it('explains a metrics permission denial', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ message: 'forbidden' }), { status: 403 }));
    renderWith(<NlbMetrics projectId="demo" nlb={nlb} />);

    await waitFor(() =>
      expect(screen.getAllByText("You don't have permission to view metrics")).toHaveLength(4)
    );
  });
});

describe('NlbTrafficCard', () => {
  it('shows the last hour of throughput and links to the metrics tab', async () => {
    serve();
    renderWith(<NlbTrafficCard projectId="demo" nlb={nlb} metricsHref="/nlb/edge-nlb/metrics" />, '/');

    expect(screen.getByRole('link', { name: 'View metrics →' })).toHaveAttribute('href', '/nlb/edge-nlb/metrics');
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(sentQueries()[0]).toContain('datum_nlb_listener_receive_bytes_total');
  });
});

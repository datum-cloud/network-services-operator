import { OTHER_SERIES } from './interface-metrics';
import { toSeries } from './metrics';
import {
  NLB_GROUP_LABELS,
  filterNlbSeries,
  formatCount,
  formatPacketsPerSecond,
  formatPacketsTick,
  nlbMetricQuery,
  nlbSeriesColors,
  nlbSeriesLabels,
  seriesFacets,
  splitSeriesKey,
} from './nlb-metrics';
import type { NetworkLoadBalancerListener } from '../schema';
import { describe, expect, it } from 'vitest';

const listeners: NetworkLoadBalancerListener[] = [
  { name: 'tcp-80', protocol: 'TCP', port: 80 },
  { name: 'udp-7777', protocol: 'UDP', port: 7777 },
];

function series(key: string, values: number[] = [1]) {
  return { key, points: values.map((v, i) => [i * 60_000, v] as [number, number]) };
}

describe('nlbMetricQuery', () => {
  const selector =
    'resourcemanager_datumapis_com_project_name="demo",resource_namespace="default",resource_name="edge-nlb"';

  it('reads inbound throughput as bits per second by listener and region', () => {
    expect(nlbMetricQuery('demo', 'edge-nlb', 'throughput', 60)).toBe(
      `sum by (listener, region) (rate(datum_nlb_listener_receive_bytes_total{${selector}}[300s])) * 8`
    );
  });

  it('reads packets and drops as a per-second rate over at least the step', () => {
    expect(nlbMetricQuery('demo', 'edge-nlb', 'packets', 900)).toBe(
      `sum by (listener, region) (rate(datum_nlb_listener_receive_packets_total{${selector}}[900s]))`
    );
    expect(nlbMetricQuery('demo', 'edge-nlb', 'dropped', 60)).toContain(
      'rate(datum_nlb_listener_receive_dropped_packets_total{'
    );
  });

  it('reads the backend gauge without a rate', () => {
    expect(nlbMetricQuery('demo', 'edge-nlb', 'backends', 60)).toBe(
      `max by (listener, region) (datum_nlb_listener_backends{${selector}})`
    );
  });

  it('never reads outbound counters', () => {
    for (const metric of ['throughput', 'packets', 'dropped', 'backends'] as const) {
      expect(nlbMetricQuery('demo', 'edge-nlb', metric, 60)).not.toContain('transmit');
    }
  });

  it('escapes quotes in names', () => {
    expect(nlbMetricQuery('de"mo', 'edge-nlb', 'backends', 60)).toContain(
      'resourcemanager_datumapis_com_project_name="de\\"mo"'
    );
  });
});

describe('series keys', () => {
  it('keys each series by listener and region', () => {
    const [s] = toSeries(
      {
        result: [{ metric: { listener: 'tcp-80', region: 'us-east-1' }, values: [[60, '2']] }],
      },
      NLB_GROUP_LABELS
    );
    expect(s.key).toBe('tcp-80|us-east-1');
    expect(splitSeriesKey(s.key)).toEqual({ listener: 'tcp-80', region: 'us-east-1' });
  });

  it('collects the regions and listeners that report', () => {
    expect(
      seriesFacets([
        [series('udp-7777|us-east-1'), series('tcp-80|eu-west-1')],
        [series('tcp-80|us-east-1')],
      ])
    ).toEqual({ regions: ['eu-west-1', 'us-east-1'], listeners: ['tcp-80', 'udp-7777'] });
  });

  it('keeps the picked regions and listeners, or everything when none are picked', () => {
    const all = [series('tcp-80|us-east-1'), series('tcp-80|eu-west-1'), series('udp-7777|us-east-1')];
    expect(filterNlbSeries(all, [], []).map((s) => s.key)).toHaveLength(3);
    expect(filterNlbSeries(all, ['us-east-1'], []).map((s) => s.key)).toEqual([
      'tcp-80|us-east-1',
      'udp-7777|us-east-1',
    ]);
    expect(filterNlbSeries(all, ['us-east-1'], ['tcp-80']).map((s) => s.key)).toEqual(['tcp-80|us-east-1']);
  });
});

describe('nlbSeriesLabels', () => {
  it('names a series by protocol and port when there is one region', () => {
    expect(nlbSeriesLabels(['tcp-80|us-east-1', 'udp-7777|us-east-1'], listeners)).toEqual({
      'tcp-80|us-east-1': 'TCP 80',
      'udp-7777|us-east-1': 'UDP 7777',
      [OTHER_SERIES]: 'Other',
    });
  });

  it('adds the region when more than one reports', () => {
    const labels = nlbSeriesLabels(['tcp-80|us-east-1', 'tcp-80|eu-west-1'], listeners);
    expect(labels['tcp-80|eu-west-1']).toBe('TCP 80 · eu-west-1');
  });

  it('falls back to the listener name for a listener the load balancer no longer has', () => {
    expect(nlbSeriesLabels(['gone|us-east-1'], listeners)['gone|us-east-1']).toBe('gone');
  });

  it('colors series in label order and Other in muted', () => {
    const keys = ['udp-7777|us-east-1', 'tcp-80|us-east-1'];
    const colors = nlbSeriesColors(keys, nlbSeriesLabels(keys, listeners));
    expect(colors['tcp-80|us-east-1']).toBe('#8884d8');
    expect(colors['udp-7777|us-east-1']).toBe('#82ca9d');
    expect(colors[OTHER_SERIES]).toBe('var(--muted-foreground)');
  });
});

describe('formatters', () => {
  it('formats packet rates with SI prefixes', () => {
    expect(formatPacketsPerSecond(0.0333)).toBe('0.03 pkt/s');
    expect(formatPacketsPerSecond(12.34)).toBe('12.3 pkt/s');
    expect(formatPacketsPerSecond(4_500)).toBe('4.5 kpkt/s');
    expect(formatPacketsPerSecond(Number.NaN)).toBe('—');
    expect(formatPacketsTick(2_000_000)).toBe('2 Mpkt/s');
  });

  it('formats counts as whole numbers', () => {
    expect(formatCount(2.6)).toBe('3');
    expect(formatCount(Number.POSITIVE_INFINITY)).toBe('—');
  });
});

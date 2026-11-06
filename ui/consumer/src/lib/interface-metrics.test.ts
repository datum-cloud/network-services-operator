import {
  OTHER_SERIES,
  chartKeys,
  filterInterfaces,
  formatBitsPerSecond,
  formatBitsTick,
  interfaceSeriesLabel,
  interfaceTrafficQuery,
  nextHiddenSeries,
  niceScale,
  rankSeriesKeys,
  rateWindow,
  resolveBrush,
  rollUpSeries,
  toGridRows,
  trafficSummary,
} from './interface-metrics';
import type { MetricSeries } from './metrics';
import type { NetworkInterface } from '../schema';
import { describe, expect, it } from 'vitest';

function iface(overrides: Partial<NetworkInterface> = {}): NetworkInterface {
  return {
    uid: 'u',
    name: 'web-0-eth0',
    createdAt: new Date(0),
    labels: {},
    addresses: [],
    holderAvailableStatus: 'True',
    ...overrides,
  } as NetworkInterface;
}

function series(key: string, points: Array<[number, number]>): MetricSeries {
  return { key, points };
}

describe('interfaceTrafficQuery', () => {
  it('selects one interface by exact match', () => {
    expect(interfaceTrafficQuery('proj', [iface()], 'receive', 60)).toBe(
      'sum by (network) (rate(datum_vpc_interface_receive_bytes_total{resourcemanager_datumapis_com_project_name="proj",resource_namespace="default",network="web-0-eth0"}[300s])) * 8'
    );
  });

  it('selects several interfaces by a sorted, deduplicated regexp', () => {
    const q = interfaceTrafficQuery(
      'proj',
      [iface({ name: 'web-1-eth0' }), iface({ name: 'web-0-eth0' }), iface({ name: 'web-1-eth0' })],
      'transmit',
      60
    );
    expect(q).toContain('datum_vpc_interface_transmit_bytes_total{');
    expect(q).toContain('network=~"web-0-eth0|web-1-eth0"');
  });

  it('escapes quotes and regexp metacharacters', () => {
    const q = interfaceTrafficQuery('p"x', [iface({ name: 'a.b' }), iface({ name: 'c' })], 'receive', 60);
    expect(q).toContain('resourcemanager_datumapis_com_project_name="p\\"x"');
    expect(q).toContain('network=~"a\\\\.b|c"');
  });

  it('is undefined without interfaces', () => {
    expect(interfaceTrafficQuery('proj', [], 'receive', 60)).toBeUndefined();
  });

  it('widens the rate window to the step so no sample is skipped', () => {
    expect(rateWindow(60)).toBe('300s');
    expect(rateWindow(3600)).toBe('3600s');
  });
});

describe('interfaceSeriesLabel', () => {
  it('names the workload instance and region', () => {
    expect(interfaceSeriesLabel(iface({ workloadName: 'web', instanceIndex: '0', location: 'us-east' }))).toBe(
      'web · instance 0 · us-east'
    );
  });

  it('falls back to the interface name', () => {
    expect(interfaceSeriesLabel(iface())).toBe('web-0-eth0');
  });
});

describe('filterInterfaces', () => {
  const all = [
    iface({ name: 'a', location: 'us-east' }),
    iface({ name: 'b', location: 'eu-west' }),
    iface({ name: 'c', location: 'us-east' }),
  ];

  it('covers every interface with no filters', () => {
    const r = filterInterfaces(all, [], []);
    expect(r.regions).toEqual(['eu-west', 'us-east']);
    expect(r.matching.map((i) => i.name)).toEqual(['a', 'b', 'c']);
  });

  it('narrows by region, then by instance within it', () => {
    expect(filterInterfaces(all, ['us-east'], []).matching.map((i) => i.name)).toEqual(['a', 'c']);
    const r = filterInterfaces(all, ['us-east'], ['c', 'b']);
    expect(r.pickedInstances).toEqual(['c']);
    expect(r.matching.map((i) => i.name)).toEqual(['c']);
  });

  it('ignores picks that match nothing', () => {
    const r = filterInterfaces(all, ['ap-south'], ['zzz']);
    expect(r.pickedRegions).toEqual([]);
    expect(r.matching).toHaveLength(3);
  });
});

describe('series limiting', () => {
  const rx = [series('a', [[0, 1]]), series('b', [[0, 10]]), series('c', [[0, 5]])];
  const tx = [series('a', [[0, 100]])];

  it('ranks by combined receive and transmit volume', () => {
    expect(rankSeriesKeys([rx, tx])).toEqual(['a', 'b', 'c']);
  });

  it('keeps the busiest and rolls the rest into Other', () => {
    expect(chartKeys(['a', 'b', 'c'], 2)).toEqual(['a', OTHER_SERIES]);
    expect(chartKeys(['a', 'b'], 2)).toEqual(['a', 'b']);
    expect(chartKeys(['a', 'b', 'c'], undefined)).toEqual(['a', 'b', 'c']);
  });

  it('sums the rolled-up series per timestamp', () => {
    const rolled = rollUpSeries(
      [series('a', [[0, 1]]), series('b', [[0, 2], [60, 3]]), series('c', [[0, 4]])],
      ['a', OTHER_SERIES]
    );
    expect(rolled).toEqual([series('a', [[0, 1]]), series(OTHER_SERIES, [[0, 6], [60, 3]])]);
  });
});

describe('toGridRows', () => {
  it('lays series on the step grid with gaps as null', () => {
    expect(toGridRows([series('a', [[0, 1], [120, 3]])], ['a', 'b'], 0, 120, 60)).toEqual([
      { timestamp: 0, a: 1, b: null },
      { timestamp: 60, a: null, b: null },
      { timestamp: 120, a: 3, b: null },
    ]);
  });
});

describe('trafficSummary', () => {
  it('reports the latest and peak totals', () => {
    expect(trafficSummary([series('a', [[0, 1], [60, 5]]), series('b', [[0, 9], [60, 2]])])).toEqual({
      latest: 7,
      peak: 10,
    });
    expect(trafficSummary([])).toEqual({});
  });
});

describe('niceScale', () => {
  it('rounds the top up to a round tick', () => {
    expect(niceScale(43)).toEqual({ domain: [0, 60], ticks: [0, 20, 40, 60] });
    expect(niceScale(9)).toEqual({ domain: [0, 10], ticks: [0, 5, 10] });
    expect(niceScale(0)).toEqual({ domain: [0, 1], ticks: [0, 1] });
  });
});

describe('nextHiddenSeries', () => {
  const names = ['a', 'b', 'c'];

  it('isolates on click and shows everything on a second click', () => {
    const isolated = nextHiddenSeries(names, new Set(), 'b');
    expect([...isolated]).toEqual(['a', 'c']);
    expect(nextHiddenSeries(names, isolated, 'b').size).toBe(0);
  });

  it('toggles one series on shift-click but never hides the last', () => {
    expect([...nextHiddenSeries(names, new Set(), 'a', { shiftKey: true })]).toEqual(['a']);
    expect([...nextHiddenSeries(names, new Set(['a', 'b']), 'c', { metaKey: true })]).toEqual(['a', 'b']);
    expect([...nextHiddenSeries(names, new Set(['a']), 'a', { ctrlKey: true })]).toEqual([]);
  });
});

describe('resolveBrush', () => {
  const bounds = { start: 0, end: 600_000, stepMs: 60_000 };

  it('orders the ends and includes the last bucket', () => {
    expect(resolveBrush(240_000, 60_000, bounds)).toEqual({ start: 60_000, end: 300_000 });
  });

  it('clamps to the window and ignores a click without a drag', () => {
    expect(resolveBrush(540_000, 600_000, bounds)).toEqual({ start: 540_000, end: 600_000 });
    expect(resolveBrush(60_000, 60_000, bounds)).toBeUndefined();
  });
});

describe('formatting', () => {
  it.each([
    [0, '0 b/s'],
    [999, '999 b/s'],
    [1500, '1.50 kb/s'],
    [25_000_000, '25.0 Mb/s'],
    [-5, '0 b/s'],
    [Number.NaN, '—'],
  ])('formatBitsPerSecond(%s) -> %s', (value, expected) => {
    expect(formatBitsPerSecond(value)).toBe(expected);
  });

  it('keeps axis ticks short', () => {
    expect(formatBitsTick(40_000_000)).toBe('40 Mb/s');
    expect(formatBitsTick(2_500_000)).toBe('2.5 Mb/s');
  });
});

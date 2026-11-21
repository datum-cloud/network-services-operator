import { OTHER_SERIES, naturalCompare, rateWindow, seriesColor } from './interface-metrics';
import { SERIES_KEY_SEPARATOR, type MetricSeries } from './metrics';
import type { NetworkLoadBalancerListener } from '../schema';

/**
 * Per-listener traffic for a network load balancer, recorded by the
 * resource-metrics aggregator (datum-cloud/infra#9055). Each series carries
 * `resource_name` (the NLB), `listener` (the listener's name) and `region`.
 *
 * Only inbound counters are read. The gateway forwards with direct server
 * return, so replies go from the backend to the client without passing the
 * gateway and the transmit series stay at 0.
 */
export const NLB_GROUP_LABELS = ['listener', 'region'] as const;

const NLB_NAMESPACE = 'default';

export type NlbMetric = 'throughput' | 'packets' | 'dropped' | 'backends';

const METRICS: Record<NlbMetric, { name: string; counter: boolean; scale?: number }> = {
  throughput: { name: 'datum_nlb_listener_receive_bytes_total', counter: true, scale: 8 },
  packets: { name: 'datum_nlb_listener_receive_packets_total', counter: true },
  dropped: { name: 'datum_nlb_listener_receive_dropped_packets_total', counter: true },
  backends: { name: 'datum_nlb_listener_backends', counter: false },
};

function escapeValue(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

/**
 * One series per listener and region. Counters become a per-second rate
 * (bits for throughput); the backend gauge is read as is.
 */
export function nlbMetricQuery(
  projectId: string,
  nlbName: string,
  metric: NlbMetric,
  stepSeconds: number
): string {
  const { name, counter, scale } = METRICS[metric];
  const selector = [
    `resourcemanager_datumapis_com_project_name="${escapeValue(projectId)}"`,
    `resource_namespace="${NLB_NAMESPACE}"`,
    `resource_name="${escapeValue(nlbName)}"`,
  ].join(',');
  const by = NLB_GROUP_LABELS.join(', ');
  const query = counter
    ? `sum by (${by}) (rate(${name}{${selector}}[${rateWindow(stepSeconds)}]))`
    : `max by (${by}) (${name}{${selector}})`;
  return scale ? `${query} * ${scale}` : query;
}

export function splitSeriesKey(key: string): { listener: string; region: string } {
  const at = key.indexOf(SERIES_KEY_SEPARATOR);
  if (at < 0) return { listener: key, region: '' };
  return { listener: key.slice(0, at), region: key.slice(at + 1) };
}

export function listenerLabel(listener: NetworkLoadBalancerListener): string {
  return `${listener.protocol} ${listener.port}`;
}

/** Regions and listeners that report, for the toolbar's filters. */
export function seriesFacets(groups: ReadonlyArray<readonly MetricSeries[]>): {
  regions: string[];
  listeners: string[];
} {
  const regions = new Set<string>();
  const listeners = new Set<string>();
  for (const group of groups) {
    for (const s of group) {
      const { listener, region } = splitSeriesKey(s.key);
      if (region) regions.add(region);
      if (listener) listeners.add(listener);
    }
  }
  return { regions: [...regions].sort(naturalCompare), listeners: [...listeners].sort(naturalCompare) };
}

/**
 * Chart labels: the listener's protocol and port, with the region when the
 * load balancer reports from more than one.
 */
export function nlbSeriesLabels(
  keys: readonly string[],
  listeners: readonly NetworkLoadBalancerListener[]
): Record<string, string> {
  const byName = new Map(listeners.map((l) => [l.name, l]));
  const regions = new Set(keys.map((k) => splitSeriesKey(k).region).filter(Boolean));
  const labels: Record<string, string> = { [OTHER_SERIES]: 'Other' };
  for (const key of keys) {
    if (key === OTHER_SERIES) continue;
    const { listener, region } = splitSeriesKey(key);
    const known = byName.get(listener);
    const name = known ? listenerLabel(known) : listener;
    labels[key] = regions.size > 1 && region ? `${name} · ${region}` : name;
  }
  return labels;
}

/** Colors follow the label order, so a series keeps its color across charts. */
export function nlbSeriesColors(
  keys: readonly string[],
  labels: Record<string, string>
): Record<string, string> {
  const ordered = keys
    .filter((k) => k !== OTHER_SERIES)
    .sort((a, b) => naturalCompare(labels[a] ?? a, labels[b] ?? b));
  const colors: Record<string, string> = Object.fromEntries(ordered.map((k, i) => [k, seriesColor(i)]));
  colors[OTHER_SERIES] = 'var(--muted-foreground)';
  return colors;
}

/** Keeps the series in the picked regions and listeners; none picked keeps them all. */
export function filterNlbSeries(
  series: readonly MetricSeries[],
  regions: readonly string[],
  listeners: readonly string[]
): MetricSeries[] {
  const regionSet = new Set(regions);
  const listenerSet = new Set(listeners);
  return series.filter((s) => {
    const { listener, region } = splitSeriesKey(s.key);
    return (regionSet.size === 0 || regionSet.has(region)) && (listenerSet.size === 0 || listenerSet.has(listener));
  });
}

const SI_PREFIXES = ['', 'k', 'M', 'G', 'T'];

function scaleSi(value: number): [number, string] {
  let n = Math.max(0, value);
  let i = 0;
  while (n >= 1000 && i < SI_PREFIXES.length - 1) {
    n /= 1000;
    i += 1;
  }
  return [n, SI_PREFIXES[i]];
}

export function formatPacketsPerSecond(value: number): string {
  if (!Number.isFinite(value)) return '—';
  const [n, prefix] = scaleSi(value);
  const decimals = n < 10 ? 2 : n < 100 ? 1 : 0;
  return `${Number(n.toFixed(decimals))} ${prefix}pkt/s`;
}

export function formatPacketsTick(value: number): string {
  if (!Number.isFinite(value)) return '';
  const [n, prefix] = scaleSi(value);
  return `${Number(n.toFixed(n < 10 ? 1 : 0))} ${prefix}pkt/s`;
}

export function formatCount(value: number): string {
  if (!Number.isFinite(value)) return '—';
  return String(Math.round(value));
}

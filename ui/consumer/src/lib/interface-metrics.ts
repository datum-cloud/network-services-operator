import type { MetricSeries } from './metrics';
import type { NetworkInterface } from '../schema';

export const INTERFACE_RECEIVE_METRIC = 'datum_vpc_interface_receive_bytes_total';
export const INTERFACE_TRANSMIT_METRIC = 'datum_vpc_interface_transmit_bytes_total';
export const INTERFACE_LABEL = 'network';
export const OTHER_SERIES = '__other__';
export const MAX_CHART_SERIES = 8;

const NETWORK_INTERFACE_NAMESPACE = 'default';
const MIN_RATE_WINDOW_SECONDS = 300;

export type TrafficDirection = 'receive' | 'transmit';

function escapeValue(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

function escapeRegexp(value: string): string {
  return escapeValue(value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
}

function interfaceMatcher(names: readonly string[]): string | undefined {
  const unique = [...new Set(names.filter(Boolean))].sort();
  if (unique.length === 0) return undefined;
  if (unique.length === 1) return `${INTERFACE_LABEL}="${escapeValue(unique[0])}"`;
  return `${INTERFACE_LABEL}=~"${unique.map(escapeRegexp).join('|')}"`;
}

export function rateWindow(stepSeconds: number): string {
  return `${Math.max(MIN_RATE_WINDOW_SECONDS, stepSeconds)}s`;
}

export function interfaceTrafficQuery(
  projectId: string,
  interfaces: readonly NetworkInterface[],
  direction: TrafficDirection,
  stepSeconds: number
): string | undefined {
  const matcher = interfaceMatcher(interfaces.map((i) => i.name));
  if (!matcher) return undefined;
  const metric = direction === 'receive' ? INTERFACE_RECEIVE_METRIC : INTERFACE_TRANSMIT_METRIC;
  const selector = [
    `resourcemanager_datumapis_com_project_name="${escapeValue(projectId)}"`,
    `resource_namespace="${NETWORK_INTERFACE_NAMESPACE}"`,
    matcher,
  ].join(',');
  return `sum by (${INTERFACE_LABEL}) (rate(${metric}{${selector}}[${rateWindow(stepSeconds)}])) * 8`;
}

export function interfaceSeriesLabel(iface: NetworkInterface): string {
  const owner = iface.workloadName ?? iface.name;
  const parts = [iface.instanceIndex !== undefined ? `${owner} · instance ${iface.instanceIndex}` : owner];
  if (iface.location) parts.push(iface.location);
  return parts.join(' · ');
}

export function interfaceSeriesLabels(interfaces: readonly NetworkInterface[]): Record<string, string> {
  const labels: Record<string, string> = Object.fromEntries(
    interfaces.map((i) => [i.name, interfaceSeriesLabel(i)])
  );
  labels[OTHER_SERIES] = 'Other';
  return labels;
}

export interface InterfaceFilterResult {
  regions: string[];
  pickedRegions: string[];
  instanceOptions: NetworkInterface[];
  pickedInstances: string[];
  matching: NetworkInterface[];
}

export function filterInterfaces(
  interfaces: readonly NetworkInterface[],
  regions: readonly string[],
  instances: readonly string[]
): InterfaceFilterResult {
  const allRegions = [...new Set(interfaces.map((i) => i.location).filter((r): r is string => !!r))].sort();
  const pickedRegions = regions.filter((r) => allRegions.includes(r));
  const regionSet = new Set(pickedRegions);
  const instanceOptions =
    regionSet.size > 0 ? interfaces.filter((i) => !!i.location && regionSet.has(i.location)) : [...interfaces];
  const picked = new Set(instances);
  const pickedInstances = instanceOptions.filter((i) => picked.has(i.name)).map((i) => i.name);
  const matching = pickedInstances.length > 0 ? instanceOptions.filter((i) => picked.has(i.name)) : instanceOptions;
  return { regions: allRegions, pickedRegions, instanceOptions, pickedInstances, matching };
}

function volume(series: MetricSeries | undefined): number {
  return series?.points.reduce((sum, [, v]) => sum + v, 0) ?? 0;
}

export const naturalCompare = (a: string, b: string) =>
  a.localeCompare(b, undefined, { numeric: true, sensitivity: 'base' });

export function rankSeriesKeys(groups: ReadonlyArray<readonly MetricSeries[]>): string[] {
  const totals = new Map<string, number>();
  for (const group of groups) {
    for (const s of group) totals.set(s.key, (totals.get(s.key) ?? 0) + volume(s));
  }
  return [...totals.entries()].sort((a, b) => b[1] - a[1] || naturalCompare(a[0], b[0])).map(([k]) => k);
}

export function chartKeys(ranked: readonly string[], limit: number | undefined): string[] {
  if (!limit || ranked.length <= limit) return [...ranked];
  return [...ranked.slice(0, limit - 1), OTHER_SERIES];
}

export function rollUpSeries(series: readonly MetricSeries[], keys: readonly string[]): MetricSeries[] {
  if (!keys.includes(OTHER_SERIES)) return series.filter((s) => keys.includes(s.key));
  const kept: MetricSeries[] = [];
  const other = new Map<number, number>();
  for (const s of series) {
    if (keys.includes(s.key)) {
      kept.push(s);
      continue;
    }
    for (const [ts, v] of s.points) other.set(ts, (other.get(ts) ?? 0) + v);
  }
  if (other.size > 0) {
    kept.push({ key: OTHER_SERIES, points: [...other.entries()].sort((a, b) => a[0] - b[0]) });
  }
  return kept;
}

const BASE_COLORS = [
  '#8884d8',
  '#82ca9d',
  '#ffc658',
  '#ff7300',
  '#e74c3c',
  '#9b59b6',
  '#1abc9c',
  '#f39c12',
  '#3498db',
  '#2ecc71',
  '#e67e22',
  '#34495e',
];
const GOLDEN_RATIO = 0.618033988749;

export function seriesColor(index: number): string {
  if (index < BASE_COLORS.length) return BASE_COLORS[index];
  const n = index - BASE_COLORS.length;
  return `hsl(${Math.round((n * GOLDEN_RATIO * 360) % 360)}, ${[70, 85, 60, 90, 75][n % 5]}%, ${[50, 40, 60, 45, 55][n % 5]}%)`;
}

export function seriesColors(
  interfaces: readonly NetworkInterface[],
  labels: Record<string, string>
): Record<string, string> {
  const ordered = [...interfaces].sort((a, b) => naturalCompare(labels[a.name] ?? a.name, labels[b.name] ?? b.name));
  const colors: Record<string, string> = Object.fromEntries(ordered.map((iface, i) => [iface.name, seriesColor(i)]));
  colors[OTHER_SERIES] = 'var(--muted-foreground)';
  return colors;
}

export type ChartRow = { timestamp: number } & Record<string, number | null>;

export function toGridRows(
  series: readonly MetricSeries[],
  keys: readonly string[],
  start: number,
  end: number,
  stepMs: number
): ChartRow[] {
  const byKey = new Map(series.map((s) => [s.key, new Map(s.points)]));
  const rows: ChartRow[] = [];
  for (let ts = start; ts <= end; ts += stepMs) {
    const row: ChartRow = { timestamp: ts };
    for (const key of keys) row[key] = byKey.get(key)?.get(ts) ?? null;
    rows.push(row);
  }
  return rows;
}

export function totalSeries(series: readonly MetricSeries[]): Array<[number, number]> {
  const totals = new Map<number, number>();
  for (const s of series) for (const [ts, v] of s.points) totals.set(ts, (totals.get(ts) ?? 0) + v);
  return [...totals.entries()].sort((a, b) => a[0] - b[0]);
}

export function trafficSummary(series: readonly MetricSeries[] | undefined): { latest?: number; peak?: number } {
  const totals = totalSeries(series ?? []);
  if (totals.length === 0) return {};
  return { latest: totals[totals.length - 1][1], peak: Math.max(...totals.map(([, v]) => v)) };
}

export function rowsMax(rows: readonly ChartRow[], keys: readonly string[]): number {
  let max = 0;
  for (const row of rows) {
    for (const key of keys) {
      const v = row[key];
      if (typeof v === 'number' && v > max) max = v;
    }
  }
  return max;
}

export function niceScale(max: number, tickCount = 5): { domain: [number, number]; ticks: number[] } {
  if (!(max > 0)) return { domain: [0, 1], ticks: [0, 1] };
  const rough = max / Math.max(tickCount - 1, 1);
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const normalized = rough / magnitude;
  const step = (normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10) * magnitude;
  const top = Math.ceil((max * 1.05) / step) * step;
  const ticks: number[] = [];
  for (let v = 0; v <= top + step * 0.001; v += step) ticks.push(v);
  return { domain: [0, top], ticks };
}

export function timeTicks(start: number, end: number, count = 6): number[] {
  if (end <= start) return [start];
  const step = (end - start) / (count - 1);
  return Array.from({ length: count }, (_, i) => Math.round(start + step * i));
}

export type LegendModifiers = { shiftKey?: boolean; metaKey?: boolean; ctrlKey?: boolean };

export function nextHiddenSeries(
  names: readonly string[],
  hidden: ReadonlySet<string>,
  clicked: string,
  modifiers: LegendModifiers = {}
): Set<string> {
  if (!names.includes(clicked)) return new Set(hidden);
  if (modifiers.shiftKey || modifiers.metaKey || modifiers.ctrlKey) {
    const next = new Set(hidden);
    if (next.has(clicked)) {
      next.delete(clicked);
      return next;
    }
    if (names.filter((n) => !next.has(n)).length > 1) next.add(clicked);
    return next;
  }
  const onlyThisVisible = names.every((n) => n === clicked || hidden.has(n));
  return onlyThisVisible ? new Set() : new Set(names.filter((n) => n !== clicked));
}

export function resolveBrush(
  a: number,
  b: number,
  bounds: { start: number; end: number; stepMs: number }
): { start: number; end: number } | undefined {
  const left = Math.max(bounds.start, Math.min(a, b));
  const right = Math.min(bounds.end, Math.max(a, b) + bounds.stepMs);
  if (!(right - left >= bounds.stepMs) || Math.min(a, b) === Math.max(a, b)) return undefined;
  return { start: left, end: right };
}

const BIT_UNITS = ['b/s', 'kb/s', 'Mb/s', 'Gb/s', 'Tb/s'];

function scaleBits(value: number): [number, string] {
  let n = Math.max(0, value);
  let i = 0;
  while (n >= 1000 && i < BIT_UNITS.length - 1) {
    n /= 1000;
    i += 1;
  }
  return [n, BIT_UNITS[i]];
}

export function formatBitsPerSecond(value: number): string {
  if (!Number.isFinite(value)) return '—';
  const [n, unit] = scaleBits(value);
  const decimals = unit === 'b/s' ? 0 : n < 10 ? 2 : n < 100 ? 1 : 0;
  return `${n.toFixed(decimals)} ${unit}`;
}

export function formatBitsTick(value: number): string {
  if (!Number.isFinite(value)) return '';
  const [n, unit] = scaleBits(value);
  return `${Number(n.toFixed(n < 10 ? 1 : 0))} ${unit}`;
}

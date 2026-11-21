import { MetricsToolbar } from './metrics-toolbar';
import { MetricKpiCard } from './metric-kpi-card';
import { TrafficLegendSkeleton } from './skeletons';
import { SeriesLegend, TrafficChart, useHiddenSeries } from './traffic-chart';
import {
  MAX_CHART_SERIES,
  OTHER_SERIES,
  chartKeys,
  formatBitsPerSecond,
  naturalCompare,
  rankSeriesKeys,
  resolveBrush,
  rollUpSeries,
  toGridRows,
  totalSeries,
  trafficSummary,
  type ChartRow,
} from '../lib/interface-metrics';
import {
  isMetricsDenied,
  useMetricsRange,
  type MetricSeries,
  type QueryWindow,
  type RangeResult,
} from '../lib/metrics';
import {
  rangeLabelFor,
  resolveRange,
  serializeAbsoluteRange,
  stepSecondsFor,
  useMetricsControls,
  useNow,
  useRangeZoom,
} from '../lib/metrics-controls';
import {
  NLB_GROUP_LABELS,
  filterNlbSeries,
  formatCount,
  formatPacketsPerSecond,
  formatPacketsTick,
  listenerLabel,
  nlbMetricQuery,
  nlbSeriesColors,
  nlbSeriesLabels,
  seriesFacets,
  type NlbMetric,
} from '../lib/nlb-metrics';
import type { NetworkLoadBalancer } from '../schema';
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@datum-cloud/datum-ui/card';
import { Text } from '@datum-cloud/datum-ui/typography';
import { useMemo } from 'react';
import { Link, useNavigate } from 'react-router';

const SYNC_ID = 'networking-plugin-nlb-traffic';
const EMPTY_SERIES: MetricSeries[] = [];
const OVERVIEW_RANGE = 'now-1h';
const OVERVIEW_REFRESH_MS = 60_000;
/** Below this, the backends chart still ticks in whole numbers. */
const BACKENDS_MIN_SCALE = 4;

const ZOOM_HINT = 'Drag across a chart to zoom in · double-click to zoom back out';
const INBOUND_NOTE =
  "Inbound traffic only. Replies go from your backends straight to clients, so they don't pass the load balancer.";

function useNlbSeries(
  projectId: string | undefined,
  nlbName: string,
  metric: NlbMetric,
  window: QueryWindow,
  enabled = true
) {
  const query = projectId && enabled ? nlbMetricQuery(projectId, nlbName, metric, window.stepSeconds) : undefined;
  return useMetricsRange(projectId, query, { ...window, groupLabel: NLB_GROUP_LABELS });
}

function gridRows(data: RangeResult | undefined, series: readonly MetricSeries[], keys: readonly string[]): ChartRow[] {
  if (!data) return [];
  return toGridRows(rollUpSeries(series, keys), keys, data.start, data.end, data.stepMs);
}

/**
 * Keys shared by every chart, ranked by traffic and capped at the busiest
 * few plus Other, with labels and colors that match across charts.
 */
function useSeriesKeys(nlb: NetworkLoadBalancer, groups: ReadonlyArray<readonly MetricSeries[]>) {
  return useMemo(() => {
    const ranked = rankSeriesKeys(groups);
    const labels = nlbSeriesLabels(ranked, nlb.listeners);
    const keys = chartKeys(ranked, MAX_CHART_SERIES).sort((a, b) =>
      a === OTHER_SERIES ? 1 : b === OTHER_SERIES ? -1 : naturalCompare(labels[a] ?? a, labels[b] ?? b)
    );
    return { keys, labels, colors: nlbSeriesColors(ranked, labels), reporting: ranked.length };
  }, [groups, nlb.listeners]);
}

export function NlbMetrics({ projectId, nlb }: { projectId: string | undefined; nlb: NetworkLoadBalancer }) {
  const controls = useMetricsControls();
  const window = controls.window;
  const throughput = useNlbSeries(projectId, nlb.name, 'throughput', window);
  const packets = useNlbSeries(projectId, nlb.name, 'packets', window);
  const dropped = useNlbSeries(projectId, nlb.name, 'dropped', window);
  const backends = useNlbSeries(projectId, nlb.name, 'backends', window);

  const raw = useMemo(
    () => [throughput.data, packets.data, dropped.data, backends.data].map((d) => d?.series ?? EMPTY_SERIES),
    [throughput.data, packets.data, dropped.data, backends.data]
  );
  const facets = useMemo(() => seriesFacets(raw), [raw]);
  const listenerNames = useMemo(() => nlb.listeners.map((l) => l.name), [nlb.listeners]);
  const pickedRegions = useMemo(
    () => controls.regions.filter((r) => facets.regions.includes(r)),
    [controls.regions, facets.regions]
  );
  const pickedListeners = useMemo(
    () => controls.listeners.filter((l) => listenerNames.includes(l)),
    [controls.listeners, listenerNames]
  );
  const filtered = useMemo(
    () => raw.map((series) => filterNlbSeries(series, pickedRegions, pickedListeners)),
    [raw, pickedRegions, pickedListeners]
  );
  const [throughputSeries, packetSeries, droppedSeries, backendSeries] = filtered;

  const { keys, labels, colors, reporting } = useSeriesKeys(nlb, filtered);
  const { hidden, toggle } = useHiddenSeries(keys);

  const grid = throughput.data ?? packets.data ?? dropped.data ?? backends.data;
  const start = grid?.start ?? controls.resolved.start;
  const end = grid?.end ?? controls.resolved.end;
  const rows = useMemo(
    () => ({
      throughput: gridRows(throughput.data, throughputSeries, keys),
      packets: gridRows(packets.data, packetSeries, keys),
      dropped: gridRows(dropped.data, droppedSeries, keys),
      backends: gridRows(backends.data, backendSeries, keys),
    }),
    [throughput.data, packets.data, dropped.data, backends.data, throughputSeries, packetSeries, droppedSeries, backendSeries, keys]
  );

  const { zoom: onZoom, reset: onResetZoom } = useRangeZoom(controls.range, controls.setRange, {
    start,
    end,
    stepMs: window.stepSeconds * 1000,
  });

  const regionOptions = useMemo(() => facets.regions.map((r) => ({ value: r, label: r })), [facets.regions]);
  const listenerOptions = useMemo(
    () =>
      nlb.listeners
        .map((l) => ({ value: l.name, label: listenerLabel(l) }))
        .sort((a, b) => naturalCompare(a.label, b.label)),
    [nlb.listeners]
  );

  const throughputSummary = trafficSummary(throughputSeries);
  const packetSummary = trafficSummary(packetSeries);
  const droppedSummary = trafficSummary(droppedSeries);
  const queries = [throughput, packets, dropped, backends];
  const denied = queries.some((q) => isMetricsDenied(q.error));
  const loading = throughput.isLoading || packets.isLoading || dropped.isLoading;
  const rangeLabel = rangeLabelFor(controls.resolved);
  const format = (value: number | undefined, f: (n: number) => string) => (value === undefined ? undefined : f(value));
  const kpis = [
    {
      title: 'Throughput in',
      value: format(throughputSummary.latest, formatBitsPerSecond),
      points: totalSeries(throughputSeries),
      color: 'var(--color-chart-1)',
    },
    {
      title: 'Packets in',
      value: format(packetSummary.latest, formatPacketsPerSecond),
      points: totalSeries(packetSeries),
      color: 'var(--color-chart-2)',
    },
    {
      title: 'Dropped packets',
      value: format(droppedSummary.latest, formatPacketsPerSecond),
      points: totalSeries(droppedSeries),
      color: 'var(--color-chart-3)',
    },
    {
      title: 'Peak throughput',
      value: format(throughputSummary.peak, formatBitsPerSecond),
      points: totalSeries(throughputSeries),
      color: 'var(--color-chart-1)',
    },
  ];

  const listeners = nlb.listeners.length;
  const scope =
    pickedListeners.length > 0
      ? `${pickedListeners.length} of ${listeners} listeners`
      : `${listeners} ${listeners === 1 ? 'listener' : 'listeners'}`;
  const omitted = reporting - keys.filter((k) => k !== OTHER_SERIES).length;
  const chart = {
    keys,
    labels,
    colors,
    hidden,
    start,
    end,
    syncId: SYNC_ID,
    onZoom,
    onResetZoom,
  };

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-nlb-metrics">
      <MetricsToolbar
        controls={controls}
        regionOptions={regionOptions}
        instanceOptions={listenerOptions}
        pickedRegions={pickedRegions}
        pickedInstances={pickedListeners}
        instanceFilter={{
          placeholder: 'All listeners',
          emptyContent: 'No listeners found.',
          onValueChange: controls.setListeners,
        }}
      />

      <div className="grid grid-cols-2 gap-6 lg:grid-cols-4" data-testid="networking-plugin-nlb-kpis">
        {kpis.map((card) => (
          <MetricKpiCard
            key={card.title}
            title={card.title}
            rangeLabel={rangeLabel}
            value={card.value}
            points={card.points}
            color={card.color}
            isLoading={loading}
            denied={denied}
          />
        ))}
      </div>

      <Card size="sm" sectioned className="w-full overflow-hidden" data-testid="networking-plugin-nlb-traffic">
        <CardHeader size="sm" bordered>
          <CardTitle className="text-sm">Traffic</CardTitle>
          <CardAction>
            <Text size="xs" textColor="muted">
              {keys.includes(OTHER_SERIES) && omitted > 0
                ? `${scope} · busiest ${keys.length - 1} shown · ${omitted} more in Other`
                : scope}
            </Text>
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {loading && keys.length === 0 ? (
            <TrafficLegendSkeleton count={Math.min(listeners, MAX_CHART_SERIES)} other={false} />
          ) : (
            <SeriesLegend
              keys={keys}
              labels={labels}
              colors={colors}
              hidden={hidden}
              onToggle={toggle}
              testId="networking-plugin-nlb-legend"
            />
          )}
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <TrafficChart
              {...chart}
              title="Throughput in"
              testId="networking-plugin-nlb-throughput"
              rows={rows.throughput}
              isLoading={throughput.isLoading}
              error={throughput.error}
            />
            <TrafficChart
              {...chart}
              title="Packets in"
              testId="networking-plugin-nlb-packets"
              rows={rows.packets}
              isLoading={packets.isLoading}
              error={packets.error}
              formatValue={formatPacketsPerSecond}
              formatTick={formatPacketsTick}
            />
            <TrafficChart
              {...chart}
              title="Dropped packets"
              testId="networking-plugin-nlb-dropped"
              rows={rows.dropped}
              isLoading={dropped.isLoading}
              error={dropped.error}
              formatValue={formatPacketsPerSecond}
              formatTick={formatPacketsTick}
              emptyMessage="No dropped packets in this window"
            />
            <TrafficChart
              {...chart}
              title="Backends"
              testId="networking-plugin-nlb-backends-chart"
              rows={rows.backends}
              isLoading={backends.isLoading}
              error={backends.error}
              formatValue={formatCount}
              formatTick={formatCount}
              minScale={BACKENDS_MIN_SCALE}
              emptyMessage="No backends reported in this window"
            />
          </div>
          <Text as="p" size="xs" textColor="muted">
            {ZOOM_HINT}
          </Text>
          <Text as="p" size="xs" textColor="muted">
            {INBOUND_NOTE}
          </Text>
        </CardContent>
      </Card>
    </div>
  );
}

/**
 * The last hour of inbound throughput on the overview. Dragging across it
 * opens the Metrics tab on that window.
 */
export function NlbTrafficCard({
  projectId,
  nlb,
  metricsHref,
}: {
  projectId: string | undefined;
  nlb: NetworkLoadBalancer;
  metricsHref: string;
}) {
  const navigate = useNavigate();
  const now = useNow(OVERVIEW_REFRESH_MS);
  const window = useMemo(() => {
    const range = resolveRange(OVERVIEW_RANGE, now);
    return { start: range.start, end: range.end, stepSeconds: stepSecondsFor('auto', range.end - range.start) };
  }, [now]);
  const throughput = useNlbSeries(projectId, nlb.name, 'throughput', window);
  const groups = useMemo(() => [throughput.data?.series ?? EMPTY_SERIES], [throughput.data]);
  const { keys, labels, colors } = useSeriesKeys(nlb, groups);
  const { hidden, toggle } = useHiddenSeries(keys);
  const rows = useMemo(() => gridRows(throughput.data, groups[0], keys), [throughput.data, groups, keys]);
  const start = throughput.data?.start ?? window.start;
  const end = throughput.data?.end ?? window.end;

  const openZoomed = (from: number, to: number) => {
    const next = resolveBrush(from, to, { start, end, stepMs: window.stepSeconds * 1000 });
    if (!next) return;
    navigate(`${metricsHref}?timeRange=${serializeAbsoluteRange(next.start, next.end)}`);
  };

  return (
    <Card size="sm" sectioned className="w-full min-w-0 overflow-hidden" data-testid="networking-plugin-nlb-traffic-card">
      <CardHeader size="sm" bordered>
        <CardTitle className="text-sm">Traffic</CardTitle>
        <CardAction>
          <Link to={metricsHref} className="text-primary text-xs font-medium hover:underline">
            View metrics →
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <SeriesLegend keys={keys} labels={labels} colors={colors} hidden={hidden} onToggle={toggle} />
        <TrafficChart
          title="Throughput in · last hour"
          testId="networking-plugin-nlb-overview-throughput"
          rows={rows}
          keys={keys}
          labels={labels}
          colors={colors}
          hidden={hidden}
          start={start}
          end={end}
          isLoading={throughput.isLoading}
          error={throughput.error}
          onZoom={openZoomed}
          onResetZoom={() => undefined}
        />
        <Text as="p" size="xs" textColor="muted">
          Drag across the chart to open that window in Metrics
        </Text>
      </CardContent>
    </Card>
  );
}

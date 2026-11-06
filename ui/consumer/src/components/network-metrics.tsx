import { MetricsToolbar } from './metrics-toolbar';
import { MetricKpiCard } from './metric-kpi-card';
import { NetworkMetricsSkeleton, TrafficLegendSkeleton } from './skeletons';
import { ErrorOrRestrictedState, SectionEmpty } from './states';
import { SeriesLegend, TrafficChart } from './traffic-chart';
import { useNetworkInterfaces } from '../lib/api';
import {
  INTERFACE_LABEL,
  MAX_CHART_SERIES,
  OTHER_SERIES,
  chartKeys,
  filterInterfaces,
  formatBitsPerSecond,
  interfaceSeriesLabels,
  interfaceTrafficQuery,
  naturalCompare,
  nextHiddenSeries,
  rankSeriesKeys,
  rollUpSeries,
  seriesColors,
  toGridRows,
  totalSeries,
  trafficSummary,
  type LegendModifiers,
} from '../lib/interface-metrics';
import { isMetricsDenied, useMetricsRange, type RangeResult } from '../lib/metrics';
import { RANGE_PRESETS, useMetricsControls, useRangeZoom } from '../lib/metrics-controls';
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@datum-cloud/datum-ui/card';
import { Text } from '@datum-cloud/datum-ui/typography';
import { useCallback, useEffect, useMemo, useState } from 'react';

const SYNC_ID = 'networking-plugin-vpc-traffic';
const INTERFACES_REFETCH_MS = 60_000;
const EMPTY_SERIES: RangeResult['series'] = [];

function formatDuration(ms: number): string {
  const minutes = Math.round(ms / 60_000);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  return hours < 48 ? `${hours}h` : `${Math.round(hours / 24)}d`;
}

function rangeLabelFor(range: { start: number; end: number; preset?: string }): string {
  const preset = RANGE_PRESETS.find((p) => p.key === range.preset);
  if (preset) return preset.label;
  return formatDuration(range.end - range.start);
}

function useHiddenSeries(keys: readonly string[]) {
  const [hidden, setHidden] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    setHidden((prev) => {
      const next = new Set([...prev].filter((k) => keys.includes(k)));
      return next.size === prev.size ? prev : next;
    });
  }, [keys]);
  const toggle = useCallback(
    (key: string, modifiers: LegendModifiers) => setHidden((prev) => nextHiddenSeries(keys, prev, key, modifiers)),
    [keys]
  );
  return { hidden, toggle };
}

export function NetworkMetrics({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string;
}) {
  const controls = useMetricsControls();
  const interfaces = useNetworkInterfaces(projectId, networkName, { refetchInterval: INTERFACES_REFETCH_MS });
  const items = useMemo(() => interfaces.data ?? [], [interfaces.data]);
  const filter = useMemo(
    () => filterInterfaces(items, controls.regions, controls.instances),
    [items, controls.regions, controls.instances]
  );
  const labels = useMemo(() => interfaceSeriesLabels(items), [items]);

  const queryOptions = { ...controls.window, groupLabel: INTERFACE_LABEL };
  const stepSeconds = controls.window.stepSeconds;
  const receiveQuery = projectId
    ? interfaceTrafficQuery(projectId, filter.matching, 'receive', stepSeconds)
    : undefined;
  const transmitQuery = projectId
    ? interfaceTrafficQuery(projectId, filter.matching, 'transmit', stepSeconds)
    : undefined;
  const receive = useMetricsRange(projectId, receiveQuery, queryOptions);
  const transmit = useMetricsRange(projectId, transmitQuery, queryOptions);
  const receiveSeries = receive.data?.series ?? EMPTY_SERIES;
  const transmitSeries = transmit.data?.series ?? EMPTY_SERIES;

  const seriesLimit = filter.pickedInstances.length > 0 ? undefined : MAX_CHART_SERIES;
  const { keys, reporting } = useMemo(() => {
    const ranked = rankSeriesKeys([receiveSeries, transmitSeries]);
    const sorted = chartKeys(ranked, seriesLimit).sort((a, b) =>
      a === OTHER_SERIES ? 1 : b === OTHER_SERIES ? -1 : naturalCompare(labels[a] ?? a, labels[b] ?? b)
    );
    return { keys: sorted, reporting: ranked.length };
  }, [receiveSeries, transmitSeries, seriesLimit, labels]);
  const colors = useMemo(() => seriesColors(items, labels), [items, labels]);
  const { hidden, toggle } = useHiddenSeries(keys);

  const grid = receive.data ?? transmit.data;
  const start = grid?.start ?? controls.resolved.start;
  const end = grid?.end ?? controls.resolved.end;
  const receiveRows = useMemo(
    () =>
      receive.data
        ? toGridRows(rollUpSeries(receiveSeries, keys), keys, receive.data.start, receive.data.end, receive.data.stepMs)
        : [],
    [receive.data, receiveSeries, keys]
  );
  const transmitRows = useMemo(
    () =>
      transmit.data
        ? toGridRows(rollUpSeries(transmitSeries, keys), keys, transmit.data.start, transmit.data.end, transmit.data.stepMs)
        : [],
    [transmit.data, transmitSeries, keys]
  );
  const receiveSummary = useMemo(() => trafficSummary(receiveSeries), [receiveSeries]);
  const transmitSummary = useMemo(() => trafficSummary(transmitSeries), [transmitSeries]);
  const receiveTotals = useMemo(() => totalSeries(receiveSeries), [receiveSeries]);
  const transmitTotals = useMemo(() => totalSeries(transmitSeries), [transmitSeries]);

  const { zoom: onZoom, reset: onResetZoom } = useRangeZoom(controls.range, controls.setRange, {
    start,
    end,
    stepMs: stepSeconds * 1000,
  });

  const regionOptions = useMemo(() => filter.regions.map((r) => ({ value: r, label: r })), [filter.regions]);
  const instanceOptions = useMemo(
    () =>
      filter.instanceOptions
        .map((i) => ({ value: i.name, label: labels[i.name] ?? i.name }))
        .sort((a, b) => naturalCompare(a.label, b.label)),
    [filter.instanceOptions, labels]
  );

  if (interfaces.isLoading) return <NetworkMetricsSkeleton />;

  if (interfaces.error) {
    return (
      <ErrorOrRestrictedState
        error={interfaces.error}
        restrictedMessage="You don't have permission to view this network's instances."
        onRetry={() => void interfaces.refetch()}
      />
    );
  }

  if (items.length === 0) {
    return (
      <SectionEmpty
        testId="networking-plugin-metrics-empty"
        title="No instances on this network yet"
        description="Traffic shows here once a workload instance attaches to this network."
      />
    );
  }

  const denied = isMetricsDenied(receive.error) || isMetricsDenied(transmit.error);
  const loading = receive.isLoading || transmit.isLoading;
  const kpi = (value: number | undefined) => (value === undefined ? undefined : formatBitsPerSecond(value));
  const kpis = [
    { title: 'Traffic in', value: receiveSummary.latest, points: receiveTotals, color: 'var(--color-chart-1)' },
    { title: 'Traffic out', value: transmitSummary.latest, points: transmitTotals, color: 'var(--color-chart-2)' },
    { title: 'Peak in', value: receiveSummary.peak, points: receiveTotals, color: 'var(--color-chart-1)' },
    { title: 'Peak out', value: transmitSummary.peak, points: transmitTotals, color: 'var(--color-chart-2)' },
  ];
  const rangeLabel = rangeLabelFor(controls.resolved);
  const scope =
    filter.matching.length === items.length
      ? `${items.length} ${items.length === 1 ? 'instance' : 'instances'}`
      : `${filter.matching.length} of ${items.length} instances`;
  const omitted = reporting - keys.filter((k) => k !== OTHER_SERIES).length;
  const expectedSeries = seriesLimit ? Math.min(filter.matching.length, seriesLimit) : filter.matching.length;
  const expectOther = !!seriesLimit && filter.matching.length > seriesLimit;

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-network-metrics">
      <MetricsToolbar
        controls={controls}
        regionOptions={regionOptions}
        instanceOptions={instanceOptions}
        pickedRegions={filter.pickedRegions}
        pickedInstances={filter.pickedInstances}
      />

      <div className="grid grid-cols-2 gap-6 lg:grid-cols-4" data-testid="networking-plugin-metrics-kpis">
        {kpis.map((card) => (
          <MetricKpiCard
            key={card.title}
            title={card.title}
            rangeLabel={rangeLabel}
            value={kpi(card.value)}
            points={card.points}
            color={card.color}
            isLoading={loading}
            denied={denied}
          />
        ))}
      </div>

      <Card size="sm" sectioned className="w-full overflow-hidden" data-testid="networking-plugin-traffic">
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
          {filter.matching.length === 0 ? (
            <Text as="p" size="sm" textColor="muted">
              No instances match these filters.
            </Text>
          ) : (
            <>
              {loading && keys.length === 0 ? (
                <TrafficLegendSkeleton count={expectedSeries - (expectOther ? 1 : 0)} other={expectOther} />
              ) : (
                <SeriesLegend
                  keys={keys}
                  labels={labels}
                  colors={colors}
                  hidden={hidden}
                  onToggle={toggle}
                  testId="networking-plugin-traffic-legend"
                />
              )}
              <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
                <TrafficChart
                  title="Traffic in"
                  testId="networking-plugin-traffic-in"
                  rows={receiveRows}
                  keys={keys}
                  labels={labels}
                  colors={colors}
                  hidden={hidden}
                  start={start}
                  end={end}
                  isLoading={receive.isLoading}
                  error={receive.error}
                  syncId={SYNC_ID}
                  onZoom={onZoom}
                  onResetZoom={onResetZoom}
                />
                <TrafficChart
                  title="Traffic out"
                  testId="networking-plugin-traffic-out"
                  rows={transmitRows}
                  keys={keys}
                  labels={labels}
                  colors={colors}
                  hidden={hidden}
                  start={start}
                  end={end}
                  isLoading={transmit.isLoading}
                  error={transmit.error}
                  syncId={SYNC_ID}
                  onZoom={onZoom}
                  onResetZoom={onResetZoom}
                />
              </div>
              <Text as="p" size="xs" textColor="muted">
                Drag across a chart to zoom in · double-click to zoom back out
              </Text>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

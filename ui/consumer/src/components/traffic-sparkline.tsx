import {
  INTERFACE_LABEL,
  formatBitsPerSecond,
  interfaceTrafficQuery,
} from '../lib/interface-metrics';
import { isMetricsDenied, useMetricsRange, type MetricSeries } from '../lib/metrics';
import { INSTANCES_PARAM, resolveRange, useNow } from '../lib/metrics-controls';
import type { NetworkInterface } from '../schema';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { Text } from '@datum-cloud/datum-ui/typography';
import { useMemo } from 'react';
import { Link } from 'react-router';

export const SPARK_RANGE = 'now-30m';
const SPARK_STEP_SECONDS = 60;
const SPARK_REFRESH_MS = 60_000;
const WIDTH = 96;
const HEIGHT = 24;

export interface InterfaceSpark {
  receive: Array<[number, number]>;
  transmit: Array<[number, number]>;
}

export interface InterfaceSparks {
  byInterface: Map<string, InterfaceSpark>;
  isLoading: boolean;
  denied: boolean;
  failed: boolean;
}

function byKey(series: readonly MetricSeries[] | undefined): Map<string, Array<[number, number]>> {
  return new Map((series ?? []).map((s) => [s.key, s.points]));
}

export function useInterfaceSparks(
  projectId: string | undefined,
  interfaces: readonly NetworkInterface[]
): InterfaceSparks {
  const now = useNow(SPARK_REFRESH_MS);
  const window = resolveRange(SPARK_RANGE, now);
  const options = { start: window.start, end: window.end, stepSeconds: SPARK_STEP_SECONDS, groupLabel: INTERFACE_LABEL };
  const receive = useMetricsRange(
    projectId,
    projectId ? interfaceTrafficQuery(projectId, interfaces, 'receive', SPARK_STEP_SECONDS) : undefined,
    options
  );
  const transmit = useMetricsRange(
    projectId,
    projectId ? interfaceTrafficQuery(projectId, interfaces, 'transmit', SPARK_STEP_SECONDS) : undefined,
    options
  );
  const byInterface = useMemo(() => {
    const rx = byKey(receive.data?.series);
    const tx = byKey(transmit.data?.series);
    const map = new Map<string, InterfaceSpark>();
    for (const key of new Set([...rx.keys(), ...tx.keys()])) {
      map.set(key, { receive: rx.get(key) ?? [], transmit: tx.get(key) ?? [] });
    }
    return map;
  }, [receive.data, transmit.data]);
  return {
    byInterface,
    isLoading: receive.isLoading || transmit.isLoading,
    denied: isMetricsDenied(receive.error) || isMetricsDenied(transmit.error),
    failed: !!receive.error || !!transmit.error,
  };
}

export function sparkPath(
  points: ReadonlyArray<[number, number]>,
  bounds: { start: number; end: number; max: number },
  width = WIDTH,
  height = HEIGHT
): string {
  if (points.length === 0) return '';
  const span = Math.max(1, bounds.end - bounds.start);
  const max = bounds.max > 0 ? bounds.max : 1;
  return points
    .map(([ts, v], i) => {
      const x = ((ts - bounds.start) / span) * width;
      const y = height - 1 - (Math.max(0, v) / max) * (height - 2);
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(' ');
}

const CELL_HEIGHT = 37;

function SparkBox({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-flex items-center" style={{ height: CELL_HEIGHT }}>
      {children}
    </span>
  );
}

export function TrafficSparkSkeleton() {
  return (
    <SparkBox>
      <span className="inline-flex items-center gap-3" aria-label="Loading traffic">
        <Skeleton className="shrink-0" style={{ width: WIDTH, height: HEIGHT }} />
        <span className="flex flex-col gap-1">
          <Skeleton className="h-3 w-20" />
          <Skeleton className="h-3 w-16" />
        </span>
      </span>
    </SparkBox>
  );
}

function latest(points: ReadonlyArray<[number, number]>): number | undefined {
  return points.at(-1)?.[1];
}

export function TrafficSparkCell({
  iface,
  sparks,
  metricsHref,
}: {
  iface: NetworkInterface;
  sparks: InterfaceSparks;
  metricsHref?: string;
}) {
  const spark = sparks.byInterface.get(iface.name);

  if (sparks.isLoading && !spark) return <TrafficSparkSkeleton />;
  if (!spark || (spark.receive.length === 0 && spark.transmit.length === 0)) {
    return (
      <SparkBox>
        <Text size="xs" textColor="muted" title={sparks.denied ? "You don't have permission to view metrics" : undefined}>
          —
        </Text>
      </SparkBox>
    );
  }

  const all = [...spark.receive, ...spark.transmit];
  const bounds = {
    start: Math.min(...all.map(([ts]) => ts)),
    end: Math.max(...all.map(([ts]) => ts)),
    max: Math.max(...all.map(([, v]) => v)),
  };
  const rx = latest(spark.receive);
  const tx = latest(spark.transmit);
  const content = (
    <span className="inline-flex items-center gap-3" data-testid="network-interface-traffic">
      <svg width={WIDTH} height={HEIGHT} viewBox={`0 0 ${WIDTH} ${HEIGHT}`} aria-hidden className="shrink-0 overflow-visible">
        <path d={sparkPath(spark.transmit, bounds)} fill="none" stroke="var(--color-chart-2)" strokeWidth={1.25} />
        <path d={sparkPath(spark.receive, bounds)} fill="none" stroke="var(--color-chart-1)" strokeWidth={1.25} />
      </svg>
      <span className="flex flex-col font-mono text-xs leading-tight tabular-nums">
        <span>
          <span style={{ color: 'var(--color-chart-1)' }}>↓</span> {rx === undefined ? '—' : formatBitsPerSecond(rx)}
        </span>
        <span className="text-muted-foreground">
          <span style={{ color: 'var(--color-chart-2)' }}>↑</span> {tx === undefined ? '—' : formatBitsPerSecond(tx)}
        </span>
      </span>
    </span>
  );

  if (!metricsHref) return <SparkBox>{content}</SparkBox>;
  const href = `${metricsHref}?${new URLSearchParams({ [INSTANCES_PARAM]: iface.name }).toString()}`;
  return (
    <SparkBox>
      <Link to={href} className="hover:bg-muted/60 -mx-1 inline-flex rounded px-1 py-0.5" title="Open in Metrics">
        {content}
      </Link>
    </SparkBox>
  );
}

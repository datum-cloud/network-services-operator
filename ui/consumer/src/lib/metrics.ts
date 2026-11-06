import { PLUGIN_ID, getProjectScopedBase } from './api';
import { useQuery, keepPreviousData, type UseQueryResult } from '@tanstack/react-query';

export const METRICS_API_PATH = '/apis/o11y.miloapis.com/v1alpha1/metrics/api/v1';

export class MetricsError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'MetricsError';
    this.status = status;
  }
}

export interface MetricSeries {
  key: string;
  points: Array<[timestampMs: number, value: number]>;
}

export interface RangeResult {
  start: number;
  end: number;
  stepMs: number;
  series: MetricSeries[];
}

interface RawMatrix {
  resultType?: string;
  result?: Array<{ metric?: Record<string, string>; values?: Array<[number, string]> }>;
}

interface PrometheusEnvelope {
  status?: 'success' | 'error';
  data?: RawMatrix;
  error?: string;
}

async function errorText(res: Response): Promise<string | undefined> {
  try {
    const body = (await res.json()) as { error?: unknown; message?: unknown };
    if (typeof body.error === 'string') return body.error;
    if (typeof body.message === 'string') return body.message;
  } catch {
    return undefined;
  }
  return undefined;
}

export function toSeries(matrix: RawMatrix | undefined, groupLabel: string): MetricSeries[] {
  return (matrix?.result ?? []).map((row) => ({
    key: row.metric?.[groupLabel] ?? '',
    points: (row.values ?? []).flatMap(([ts, value]) => {
      const n = Number.parseFloat(value);
      return Number.isFinite(n) ? [[ts * 1000, n] as [number, number]] : [];
    }),
  }));
}

export interface QueryWindow {
  start: number;
  end: number;
  stepSeconds: number;
}

export async function queryRange(
  projectId: string,
  query: string,
  window: QueryWindow,
  groupLabel: string
): Promise<RangeResult> {
  const { stepSeconds } = window;
  const end = Math.floor(window.end / 1000);
  const start = Math.floor(window.start / 1000 / stepSeconds) * stepSeconds;
  const res = await fetch(`${getProjectScopedBase(projectId)}${METRICS_API_PATH}/query_range`, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      query,
      start: String(start),
      end: String(end),
      step: `${stepSeconds}s`,
    }).toString(),
  });
  if (!res.ok) {
    throw new MetricsError((await errorText(res)) ?? `Metrics request failed (${res.status})`, res.status);
  }
  const body = (await res.json()) as PrometheusEnvelope;
  if (body.status === 'error') throw new MetricsError(body.error || 'Metrics query failed', 400);
  return { start: start * 1000, end: end * 1000, stepMs: stepSeconds * 1000, series: toSeries(body.data, groupLabel) };
}

const NO_RETRY_STATUSES = new Set([400, 401, 403, 422, 503, 504]);

export function shouldRetryMetrics(failureCount: number, error: unknown): boolean {
  if (error instanceof MetricsError && NO_RETRY_STATUSES.has(error.status)) return false;
  return failureCount < 2;
}

export function isMetricsDenied(error: unknown): boolean {
  return error instanceof MetricsError && (error.status === 401 || error.status === 403);
}

export function metricsErrorMessage(error: unknown): string {
  if (isMetricsDenied(error)) return "You don't have permission to view metrics";
  const status = error instanceof MetricsError ? error.status : undefined;
  if (status === 422) return 'Too much data to show. Try a shorter time range';
  if (status === 503 || status === 504) return 'Took too long to load. Try a shorter time range';
  return 'Unable to load metrics';
}

export const METRICS_QUERY_KEY = 'metrics-range';

export function useMetricsRange(
  projectId: string | undefined,
  query: string | undefined,
  options: QueryWindow & { groupLabel: string; refetchInterval?: number | false }
): UseQueryResult<RangeResult, MetricsError> {
  const { start, end, stepSeconds, groupLabel, refetchInterval = false } = options;
  return useQuery<RangeResult, MetricsError>({
    queryKey: [PLUGIN_ID, METRICS_QUERY_KEY, projectId, query, start, end, stepSeconds, groupLabel],
    enabled: !!projectId && !!query,
    queryFn: () => queryRange(projectId as string, query as string, { start, end, stepSeconds }, groupLabel),
    refetchInterval,
    staleTime: 15_000,
    retry: shouldRetryMetrics,
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[2] === projectId ? keepPreviousData(previous) : undefined,
  });
}

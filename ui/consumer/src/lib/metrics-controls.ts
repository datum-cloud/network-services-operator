import type { PickerPreset } from '@datum-cloud/datum-ui/picker';
import { resolveBrush } from './interface-metrics';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router';

export const RANGE_PARAM = 'timeRange';
export const STEP_PARAM = 'step';
export const REFRESH_PARAM = 'refresh';
export const REGIONS_PARAM = 'regions';
export const INSTANCES_PARAM = 'instances';

export const DEFAULT_RANGE = 'now-1h';
export const DEFAULT_STEP = 'auto';
export const DEFAULT_REFRESH = 'off';

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const UNIT_MS: Record<string, number> = { s: SECOND, m: MINUTE, h: HOUR, d: DAY };

export function parseDurationMs(raw: string): number | undefined {
  const match = raw.match(/^(\d+)([smhd])$/);
  if (!match) return undefined;
  return Number(match[1]) * UNIT_MS[match[2]];
}

const PRESETS = [
  { key: 'now-5m', label: 'Last 5m', shortcut: '1' },
  { key: 'now-15m', label: 'Last 15m', shortcut: '2' },
  { key: 'now-30m', label: 'Last 30m', shortcut: '3' },
  { key: 'now-1h', label: 'Last 1h', shortcut: 'h' },
  { key: 'now-3h', label: 'Last 3h', shortcut: '4' },
  { key: 'now-6h', label: 'Last 6h', shortcut: '5' },
  { key: 'now-12h', label: 'Last 12h', shortcut: '6' },
  { key: 'now-24h', label: 'Last 24h', shortcut: 'd' },
  { key: 'now-2d', label: 'Last 2d', shortcut: '7' },
  { key: 'now-7d', label: 'Last 7d', shortcut: 'w' },
] as const;

export const RANGE_PRESETS: readonly PickerPreset[] = PRESETS.map((preset) => ({
  ...preset,
  getRange: () => {
    const to = new Date();
    return { from: new Date(to.getTime() - (parseDurationMs(preset.key.slice(4)) ?? HOUR)), to };
  },
}));

const PRESET_KEYS = new Set<string>(PRESETS.map((p) => p.key));

export const STEP_OPTIONS = [
  { value: 'auto', label: 'Auto' },
  { value: '15s', label: '15s' },
  { value: '1m', label: '1m' },
  { value: '5m', label: '5m' },
  { value: '10m', label: '10m' },
  { value: '15m', label: '15m' },
  { value: '30m', label: '30m' },
  { value: '1h', label: '1h' },
] as const;

export const REFRESH_OPTIONS = [
  { value: 'off', label: 'Off' },
  { value: '10s', label: '10s' },
  { value: '30s', label: '30s' },
  { value: '1m', label: '1m' },
  { value: '5m', label: '5m' },
] as const;

export interface ResolvedRange {
  start: number;
  end: number;
  preset?: string;
}

export function isRelativeRange(raw: string): boolean {
  return raw.startsWith('now-');
}

export function resolveRange(raw: string | null | undefined, now = Date.now()): ResolvedRange {
  const value = raw || DEFAULT_RANGE;
  if (isRelativeRange(value)) {
    const duration = parseDurationMs(value.slice(4));
    if (duration) return { start: now - duration, end: now, preset: PRESET_KEYS.has(value) ? value : undefined };
  }
  const absolute = value.match(/^(\d+)_(\d+)$/);
  if (absolute) {
    const start = Number(absolute[1]) * SECOND;
    const end = Number(absolute[2]) * SECOND;
    if (end > start) return { start, end };
  }
  return { start: now - HOUR, end: now, preset: DEFAULT_RANGE };
}

export function serializeAbsoluteRange(start: number, end: number): string {
  return `${Math.floor(start / SECOND)}_${Math.ceil(end / SECOND)}`;
}

export function serializePickerValue(value: { from: string; to: string; preset?: string } | null): string {
  if (!value) return DEFAULT_RANGE;
  if (value.preset && PRESET_KEYS.has(value.preset)) return value.preset;
  const start = new Date(value.from).getTime();
  const end = new Date(value.to).getTime();
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return DEFAULT_RANGE;
  return serializeAbsoluteRange(start, end);
}

const AUTO_STEPS_SECONDS = [15, 60, 300, 600, 900, 1800, 3600];
const AUTO_MIN_POINTS = 16;

export function autoStepSeconds(rangeMs: number): number {
  for (let i = AUTO_STEPS_SECONDS.length - 1; i >= 0; i -= 1) {
    if (rangeMs / (AUTO_STEPS_SECONDS[i] * SECOND) >= AUTO_MIN_POINTS) return AUTO_STEPS_SECONDS[i];
  }
  return AUTO_STEPS_SECONDS[0];
}

const MAX_POINTS = 11_000;

export function stepSecondsFor(step: string | null | undefined, rangeMs: number): number {
  const chosen = step && step !== 'auto' ? parseDurationMs(step) : undefined;
  const seconds = chosen ? chosen / SECOND : autoStepSeconds(rangeMs);
  return Math.max(seconds, Math.ceil(rangeMs / SECOND / MAX_POINTS));
}

export function refreshIntervalMs(refresh: string | null | undefined): number | false {
  if (!refresh || refresh === 'off') return false;
  return parseDurationMs(refresh) ?? false;
}

export function parseListParam(raw: string | null): string[] {
  if (!raw) return [];
  return [...new Set(raw.split(',').map((v) => v.trim()).filter(Boolean))];
}

export function useNow(intervalMs: number | false): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!intervalMs) return;
    const timer = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(timer);
  }, [intervalMs]);
  return now;
}

export function useMetricsControls() {
  const [searchParams, setSearchParams] = useSearchParams();
  const range = searchParams.get(RANGE_PARAM) || DEFAULT_RANGE;
  const step = searchParams.get(STEP_PARAM) || DEFAULT_STEP;
  const refresh = searchParams.get(REFRESH_PARAM) || DEFAULT_REFRESH;
  const regionsRaw = searchParams.get(REGIONS_PARAM);
  const instancesRaw = searchParams.get(INSTANCES_PARAM);
  const regions = useMemo(() => parseListParam(regionsRaw), [regionsRaw]);
  const instances = useMemo(() => parseListParam(instancesRaw), [instancesRaw]);

  const relative = isRelativeRange(range);
  const [now, setNow] = useState(() => Date.now());
  const interval = relative ? refreshIntervalMs(refresh) : false;
  useEffect(() => {
    if (!interval) return;
    const timer = window.setInterval(() => setNow(Date.now()), interval);
    return () => window.clearInterval(timer);
  }, [interval]);

  const resolved = useMemo(() => resolveRange(range, now), [range, now]);
  const stepSeconds = stepSecondsFor(step, resolved.end - resolved.start);
  const queryWindow = useMemo(
    () => ({ start: resolved.start, end: resolved.end, stepSeconds }),
    [resolved, stepSeconds]
  );

  const setParam = useCallback(
    (key: string, value: string | undefined, fallback?: string) => {
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          if (!value || value === fallback) next.delete(key);
          else next.set(key, value);
          return next;
        },
        { replace: true }
      );
    },
    [setSearchParams]
  );

  return {
    range,
    resolved,
    window: queryWindow,
    step,
    refresh,
    regions,
    instances,
    advance: useCallback(() => setNow(Date.now()), []),
    setRange: useCallback(
      (value: string) => {
        setNow(Date.now());
        setParam(RANGE_PARAM, value, DEFAULT_RANGE);
      },
      [setParam]
    ),
    setStep: useCallback((value: string) => setParam(STEP_PARAM, value, DEFAULT_STEP), [setParam]),
    setRefresh: useCallback((value: string) => setParam(REFRESH_PARAM, value, DEFAULT_REFRESH), [setParam]),
    setRegions: useCallback((values: string[]) => setParam(REGIONS_PARAM, values.join(',')), [setParam]),
    setInstances: useCallback((values: string[]) => setParam(INSTANCES_PARAM, values.join(',')), [setParam]),
  };
}

export type MetricsControls = ReturnType<typeof useMetricsControls>;

export function useRangeZoom(
  range: string,
  setRange: (value: string) => void,
  bounds: { start: number; end: number; stepMs: number }
) {
  const origin = useRef<string | null>(null);
  useEffect(() => {
    if (isRelativeRange(range)) origin.current = null;
  }, [range]);
  const { start, end, stepMs } = bounds;
  const zoom = useCallback(
    (from: number, to: number) => {
      const next = resolveBrush(from, to, { start, end, stepMs });
      if (!next) return;
      if (!origin.current && isRelativeRange(range)) origin.current = range;
      setRange(serializeAbsoluteRange(next.start, next.end));
    },
    [start, end, stepMs, range, setRange]
  );
  const reset = useCallback(() => {
    if (origin.current) setRange(origin.current);
  }, [setRange]);
  return { zoom, reset };
}

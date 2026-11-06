import {
  autoStepSeconds,
  refreshIntervalMs,
  resolveRange,
  serializeAbsoluteRange,
  serializePickerValue,
  stepSecondsFor,
  useMetricsControls,
  useRangeZoom,
} from './metrics-controls';
import { act, renderHook } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

const NOW = 1_800_000_000_000;
const HOUR = 3_600_000;

describe('resolveRange', () => {
  it('resolves a relative preset against now', () => {
    expect(resolveRange('now-6h', NOW)).toEqual({ start: NOW - 6 * HOUR, end: NOW, preset: 'now-6h' });
  });

  it('reads an absolute range in seconds', () => {
    expect(resolveRange('100_200', NOW)).toEqual({ start: 100_000, end: 200_000 });
  });

  it('falls back to the last hour for anything else', () => {
    expect(resolveRange('nonsense', NOW)).toEqual({ start: NOW - HOUR, end: NOW, preset: 'now-1h' });
    expect(resolveRange('200_100', NOW).preset).toBe('now-1h');
    expect(resolveRange(null, NOW).preset).toBe('now-1h');
  });
});

describe('serializing', () => {
  it('keeps presets relative and other picks absolute', () => {
    expect(serializePickerValue({ from: '', to: '', preset: 'now-24h' })).toBe('now-24h');
    expect(
      serializePickerValue({ from: new Date(100_000).toISOString(), to: new Date(200_000).toISOString() })
    ).toBe('100_200');
    expect(serializePickerValue(null)).toBe('now-1h');
    expect(serializeAbsoluteRange(100_400, 199_600)).toBe('100_200');
  });
});

describe('steps', () => {
  it('picks the coarsest step that still gives enough points', () => {
    expect(autoStepSeconds(5 * 60_000)).toBe(15);
    expect(autoStepSeconds(HOUR)).toBe(60);
    expect(autoStepSeconds(7 * 24 * HOUR)).toBe(3600);
  });

  it('honours an explicit step but never exceeds the point limit', () => {
    expect(stepSecondsFor('5m', HOUR)).toBe(300);
    expect(stepSecondsFor('15s', 7 * 24 * HOUR)).toBe(55);
  });

  it('turns refresh options into intervals', () => {
    expect(refreshIntervalMs('off')).toBe(false);
    expect(refreshIntervalMs('30s')).toBe(30_000);
  });
});

describe('useMetricsControls', () => {
  function wrapper(search: string) {
    return ({ children }: { children: React.ReactNode }) => (
      <MemoryRouter initialEntries={[`/metrics${search}`]}>{children}</MemoryRouter>
    );
  }

  afterEach(() => {
    vi.useRealTimers();
  });

  it('keeps a relative window fixed while auto refresh is off', () => {
    vi.useFakeTimers({ now: NOW });
    const { result } = renderHook(() => useMetricsControls(), { wrapper: wrapper('') });
    const first = result.current.window;

    act(() => vi.advanceTimersByTime(5 * 60_000));

    expect(result.current.window).toBe(first);
    expect(first).toEqual({ start: NOW - HOUR, end: NOW, stepSeconds: 60 });
  });

  it('slides a relative window on each auto refresh and on a manual refresh', () => {
    vi.useFakeTimers({ now: NOW });
    const { result } = renderHook(() => useMetricsControls(), { wrapper: wrapper('?refresh=30s') });

    act(() => vi.advanceTimersByTime(30_000));
    expect(result.current.window.end).toBe(NOW + 30_000);

    act(() => vi.setSystemTime(NOW + 45_000));
    act(() => result.current.advance());
    expect(result.current.window.end).toBe(NOW + 45_000);
  });

  it('starts a newly picked range at the current time', () => {
    vi.useFakeTimers({ now: NOW });
    const { result } = renderHook(() => useMetricsControls(), { wrapper: wrapper('') });

    act(() => vi.setSystemTime(NOW + 10 * 60_000));
    act(() => result.current.setRange('now-6h'));

    expect(result.current.range).toBe('now-6h');
    expect(result.current.window.end).toBe(NOW + 10 * 60_000);
  });
});

describe('useRangeZoom', () => {
  const bounds = { start: 0, end: 600_000, stepMs: 60_000 };

  it('zooms to an absolute range and zooms back out to the original preset', () => {
    const setRange = vi.fn();
    const { result, rerender } = renderHook(({ range }) => useRangeZoom(range, setRange, bounds), {
      initialProps: { range: 'now-1h' },
    });

    act(() => result.current.zoom(240_000, 60_000));
    expect(setRange).toHaveBeenLastCalledWith('60_300');

    rerender({ range: '60_300' });
    act(() => result.current.zoom(120_000, 180_000));
    rerender({ range: '120_240' });
    act(() => result.current.reset());
    expect(setRange).toHaveBeenLastCalledWith('now-1h');
  });

  it('does nothing on a click without a drag or a reset without a zoom', () => {
    const setRange = vi.fn();
    const { result } = renderHook(() => useRangeZoom('now-1h', setRange, bounds));

    act(() => result.current.zoom(60_000, 60_000));
    act(() => result.current.reset());

    expect(setRange).not.toHaveBeenCalled();
  });
});

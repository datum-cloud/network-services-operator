import {
  formatBitsPerSecond,
  formatBitsTick,
  niceScale,
  rowsMax,
  timeTicks,
  type ChartRow,
  type LegendModifiers,
} from '../lib/interface-metrics';
import { TrafficChartSkeleton } from './skeletons';
import { metricsErrorMessage } from '../lib/metrics';
import { ChartContainer, ChartTooltip, type ChartConfig } from '@datum-cloud/datum-ui/chart';
import { cn } from '@datum-cloud/datum-ui/utils';
import { format } from 'date-fns';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { CartesianGrid, Line, LineChart, ReferenceArea, XAxis, YAxis } from 'recharts';
import type { MouseHandlerDataParam } from 'recharts/types/synchronisation/types';

const CHART_HEIGHT = 240;
const TOOLTIP_ROWS = 10;
const SIX_HOURS_MS = 6 * 60 * 60 * 1000;
const TWO_DAYS_MS = 48 * 60 * 60 * 1000;

export function formatTimeTick(timestamp: number, rangeMs: number): string {
  if (rangeMs < SIX_HOURS_MS) return format(timestamp, 'h:mm a');
  if (rangeMs < TWO_DAYS_MS) return format(timestamp, 'MMM d, h:mm a');
  return format(timestamp, 'MMM d');
}

function Placeholder({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="text-muted-foreground flex items-center justify-center text-xs"
      style={{ height: CHART_HEIGHT }}>
      {children}
    </div>
  );
}

export function SeriesLegend({
  keys,
  labels,
  colors,
  hidden,
  onToggle,
  testId,
}: {
  keys: readonly string[];
  labels: Record<string, string>;
  colors: Record<string, string>;
  hidden: ReadonlySet<string>;
  onToggle: (key: string, modifiers: LegendModifiers) => void;
  testId?: string;
}) {
  if (keys.length < 2) return null;
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1" data-testid={testId}>
      {keys.map((key) => {
        const isHidden = hidden.has(key);
        return (
          <button
            key={key}
            type="button"
            title="Click to isolate · Shift-click to hide"
            onMouseDown={(event) => {
              if (event.shiftKey) event.preventDefault();
            }}
            onClick={(event) => {
              event.preventDefault();
              onToggle(key, event);
            }}
            className={cn(
              'inline-flex items-center gap-1.5 text-xs select-none',
              isHidden ? 'text-muted-foreground/50 line-through' : 'text-foreground hover:text-foreground/80'
            )}>
            <span
              className={cn('size-2 shrink-0 rounded-full', isHidden && 'opacity-40')}
              style={{ backgroundColor: colors[key] }}
            />
            {labels[key] ?? key}
          </button>
        );
      })}
    </div>
  );
}

function timestampOf(state: MouseHandlerDataParam | undefined, rows: readonly ChartRow[]): number | undefined {
  const label = Number(state?.activeLabel);
  if (Number.isFinite(label)) return label;
  const index = Number(state?.activeTooltipIndex);
  return Number.isInteger(index) ? rows[index]?.timestamp : undefined;
}

export function TrafficChart({
  title,
  testId,
  rows,
  keys,
  labels,
  colors,
  hidden,
  start,
  end,
  isLoading,
  error,
  syncId,
  onZoom,
  onResetZoom,
}: {
  title: string;
  testId: string;
  rows: ChartRow[];
  keys: readonly string[];
  labels: Record<string, string>;
  colors: Record<string, string>;
  hidden: ReadonlySet<string>;
  start: number;
  end: number;
  isLoading: boolean;
  error: unknown;
  syncId?: string;
  onZoom: (start: number, end: number) => void;
  onResetZoom: () => void;
}) {
  const [brush, setBrush] = useState<{ from: number; to: number } | null>(null);
  const brushRef = useRef<{ from: number; to: number } | null>(null);
  const rangeMs = Math.max(1, end - start);

  const visibleKeys = useMemo(() => {
    const visible = keys.filter((k) => !hidden.has(k));
    return visible.length > 0 ? visible : [...keys];
  }, [keys, hidden]);
  const scale = useMemo(() => niceScale(rowsMax(rows, visibleKeys)), [rows, visibleKeys]);
  const xTicks = useMemo(() => timeTicks(start, end), [start, end]);
  const yWidth = useMemo(
    () => Math.min(84, Math.max(48, Math.max(...scale.ticks.map((t) => formatBitsTick(t).length)) * 7 + 14)),
    [scale]
  );
  const config: ChartConfig = useMemo(
    () => Object.fromEntries(keys.map((k) => [k, { label: labels[k] ?? k, color: colors[k] }])),
    [keys, labels, colors]
  );
  const hasData = useMemo(() => rows.some((row) => keys.some((k) => row[k] !== null)), [rows, keys]);

  const finish = useCallback(() => {
    const current = brushRef.current;
    brushRef.current = null;
    setBrush(null);
    if (!current || current.from === current.to) return;
    onZoom(Math.min(current.from, current.to), Math.max(current.from, current.to));
  }, [onZoom]);

  useEffect(() => {
    if (!brush) return;
    window.addEventListener('mouseup', finish);
    return () => window.removeEventListener('mouseup', finish);
  }, [brush, finish]);

  let body: React.ReactNode;
  if (isLoading && rows.length === 0) body = <TrafficChartSkeleton />;
  else if (error) body = <Placeholder>{metricsErrorMessage(error)}</Placeholder>;
  else if (!hasData) body = <Placeholder>No traffic recorded in this window</Placeholder>;
  else {
    body = (
      <ChartContainer
        config={config}
        className={cn(
          'h-full w-full overflow-visible',
          brush ? 'cursor-col-resize select-none' : '[&_.recharts-surface]:cursor-crosshair'
        )}
        style={{ height: CHART_HEIGHT, aspectRatio: 'auto' }}>
        <LineChart
          data={rows}
          syncId={syncId}
          syncMethod="value"
          margin={{ top: 8, right: 16, left: 0, bottom: 4 }}
          onMouseDown={(state) => {
            const ts = timestampOf(state, rows);
            if (ts === undefined) return;
            brushRef.current = { from: ts, to: ts };
            setBrush(brushRef.current);
          }}
          onMouseMove={(state) => {
            if (!brushRef.current) return;
            const ts = timestampOf(state, rows);
            if (ts === undefined) return;
            brushRef.current = { from: brushRef.current.from, to: ts };
            setBrush(brushRef.current);
          }}
          onDoubleClick={onResetZoom}>
          <CartesianGrid strokeDasharray="3 3" vertical={false} />
          <XAxis
            dataKey="timestamp"
            type="number"
            scale="time"
            domain={[start, end]}
            ticks={xTicks}
            allowDataOverflow
            tickFormatter={(value: number) => formatTimeTick(value, rangeMs)}
            tickLine={false}
            axisLine={false}
            tick={{ fill: 'var(--muted-foreground)', fontSize: 11 }}
          />
          <YAxis
            domain={scale.domain}
            ticks={scale.ticks}
            tickFormatter={formatBitsTick}
            tickLine={false}
            axisLine={false}
            tickMargin={4}
            width={yWidth}
            tick={{ fill: 'var(--muted-foreground)', fontSize: 11 }}
          />
          {!brush && (
            <ChartTooltip
              isAnimationActive={false}
              cursor={{ stroke: 'var(--border)' }}
              content={({ active, payload }) => {
                if (!active || !payload?.length) return null;
                const ts = Number(payload[0]?.payload?.timestamp);
                const items = payload
                  .filter((p) => typeof p.value === 'number')
                  .sort((a, b) => Number(b.value) - Number(a.value));
                if (items.length === 0) return null;
                return (
                  <div className="border-border/50 bg-background min-w-52 rounded-lg border px-3.5 py-2.5 text-xs shadow-xl">
                    <div className="text-muted-foreground mb-2">{format(ts, 'MMM d, h:mm:ss a')}</div>
                    <div className="flex flex-col gap-1.5">
                      {items.slice(0, TOOLTIP_ROWS).map((p) => (
                        <div key={String(p.dataKey)} className="flex items-center justify-between gap-6">
                          <span className="flex min-w-0 items-center gap-2">
                            <span
                              className="size-2.5 shrink-0 rounded-[2px]"
                              style={{ backgroundColor: colors[String(p.dataKey)] }}
                            />
                            <span className="text-muted-foreground truncate">
                              {labels[String(p.dataKey)] ?? String(p.dataKey)}
                            </span>
                          </span>
                          <span className="text-foreground shrink-0 font-mono font-medium tabular-nums">
                            {formatBitsPerSecond(Number(p.value))}
                          </span>
                        </div>
                      ))}
                      {items.length > TOOLTIP_ROWS && (
                        <div className="text-muted-foreground">+{items.length - TOOLTIP_ROWS} more</div>
                      )}
                    </div>
                  </div>
                );
              }}
            />
          )}
          {brush && brush.from !== brush.to && (
            <ReferenceArea
              x1={Math.min(brush.from, brush.to)}
              x2={Math.max(brush.from, brush.to)}
              fill="var(--primary)"
              fillOpacity={0.1}
              stroke="var(--primary)"
              strokeOpacity={0.35}
              ifOverflow="visible"
            />
          )}
          {keys.map((key) => (
            <Line
              key={key}
              type="monotone"
              dataKey={key}
              name={labels[key] ?? key}
              stroke={colors[key]}
              strokeWidth={1.5}
              dot={false}
              hide={hidden.has(key) && visibleKeys.length < keys.length}
              connectNulls={false}
              isAnimationActive={false}
            />
          ))}
        </LineChart>
      </ChartContainer>
    );
  }

  return (
    <div className="flex min-w-0 flex-col gap-2" data-testid={testId}>
      <div className="flex h-5 items-center text-sm font-medium">{title}</div>
      {body}
    </div>
  );
}

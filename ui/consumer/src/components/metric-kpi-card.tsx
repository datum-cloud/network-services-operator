import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { SpinnerIcon } from '@datum-cloud/datum-ui/icons';
import { Tooltip } from '@datum-cloud/datum-ui/tooltip';
import { Text } from '@datum-cloud/datum-ui/typography';
import { cn } from '@datum-cloud/datum-ui/utils';
import { useId, useMemo } from 'react';
import { Area, AreaChart, YAxis } from 'recharts';

export function MetricKpiCard({
  title,
  rangeLabel,
  value,
  points,
  color = 'var(--primary)',
  isLoading = false,
  denied = false,
  testId,
}: {
  title: string;
  rangeLabel: string;
  value: string | undefined;
  points: ReadonlyArray<[number, number]>;
  color?: string;
  isLoading?: boolean;
  denied?: boolean;
  testId?: string;
}) {
  const gradientId = `kpi-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`;
  const rows = useMemo(() => points.map(([timestamp, v]) => ({ timestamp, value: v })), [points]);
  const yDomain = useMemo<[number, number] | undefined>(() => {
    if (rows.length === 0) return undefined;
    const values = rows.map((r) => r.value);
    const min = Math.min(...values);
    const max = Math.max(...values);
    const pad = min === max ? Math.max(Math.abs(min) * 0.25, 1) : (max - min) * 0.1;
    return [min - pad, max + pad];
  }, [rows]);

  return (
    <Card size="sm" className="relative h-full w-full overflow-hidden" data-testid={testId}>
      {isLoading && (
        <div className="absolute inset-0 z-10 flex items-center justify-center">
          <SpinnerIcon size="sm" />
        </div>
      )}
      <CardContent className={cn('flex min-w-0 flex-col gap-2', isLoading && 'invisible')}>
        <div className="flex h-4 items-center justify-between gap-2">
          <Text size="xs" weight="medium" textColor="muted">
            {title}
          </Text>
          <Text size="5xs" textColor="muted">
            {rangeLabel}
          </Text>
        </div>
        <Text as="div" size="2xl" weight="semibold" textColor="default" className="flex h-8 items-center tabular-nums">
          {denied ? (
            <Tooltip message="You don't have permission to view metrics">
              <Text textColor="muted">&mdash;</Text>
            </Tooltip>
          ) : (
            (value ?? '—')
          )}
        </Text>
        <div className="h-8 w-full">
          {!denied && rows.length >= 2 && (
            <AreaChart
              data={rows}
              responsive
              width="100%"
              height={32}
              margin={{ top: 2, right: 0, left: 0, bottom: 2 }}>
              <defs>
                <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={color} stopOpacity={0.3} />
                  <stop offset="100%" stopColor={color} stopOpacity={0} />
                </linearGradient>
              </defs>
              {yDomain && <YAxis hide domain={yDomain} />}
              <Area
                type="monotone"
                dataKey="value"
                stroke={color}
                strokeWidth={1.5}
                fill={`url(#${gradientId})`}
                fillOpacity={1}
                dot={false}
                activeDot={false}
                isAnimationActive={false}
              />
            </AreaChart>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

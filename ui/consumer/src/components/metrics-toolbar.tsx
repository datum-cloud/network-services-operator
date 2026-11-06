import { METRICS_QUERY_KEY } from '../lib/metrics';
import {
  RANGE_PRESETS,
  REFRESH_OPTIONS,
  STEP_OPTIONS,
  isRelativeRange,
  serializePickerValue,
  type MetricsControls,
} from '../lib/metrics-controls';
import { PLUGIN_ID } from '../lib/api';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { MultiSelect } from '@datum-cloud/datum-ui/multi-select';
import { DateTimeRangePicker, getBrowserTimezone } from '@datum-cloud/datum-ui/picker';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@datum-cloud/datum-ui/select';
import { Tooltip } from '@datum-cloud/datum-ui/tooltip';
import { cn } from '@datum-cloud/datum-ui/utils';
import { useQueryClient } from '@tanstack/react-query';
import { RefreshCwIcon } from 'lucide-react';
import { useMemo, useState } from 'react';

const CONTROL_HEIGHT = 'h-8 min-h-8';

export interface FilterOption {
  value: string;
  label: string;
}

function RefreshControl({ controls }: { controls: MetricsControls }) {
  const queryClient = useQueryClient();
  const [spinning, setSpinning] = useState(false);
  const auto = controls.refresh !== 'off';

  const refresh = () => {
    if (isRelativeRange(controls.range)) {
      controls.advance();
      return;
    }
    setSpinning(true);
    void queryClient
      .invalidateQueries({ queryKey: [PLUGIN_ID, METRICS_QUERY_KEY] })
      .finally(() => setSpinning(false));
  };

  return (
    <div
      className={cn('border-input bg-card flex shrink-0 items-stretch overflow-hidden rounded-lg border', CONTROL_HEIGHT)}>
      <Tooltip message={auto ? `Refreshing every ${controls.refresh}` : 'Refresh metrics'}>
        <button
          type="button"
          aria-label="Refresh metrics"
          data-testid="networking-plugin-metrics-refresh"
          className="hover:bg-muted inline-flex h-full w-9 shrink-0 items-center justify-center border-r disabled:opacity-50"
          onClick={refresh}
          disabled={spinning}>
          <Icon icon={RefreshCwIcon} className={cn('size-4', (spinning || auto) && 'animate-spin')} />
        </button>
      </Tooltip>
      <Select value={controls.refresh} onValueChange={controls.setRefresh}>
        <SelectTrigger
          aria-label="Auto refresh"
          className="h-full min-h-0 w-auto min-w-16 rounded-l-none border-0 bg-transparent px-2 py-0 shadow-none focus:ring-0">
          <SelectValue placeholder="Auto refresh" />
        </SelectTrigger>
        <SelectContent>
          {REFRESH_OPTIONS.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}

export function MetricsToolbar({
  controls,
  regionOptions,
  instanceOptions,
  pickedRegions,
  pickedInstances,
}: {
  controls: MetricsControls;
  regionOptions: FilterOption[];
  instanceOptions: FilterOption[];
  pickedRegions: string[];
  pickedInstances: string[];
}) {
  const timezone = useMemo(() => getBrowserTimezone(), []);
  const pickerValue = useMemo(
    () => ({
      from: new Date(controls.resolved.start).toISOString(),
      to: new Date(controls.resolved.end).toISOString(),
      ...(controls.resolved.preset ? { preset: controls.resolved.preset } : {}),
    }),
    [controls.resolved]
  );

  return (
    <div
      className="flex w-full flex-col gap-3 lg:flex-row lg:items-center lg:justify-between"
      data-testid="networking-plugin-metrics-toolbar">
      <div className="flex w-full flex-col gap-2 sm:flex-row sm:items-center lg:w-auto">
        {regionOptions.length > 1 && (
          <MultiSelect
            className={cn('bg-card w-full sm:w-auto sm:min-w-44', CONTROL_HEIGHT)}
            options={regionOptions}
            value={pickedRegions}
            onValueChange={controls.setRegions}
            placeholder="All regions"
            maxCount={1}
            emptyContent="No regions found."
          />
        )}
        {instanceOptions.length > 1 && (
          <MultiSelect
            className={cn('bg-card w-full sm:w-auto sm:min-w-64', CONTROL_HEIGHT)}
            options={instanceOptions}
            value={pickedInstances}
            onValueChange={controls.setInstances}
            placeholder="All instances"
            maxCount={1}
            emptyContent="No instances found."
          />
        )}
      </div>
      <div className="flex w-full min-w-0 flex-col gap-2 sm:flex-row sm:items-center lg:w-auto lg:justify-end">
        <DateTimeRangePicker
          value={pickerValue}
          onChange={(value) =>
            controls.setRange(
              serializePickerValue(
                value
                  ? {
                      from: value.from,
                      to: value.to,
                      ...('preset' in value && value.preset ? { preset: String(value.preset) } : {}),
                    }
                  : null
              )
            )
          }
          timezone={timezone}
          presets={RANGE_PRESETS}
          disableFuture
          placeholder="Select time range"
          className="bg-card w-full sm:w-auto"
          triggerClassName={cn(CONTROL_HEIGHT, 'py-0 text-sm')}
        />
        <div className="flex shrink-0 items-center gap-2">
          <div
            className={cn('border-input bg-card flex shrink-0 items-stretch overflow-hidden rounded-lg border', CONTROL_HEIGHT)}>
            <span className="text-foreground inline-flex items-center border-r px-3 text-xs font-medium">Step</span>
            <Select value={controls.step} onValueChange={controls.setStep}>
              <SelectTrigger
                aria-label="Step"
                className="h-full min-h-0 w-auto min-w-16 rounded-l-none border-0 bg-transparent px-2 py-0 shadow-none focus:ring-0">
                <SelectValue placeholder="Step" />
              </SelectTrigger>
              <SelectContent>
                {STEP_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <RefreshControl controls={controls} />
        </div>
      </div>
    </div>
  );
}

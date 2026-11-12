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
import { Button } from '@datum-cloud/datum-ui/button';
import { ButtonGroup, ButtonGroupText } from '@datum-cloud/datum-ui/button-group';
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
const GROUP_CLASS = cn('w-full sm:w-fit', CONTROL_HEIGHT);
const GROUP_TRIGGER_CLASS = cn('bg-card flex-1 sm:w-auto sm:flex-initial', CONTROL_HEIGHT);

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
    <ButtonGroup className={GROUP_CLASS}>
      <Tooltip message={auto ? `Refreshing every ${controls.refresh}` : 'Refresh metrics'}>
        <Button
          type="quaternary"
          theme="outline"
          size="icon"
          aria-label="Refresh metrics"
          data-testid="networking-plugin-metrics-refresh"
          className="border-input-border bg-card text-muted-foreground dark:border-input-border dark:text-muted-foreground h-auto w-9 shrink-0 rounded-r-none"
          onClick={refresh}
          disabled={spinning}>
          <Icon icon={RefreshCwIcon} className={cn('size-4', (spinning || auto) && 'animate-spin')} />
        </Button>
      </Tooltip>
      <Select value={controls.refresh} onValueChange={controls.setRefresh}>
        <SelectTrigger aria-label="Auto refresh" className={GROUP_TRIGGER_CLASS}>
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
    </ButtonGroup>
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
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
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
      <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center">
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
          <ButtonGroup className={GROUP_CLASS}>
            <ButtonGroupText>Step</ButtonGroupText>
            <Select value={controls.step} onValueChange={controls.setStep}>
              <SelectTrigger aria-label="Step" className={GROUP_TRIGGER_CLASS}>
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
          </ButtonGroup>
          <RefreshControl controls={controls} />
        </div>
      </div>
    </div>
  );
}

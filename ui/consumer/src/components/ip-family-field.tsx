import { IPV4_COPY, type Ipv4Availability, ipv4LocationsWarning } from '../lib/ipv4-quota';
import { Button } from '@datum-cloud/datum-ui/button';
import { Checkbox } from '@datum-cloud/datum-ui/checkbox';
import { useField } from '@datum-cloud/datum-ui/form';
import { Label } from '@datum-cloud/datum-ui/label';
import { useEffect, useId, useRef } from 'react';

export const IP_FAMILY_COPY = {
  label: 'Address families',
  ipv6: 'IPv6',
  ipv6Hint: 'Always on',
  description: 'Each location the load balancer serves gets one address of each family.',
} as const;

export function isChecked(value: unknown): boolean {
  return value === true || value === 'on' || value === 'true';
}

export function IpFamilyField({
  availability,
  onRequest,
  locations,
  hasIpv4 = false,
  resetKey = 0,
}: {
  availability: Ipv4Availability;
  onRequest: () => void;
  locations?: number;
  hasIpv4?: boolean;
  resetKey?: number;
}) {
  const ipv6Id = useId();
  const ipv4Id = useId();
  const { control } = useField('ipv4');
  const checked = isChecked(control.value);
  const enabled = hasIpv4 || availability.canChoose;

  useEffect(() => {
    if (!enabled && checked) control.change(false);
  }, [enabled, checked, control]);

  const appliedReset = useRef(resetKey);
  useEffect(() => {
    if (appliedReset.current === resetKey) return;
    appliedReset.current = resetKey;
    control.change(hasIpv4);
  }, [resetKey, hasIpv4, control]);

  const explainDisabled = !enabled && availability.state !== 'loading';
  const adding = checked && !hasIpv4;
  const short =
    adding &&
    availability.available !== undefined &&
    locations !== undefined &&
    locations > availability.available;

  return (
    <div className="flex flex-col gap-2" role="group" aria-label={IP_FAMILY_COPY.label}>
      <p className="text-sm font-medium">{IP_FAMILY_COPY.label}</p>
      <p className="text-muted-foreground text-xs">{IP_FAMILY_COPY.description}</p>
      <div className="flex items-center space-x-2">
        <Checkbox id={ipv6Id} checked disabled aria-label={IP_FAMILY_COPY.ipv6} />
        <Label htmlFor={ipv6Id} className="text-sm font-normal">
          {IP_FAMILY_COPY.ipv6}
          <span className="text-muted-foreground ml-2 text-xs">{IP_FAMILY_COPY.ipv6Hint}</span>
        </Label>
      </div>
      <div className="flex items-center space-x-2">
        <Checkbox
          id={ipv4Id}
          checked={checked}
          disabled={!enabled}
          onCheckedChange={(value) => control.change(value === true)}
        />
        <Label
          htmlFor={ipv4Id}
          className={`text-sm font-normal ${enabled ? 'cursor-pointer' : 'cursor-not-allowed opacity-70'}`}>
          {IPV4_COPY.optionLabel}
        </Label>
      </div>
      {availability.state === 'loading' && !hasIpv4 && (
        <p className="text-muted-foreground text-xs">{IPV4_COPY.checking}</p>
      )}
      {explainDisabled && (
        <Ipv4Note
          lines={[availability.disabledReason ?? IPV4_COPY.noAllocation, IPV4_COPY.paidAddOn]}
          onRequest={onRequest}
        />
      )}
      {short && (
        <Ipv4Note
          tone="warning"
          lines={[ipv4LocationsWarning(locations, availability.available ?? 0)]}
          onRequest={onRequest}
        />
      )}
    </div>
  );
}

export function Ipv4Note({
  lines,
  onRequest,
  tone = 'muted',
}: {
  lines: string[];
  onRequest: () => void;
  tone?: 'muted' | 'warning' | 'danger';
}) {
  const color =
    tone === 'warning'
      ? 'text-amber-700 dark:text-amber-400'
      : tone === 'danger'
        ? 'text-destructive'
        : 'text-muted-foreground';
  return (
    <div className={`flex flex-col items-start gap-1 text-xs ${color}`}>
      {lines.map((line) => (
        <p key={line}>{line}</p>
      ))}
      <Button type="primary" theme="link" size="link" htmlType="button" onClick={onRequest}>
        {IPV4_COPY.requestAction}
      </Button>
    </div>
  );
}

import type { CreateNetworkInput } from '../lib/api';
import { Form } from '@datum-cloud/datum-ui/form';
import type { ReactNode } from 'react';
import { z } from 'zod';

// Shared by CreateNetworkDialog and NetworkHomeHero so the two create paths
// validate and post the same thing.

export const DEFAULT_MTU = 1440;

export const DEFAULT_NETWORK_NAME = 'default';

export const NETWORK_NAME_MESSAGE =
  'Lowercase letters, numbers, and hyphens only. Must start and end with a letter or number.';

export const networkFormSchema = z.object({
  name: z
    .string({ error: 'Name is required.' })
    .max(63, 'At most 63 characters.')
    .regex(/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/, NETWORK_NAME_MESSAGE),
  mtu: z
    .number({ error: 'Enter a whole number.' })
    .int('Enter a whole number.')
    .min(1300, 'Must be between 1300 and 8856.')
    .max(8856, 'Must be between 1300 and 8856.')
    .optional(),
});

export type NetworkFormValues = z.infer<typeof networkFormSchema>;

// New networks are IPv6-only; a blank MTU leaves the server default in place.
export function toCreateNetworkInput(values: NetworkFormValues): CreateNetworkInput {
  return {
    name: values.name,
    ipFamilies: ['IPv6'],
    mtu: values.mtu,
  };
}

export function NetworkNameField({
  label = 'Name',
  placeholder = 'e.g. my-network',
  autoFocus,
  disabled,
  className,
  trailing,
}: {
  label?: string;
  placeholder?: string;
  autoFocus?: boolean;
  disabled?: boolean;
  className?: string;
  // Rendered beside the input, e.g. an inline submit button. Errors still
  // sit under the whole row.
  trailing?: ReactNode;
}) {
  const input = <Form.Input autoFocus={autoFocus} placeholder={placeholder} />;
  return (
    <Form.Field name="name" label={label} required disabled={disabled} className={className}>
      {trailing ? (
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
          {input}
          {trailing}
        </div>
      ) : (
        input
      )}
    </Form.Field>
  );
}

export function NetworkMtuField({ disabled, className }: { disabled?: boolean; className?: string }) {
  return (
    <Form.Field
      name="mtu"
      label="MTU"
      tooltip="Maximum transmission unit. Traffic between regions is encapsulated, so a larger MTU can stall connections on some paths."
      description={`Between 1300 and 8856. Defaults to ${DEFAULT_MTU}.`}
      disabled={disabled}
      className={className}>
      <Form.Input type="number" inputMode="numeric" placeholder={String(DEFAULT_MTU)} />
    </Form.Field>
  );
}

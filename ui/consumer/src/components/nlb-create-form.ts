import type { ApiError, NlbCreateRequests } from '../lib/api';
import type { NetworkService, NetworkServicePort } from '../schema';
import { z } from 'zod';

const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
const NAME_MESSAGE =
  'Lowercase letters, numbers, and hyphens, starting and ending with a letter or number.';

// Radix Select can't hold an empty value, so "probe the serving port" needs a
// value of its own. It never reaches the API: the health check just omits port.
export const SERVING_PORT = '__serving';

export const nlbCreateSchema = z
  .object({
    name: z
      .string({ error: 'Name is required.' })
      .max(63, 'At most 63 characters.')
      .regex(NAME_PATTERN, NAME_MESSAGE),
    network: z.string({ error: 'Select a network.' }),
    protocol: z.enum(['TCP', 'UDP']),
    port: z
      .number({ error: 'Enter a port number.' })
      .int('Enter a whole number.')
      .min(1, 'Must be between 1 and 65535.')
      .max(65535, 'Must be between 1 and 65535.'),
    backend: z.string({ error: 'Select a backend.' }),
    backendPort: z.string({ error: 'Select one of the backend’s ports.' }),
    healthPort: z.string().optional(),
  })
  .superRefine((values, ctx) => {
    // The route controller refuses a UDP backend without a TCP probe port, but
    // only in the route's status after it saves, so catch it here instead.
    if (values.protocol === 'UDP' && (!values.healthPort || values.healthPort === SERVING_PORT)) {
      ctx.addIssue({
        code: 'custom',
        path: ['healthPort'],
        message: 'A UDP listener needs a TCP port on the backend to health check.',
      });
    }
  });

export type NlbCreateValues = z.infer<typeof nlbCreateSchema>;
export type NlbCreateField = keyof NlbCreateValues;

export function portProtocol(port: NetworkServicePort): 'TCP' | 'UDP' {
  return port.protocol === 'UDP' ? 'UDP' : 'TCP';
}

/** The backend's ports a listener on `protocol` can forward to. */
export function backendPortOptions(
  service: NetworkService | undefined,
  protocol: 'TCP' | 'UDP' | undefined
): NetworkServicePort[] {
  return (service?.ports ?? []).filter((p) => !protocol || portProtocol(p) === protocol);
}

/** Health checks only probe TCP, so only the backend's TCP ports can be probed. */
export function healthPortOptions(service: NetworkService | undefined): NetworkServicePort[] {
  return (service?.ports ?? []).filter((p) => portProtocol(p) === 'TCP');
}

/** The listener, its address request and its route are all named after the protocol and port. */
export function nlbListenerName(values: Pick<NlbCreateValues, 'protocol' | 'port'>): string {
  return `${values.protocol.toLowerCase()}-${values.port}`;
}

export function toNlbCreateRequests(values: NlbCreateValues): NlbCreateRequests {
  const listener = nlbListenerName(values);
  const healthCheck =
    values.healthPort && values.healthPort !== SERVING_PORT
      ? { protocol: 'TCP', port: values.healthPort }
      : { protocol: 'TCP' };

  return {
    nlb: {
      apiVersion: 'networking.datumapis.com/v1alpha',
      kind: 'NetworkLoadBalancer',
      metadata: { name: values.name, namespace: 'default' },
      spec: {
        networkRef: { name: values.network },
        // No locations: the address follows wherever the backend runs.
        exposure: { addresses: [{ name: listener, families: ['IPv6'] }] },
        listeners: [
          { name: listener, protocol: values.protocol, port: values.port, addressRefs: [listener] },
        ],
      },
    },
    route: {
      apiVersion: 'networking.datumapis.com/v1alpha',
      kind: 'NetworkLoadBalancerRoute',
      metadata: { name: `${values.name}-${listener}`, namespace: 'default' },
      spec: {
        parentRefs: [{ name: values.name, listener }],
        backends: [
          { networkService: { name: values.backend, port: values.backendPort }, healthCheck },
        ],
      },
    },
  };
}

export interface NlbCreateErrors {
  fields: Partial<Record<NlbCreateField, string>>;
  general?: string;
}

const NLB_FIELD_PREFIXES: [string, NlbCreateField][] = [
  ['metadata.name', 'name'],
  ['spec.networkRef', 'network'],
  ['spec.listeners[0].protocol', 'protocol'],
  ['spec.listeners[0].port', 'port'],
];

const ROUTE_FIELD_PREFIXES: [string, NlbCreateField][] = [
  ['spec.backends[0].networkService.name', 'backend'],
  ['spec.backends[0].networkService.port', 'backendPort'],
  ['spec.backends[0].healthCheck', 'healthPort'],
];

/**
 * Puts each of the API's field causes next to the form field it's about.
 * Anything that doesn't match a field, including fields the dialog fills in
 * itself, shows as one message for the whole form.
 */
export function toNlbCreateErrors(error: ApiError, step: 'nlb' | 'route'): NlbCreateErrors {
  if (step === 'nlb' && error.status === 409) {
    return { fields: { name: 'A load balancer with this name already exists.' } };
  }

  const prefixes = step === 'nlb' ? NLB_FIELD_PREFIXES : ROUTE_FIELD_PREFIXES;
  const fields: NlbCreateErrors['fields'] = {};
  const unmatched: string[] = [];
  for (const cause of error.causes) {
    const match = prefixes.find(([prefix]) => cause.field?.startsWith(prefix));
    const message = cause.message ?? error.message;
    if (match && !fields[match[1]]) fields[match[1]] = message;
    else if (!match) unmatched.push(message);
  }

  const general =
    unmatched.length > 0
      ? unmatched.join(' ')
      : Object.keys(fields).length === 0
        ? error.message
        : undefined;
  return { fields, general };
}

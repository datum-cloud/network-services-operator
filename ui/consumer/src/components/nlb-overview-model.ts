import { isIpv4QuotaRefusal } from '../lib/ipv4-quota';
import type {
  BadgeType,
  NetworkLoadBalancer,
  NetworkLoadBalancerAddressStatus,
  NetworkLoadBalancerEndpoint,
  NetworkLoadBalancerListener,
  NetworkLoadBalancerRoute,
  NetworkService,
} from '../schema';

export interface ListenerStatus {
  label: string;
  type: BadgeType;
  pending?: boolean;
  /** The API's own words, for a tooltip. */
  detail?: string;
}

export interface ListenerRow {
  listener: NetworkLoadBalancerListener;
  /** The first allocated address, and how many locations have one. */
  address?: string;
  /** The hostname for that same location's address, once one is generated. */
  hostname?: string;
  locations: number;
  ipv6Address?: string;
  ipv4Address?: string;
  ipv4Requested?: boolean;
  ipv4?: Ipv4RowState;
  route?: NetworkLoadBalancerRoute;
  backend?: NetworkLoadBalancerRoute['backends'][number];
  service?: NetworkService;
  status: ListenerStatus;
}

const ROUTE_REASON_COPY: Record<string, string> = {
  BackendClaimConflict: 'Backend in use',
  BackendNotFound: 'Backend not found',
  HealthCheckInvalid: 'Health check invalid',
  InvalidSelector: 'Invalid backend',
  ListenerNotFound: 'Listener not found',
  NetworkMismatch: 'Wrong network',
  NoEligibleEndpoints: 'No reachable backends',
  ParentNotFound: 'Load balancer not found',
  PortNotFound: 'Port not found',
  ProtocolMismatch: 'Protocol mismatch',
  UnsupportedCapability: 'Not supported',
};

/** `SomeNewReason` → `Some new reason`, for reasons the copy above doesn't know yet. */
function humanize(reason: string): string {
  const words = reason.replace(/([a-z0-9])([A-Z])/g, '$1 $2').toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

function routeFor(
  nlbName: string,
  listener: NetworkLoadBalancerListener,
  routes: NetworkLoadBalancerRoute[]
) {
  // The controller writes the listener each parent resolved to, which also
  // covers routes that picked the listener by port. When several routes
  // attach, only the oldest accepted one is effective.
  const reported = routes.flatMap((route) =>
    route.parentStatuses
      .filter((p) => p.name === nlbName && p.listener === listener.name)
      .map((parent) => ({ route, parent }))
  );
  const chosen = reported.find((r) => r.parent.effective) ?? reported[0];
  if (chosen) return chosen;

  // Not reported yet: fall back to what the route asked for.
  const route = routes.find((r) =>
    r.parents.some(
      (p) =>
        p.name === nlbName && (p.listener ? p.listener === listener.name : p.port === listener.port)
    )
  );
  return route ? { route, parent: undefined } : undefined;
}

function listenerStatus(
  hasRoute: boolean,
  parent: NetworkLoadBalancerRoute['parentStatuses'][number] | undefined,
  address: string | undefined,
  service: NetworkService | undefined
): ListenerStatus {
  if (!hasRoute) {
    return {
      label: 'No route',
      type: 'warning',
      detail: 'No route sends this listener’s traffic anywhere.',
    };
  }
  // A new or changed listener waits on each location's data plane. That's
  // progress, not a fault, whichever status the controller reports it with.
  if (parent?.reason === 'AwaitingDataPlane') {
    return { label: 'Programming', type: 'muted', pending: true, detail: parent.message };
  }
  if (parent?.status === 'False') {
    return {
      label:
        (parent.reason && ROUTE_REASON_COPY[parent.reason]) ??
        (parent.reason && humanize(parent.reason)) ??
        'Not attached',
      type: 'danger',
      detail: parent.message,
    };
  }
  if (!parent || parent.status !== 'True')
    return { label: 'Attaching', type: 'muted', pending: true };
  if (!parent.effective) {
    return { label: 'Superseded', type: 'warning', detail: 'An older route serves this listener.' };
  }
  if (!address) return { label: 'Address pending', type: 'muted', pending: true };
  if (service && service.summary.healthy === 0) return { label: 'Unhealthy', type: 'danger' };
  if (service && service.summary.healthy < service.summary.members) {
    return { label: 'Degraded', type: 'warning' };
  }
  return { label: 'Serving', type: 'success' };
}

export type Ipv4RowState = 'none' | 'pending' | 'allocated' | 'refused' | 'failed';

export interface Ipv4Failure {
  location: string;
  message?: string;
}

export interface Ipv4Issues {
  refused: string[];
  failed: Ipv4Failure[];
}

function addressStatuses(endpoint: NetworkLoadBalancerEndpoint): NetworkLoadBalancerAddressStatus[] {
  return endpoint.addressStatuses ?? endpoint.addresses.map((a) => ({ ...a, conditions: [] }));
}

function valueOf(endpoint: NetworkLoadBalancerEndpoint, family: string): string | undefined {
  return addressStatuses(endpoint).find((a) => a.family === family && a.value)?.value;
}

export function cleanConditionMessage(message: string | undefined): string | undefined {
  const cleaned = message?.replace(/^.*?\bis forbidden:\s*/, '').trim();
  return cleaned || undefined;
}

type Ipv4Outcome = { kind: 'allocated' | 'pending' | 'refused' } | { kind: 'failed'; message?: string };

function ipv4Outcome(address: NetworkLoadBalancerAddressStatus): Ipv4Outcome {
  if (address.value) return { kind: 'allocated' };
  if (isIpv4QuotaRefusal(address)) return { kind: 'refused' };
  const allocated = address.conditions.find((c) => c.type === 'Allocated');
  if (allocated?.status === 'False' && allocated.reason === 'AllocationFailed') {
    return { kind: 'failed', message: cleanConditionMessage(allocated.message) };
  }
  return { kind: 'pending' };
}

function ipv4Entries(endpoints: NetworkLoadBalancerEndpoint[]) {
  return endpoints.flatMap((endpoint) =>
    addressStatuses(endpoint)
      .filter((a) => a.family === 'IPv4')
      .map((a) => ({ location: endpoint.location, outcome: ipv4Outcome(a) }))
  );
}

/** Locations whose IPv4 address was refused on quota or failed for another reason. */
export function ipv4Issues(nlb: NetworkLoadBalancer): Ipv4Issues {
  const entries = ipv4Entries(nlb.endpoints);
  const refused = [
    ...new Set(entries.filter((e) => e.outcome.kind === 'refused').map((e) => e.location)),
  ].sort();
  const failed = new Map<string, Ipv4Failure>();
  for (const { location, outcome } of entries) {
    if (outcome.kind === 'failed' && !failed.has(location)) {
      failed.set(location, { location, message: outcome.message });
    }
  }
  return {
    refused,
    failed: [...failed.values()].sort((a, b) => a.location.localeCompare(b.location)),
  };
}

function rowIpv4State(requested: boolean, endpoints: NetworkLoadBalancerEndpoint[]): Ipv4RowState {
  const kinds = new Set(ipv4Entries(endpoints).map((e) => e.outcome.kind));
  if (kinds.has('refused')) return 'refused';
  if (kinds.has('failed')) return 'failed';
  if (kinds.has('allocated')) return 'allocated';
  return kinds.size > 0 || requested ? 'pending' : 'none';
}

/** The worst IPv4 state across an NLB's listeners, for the list. */
export function nlbIpv4State(nlb: NetworkLoadBalancer): Ipv4RowState {
  const requested = Object.values(nlb.addressFamilies ?? {}).some((f) => f.includes('IPv4'));
  return rowIpv4State(requested, nlb.endpoints);
}

export function toListenerRows(
  nlb: NetworkLoadBalancer,
  routes: NetworkLoadBalancerRoute[],
  services: NetworkService[]
): ListenerRow[] {
  return nlb.listeners.map((listener) => {
    const requestEndpoints = nlb.endpoints.filter((e) => e.requestName === listener.addressRef);
    const endpoints = requestEndpoints.filter((e) => e.addresses.length > 0);
    const primary = endpoints.find((e) => valueOf(e, 'IPv6')) ?? endpoints[0];
    const ipv6Address = primary && valueOf(primary, 'IPv6');
    const ipv4Source = primary && valueOf(primary, 'IPv4') ? primary : endpoints.find((e) => valueOf(e, 'IPv4'));
    const ipv4Address = ipv4Source && valueOf(ipv4Source, 'IPv4');
    const address = ipv6Address ?? primary?.addresses[0]?.value;
    const hostname = primary?.hostname;
    const ipv4Requested =
      (listener.addressRef !== undefined &&
        !!nlb.addressFamilies?.[listener.addressRef]?.includes('IPv4')) ||
      requestEndpoints.some((e) => addressStatuses(e).some((a) => a.family === 'IPv4'));
    const match = routeFor(nlb.name, listener, routes);
    const backend = match?.route.backends[0];
    const service = services.find((s) => s.name === backend?.name);

    return {
      listener,
      address,
      hostname,
      locations: endpoints.length,
      ipv6Address,
      ipv4Address,
      ipv4Requested,
      ipv4: rowIpv4State(ipv4Requested, requestEndpoints),
      route: match?.route,
      backend,
      service,
      status: listenerStatus(!!match, match?.parent, address, service),
    };
  });
}

export function overviewSummary(rows: ListenerRow[]): ListenerStatus {
  const serving = rows.filter((r) => r.status.type === 'success').length;
  if (rows.length > 0 && serving === rows.length) {
    return {
      label: `Forwarding on ${serving} ${serving === 1 ? 'listener' : 'listeners'}`,
      type: 'success',
    };
  }
  if (rows.some((r) => r.status.type === 'danger'))
    return { label: 'Needs attention', type: 'danger' };
  if (rows.some((r) => r.status.type === 'warning'))
    return { label: 'Needs attention', type: 'warning' };
  return { label: 'Starting up', type: 'muted', pending: true };
}

export interface BackendSummary {
  name: string;
  port: string;
  listeners: string[];
  service?: NetworkService;
}

export function backendSummaries(rows: ListenerRow[]): BackendSummary[] {
  const byName = new Map<string, BackendSummary>();
  for (const row of rows) {
    if (!row.backend) continue;
    const label = `${row.listener.protocol} ${row.listener.port}`;
    const seen = byName.get(row.backend.name);
    if (seen) seen.listeners.push(label);
    else {
      byName.set(row.backend.name, {
        name: row.backend.name,
        port: row.backend.port,
        listeners: [label],
        service: row.service,
      });
    }
  }
  return [...byName.values()];
}

export function backendHealth(service: NetworkService | undefined): {
  type: BadgeType;
  label: string;
} {
  if (!service) return { type: 'muted', label: 'Not found' };
  const { healthy, members } = service.summary;
  if (members === 0) return { type: 'muted', label: 'No endpoints' };
  const label = `${healthy}/${members} healthy`;
  if (healthy === members) return { type: 'success', label };
  return { type: healthy === 0 ? 'danger' : 'warning', label };
}

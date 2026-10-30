import type {
  BadgeType,
  NetworkLoadBalancer,
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
  if (parent?.status === 'False') {
    return {
      label: (parent.reason && ROUTE_REASON_COPY[parent.reason]) ?? parent.reason ?? 'Not attached',
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

export function toListenerRows(
  nlb: NetworkLoadBalancer,
  routes: NetworkLoadBalancerRoute[],
  services: NetworkService[]
): ListenerRow[] {
  return nlb.listeners.map((listener) => {
    const endpoints = nlb.endpoints.filter(
      (e) => e.requestName === listener.addressRef && e.addresses.length > 0
    );
    const address = endpoints[0]?.addresses[0]?.value;
    const hostname = endpoints[0]?.hostname;
    const match = routeFor(nlb.name, listener, routes);
    const backend = match?.route.backends[0];
    const service = services.find((s) => s.name === backend?.name);

    return {
      listener,
      address,
      hostname,
      locations: endpoints.length,
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

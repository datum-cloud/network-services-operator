import { SERVING_PORT, type NlbListenerValues } from './nlb-create-form';
import type {
  NetworkLoadBalancer,
  NetworkLoadBalancerListener,
  NetworkLoadBalancerRoute,
} from '../schema';

type Parent = NetworkLoadBalancerRoute['parents'][number];

/** Whether a route's parentRef points at this listener, by name or by port. */
function refersTo(parent: Parent, nlbName: string, listener: NetworkLoadBalancerListener): boolean {
  if (parent.name !== nlbName) return false;
  return parent.listener ? parent.listener === listener.name : parent.port === listener.port;
}

/** Routes with at least one parentRef on this listener. */
export function routesForListener(
  nlbName: string,
  listener: NetworkLoadBalancerListener,
  routes: NetworkLoadBalancerRoute[]
): NetworkLoadBalancerRoute[] {
  return routes.filter((r) => r.parents.some((p) => refersTo(p, nlbName, listener)));
}

/** Whether every parentRef of the route is this listener, so changing the route changes nothing else. */
export function routeOnlyServes(
  route: NetworkLoadBalancerRoute,
  nlbName: string,
  listener: NetworkLoadBalancerListener
): boolean {
  return route.parents.every((p) => refersTo(p, nlbName, listener));
}

function listenerIndex(nlb: NetworkLoadBalancer, listener: NetworkLoadBalancerListener): number {
  return listener.index ?? nlb.listeners.findIndex((l) => l.name === listener.name);
}

function versionTest(nlb: NetworkLoadBalancer): object[] {
  return nlb.resourceVersion
    ? [{ op: 'test', path: '/metadata/resourceVersion', value: nlb.resourceVersion }]
    : [];
}

export interface DeleteListenerPlan {
  /** JSON Patch for the NLB. */
  patch: object[];
  deleteRoutes: NetworkLoadBalancerRoute[];
  detachRoutes: { route: NetworkLoadBalancerRoute; parents: Parent[] }[];
  /** The address released with the listener, unless another listener still uses it. */
  releasedAddress?: string;
}

/**
 * Removes the listener and, when no other listener uses it, its address. The
 * test ops make the patch fail rather than remove the wrong entry if the
 * lists moved since the page read them.
 */
export function planDeleteListener(
  nlb: NetworkLoadBalancer,
  listener: NetworkLoadBalancerListener,
  routes: NetworkLoadBalancerRoute[]
): DeleteListenerPlan {
  const index = listenerIndex(nlb, listener);
  const patch: object[] = [
    ...versionTest(nlb),
    { op: 'test', path: `/spec/listeners/${index}/name`, value: listener.name },
    { op: 'remove', path: `/spec/listeners/${index}` },
  ];

  const address = listener.addressRef;
  const shared = nlb.listeners.some((l) => l.name !== listener.name && l.addressRef === address);
  const addressIndex = address ? (nlb.addressNames ?? []).indexOf(address) : -1;
  const releasedAddress = address && !shared && addressIndex >= 0 ? address : undefined;
  if (releasedAddress) {
    patch.push(
      { op: 'test', path: `/spec/exposure/addresses/${addressIndex}/name`, value: releasedAddress },
      { op: 'remove', path: `/spec/exposure/addresses/${addressIndex}` }
    );
  }

  const attached = routesForListener(nlb.name, listener, routes);
  return {
    patch,
    deleteRoutes: attached.filter((r) => routeOnlyServes(r, nlb.name, listener)),
    detachRoutes: attached
      .filter((r) => !routeOnlyServes(r, nlb.name, listener))
      .map((route) => ({
        route,
        parents: route.parents.filter((p) => !refersTo(p, nlb.name, listener)),
      })),
    releasedAddress,
  };
}

/**
 * Changes the listener's protocol and port in place, so it keeps its address.
 * Its name stays as it was: routes refer to it by name.
 */
export function toEditListenerPatch(
  nlb: NetworkLoadBalancer,
  listener: NetworkLoadBalancerListener,
  values: Pick<NlbListenerValues, 'protocol' | 'port'>
): object[] {
  if (values.protocol === listener.protocol && values.port === listener.port) return [];
  const index = listenerIndex(nlb, listener);
  return [
    ...versionTest(nlb),
    { op: 'test', path: `/spec/listeners/${index}/name`, value: listener.name },
    { op: 'replace', path: `/spec/listeners/${index}/protocol`, value: values.protocol },
    { op: 'replace', path: `/spec/listeners/${index}/port`, value: values.port },
  ];
}

/**
 * A merge patch pointing a route's one backend somewhere else. Only for a
 * route that serves just this listener; its parentRefs are rewritten to name
 * the listener, since one that picked it by port would lose it to a port change.
 */
export function toRouteBackendPatch(
  route: NetworkLoadBalancerRoute,
  nlbName: string,
  listener: NetworkLoadBalancerListener,
  values: NlbListenerValues
) {
  const healthCheck =
    values.healthPort && values.healthPort !== SERVING_PORT
      ? { protocol: 'TCP', port: values.healthPort }
      : { protocol: 'TCP' };
  return {
    metadata: { resourceVersion: route.resourceVersion },
    spec: {
      parentRefs: [{ name: nlbName, listener: listener.name }],
      backends: [
        { networkService: { name: values.backend, port: values.backendPort }, healthCheck },
      ],
    },
  };
}

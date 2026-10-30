import {
  toHTTPProxyList,
  toNetwork,
  toNetworkInterfaceList,
  toNetworkList,
  toNetworkLoadBalancerList,
  toNetworkLoadBalancerRouteList,
  toNetworkServiceList,
  toSubnetList,
} from '../adapter';
import type {
  RawHTTPProxyList,
  RawNetwork,
  RawNetworkInterfaceList,
  RawNetworkList,
  RawNetworkLoadBalancerList,
  RawNetworkLoadBalancerRouteList,
  RawNetworkServiceList,
  RawSubnetList,
} from '../adapter';
import type {
  HTTPProxy,
  Network,
  NetworkInterface,
  NetworkLoadBalancer,
  NetworkLoadBalancerRoute,
  NetworkService,
  Subnet,
} from '../schema';
import { useMutation, useQueryClient, type UseMutationResult, useQuery, type UseQueryResult } from '@tanstack/react-query';

export const PLUGIN_ID = 'network.networking.datumapis.com';

const REFETCH_INTERVAL_MS = 10_000;

/** One entry of a Kubernetes Status's details.causes, e.g. field "spec.listeners[0].port". */
export interface ApiErrorCause {
  field?: string;
  message?: string;
}

export class ApiError extends Error {
  status: number;
  causes: ApiErrorCause[];

  constructor(status: number, message: string, causes: ApiErrorCause[] = []) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.causes = causes;
  }
}

function getProjectScopedBase(projectId: string): string {
  return `/api/proxy/apis/resourcemanager.miloapis.com/v1alpha1/projects/${encodeURIComponent(projectId)}/control-plane`;
}

// v1alpha, NOT v1alpha1 — verified against api/v1alpha/groupversion_info.go.
const NETWORKS_PATH = '/apis/networking.datumapis.com/v1alpha/namespaces/default/networks';
const SUBNETS_PATH = '/apis/networking.datumapis.com/v1alpha/namespaces/default/subnets';
const NETWORKINTERFACES_PATH = '/apis/networking.datumapis.com/v1alpha/namespaces/default/networkinterfaces';
const NETWORKSERVICES_PATH = '/apis/networking.datumapis.com/v1alpha/namespaces/default/networkservices';
const HTTPPROXIES_PATH = '/apis/networking.datumapis.com/v1alpha/namespaces/default/httpproxies';
const NETWORKLOADBALANCERS_PATH =
  '/apis/networking.datumapis.com/v1alpha/namespaces/default/networkloadbalancers';
const NETWORKLOADBALANCERROUTES_PATH =
  '/apis/networking.datumapis.com/v1alpha/namespaces/default/networkloadbalancerroutes';

async function proxyFetch<T>(projectId: string, path: string): Promise<T> {
  const url = `${getProjectScopedBase(projectId)}${path}`;
  const res = await fetch(url, { headers: { Accept: 'application/json' } });
  if (!res.ok) {
    throw new ApiError(res.status, `Request failed (${res.status}): ${path}`);
  }
  return res.json() as Promise<T>;
}

async function proxyDelete(projectId: string, path: string, describe: string): Promise<void> {
  const url = `${getProjectScopedBase(projectId)}${path}`;
  const res = await fetch(url, { method: 'DELETE', headers: { Accept: 'application/json' } });
  if (!res.ok) {
    let message = `Request failed (${res.status}): ${describe}`;
    try {
      const body = (await res.json()) as { message?: string };
      if (body.message) message = body.message;
    } catch {
    }
    throw new ApiError(res.status, message);
  }
}

async function fetchNetworks(projectId: string): Promise<Network[]> {
  const body = await proxyFetch<RawNetworkList>(projectId, `${NETWORKS_PATH}?limit=100`);
  return toNetworkList(body.items ?? []);
}

export function useNetworks(projectId: string | undefined): UseQueryResult<Network[], ApiError> {
  return useQuery({
    queryKey: [PLUGIN_ID, 'networks', projectId],
    enabled: !!projectId,
    queryFn: () => fetchNetworks(projectId as string),
    refetchInterval: REFETCH_INTERVAL_MS,
    retry: false,
  });
}

async function fetchNetwork(projectId: string, name: string): Promise<Network> {
  const body = await proxyFetch<RawNetwork>(projectId, `${NETWORKS_PATH}/${encodeURIComponent(name)}`);
  return toNetwork(body);
}

export function useNetwork(
  projectId: string | undefined,
  name: string | undefined
): UseQueryResult<Network, ApiError> {
  return useQuery({
    queryKey: [PLUGIN_ID, 'network', projectId, name],
    enabled: !!projectId && !!name,
    queryFn: () => fetchNetwork(projectId as string, name as string),
    refetchInterval: REFETCH_INTERVAL_MS,
    retry: false,
  });
}

async function fetchSubnets(projectId: string, networkName: string): Promise<Subnet[]> {
  const labelSelector = encodeURIComponent(`networking.datumapis.com/network=${networkName}`);
  const body = await proxyFetch<RawSubnetList>(
    projectId,
    `${SUBNETS_PATH}?labelSelector=${labelSelector}&limit=100`
  );
  return toSubnetList(body.items ?? []);
}

export function useSubnets(
  projectId: string | undefined,
  networkName: string | undefined
): UseQueryResult<Subnet[], ApiError> {
  return useQuery({
    queryKey: [PLUGIN_ID, 'subnets', projectId, networkName],
    enabled: !!projectId && !!networkName,
    queryFn: () => fetchSubnets(projectId as string, networkName as string),
    refetchInterval: REFETCH_INTERVAL_MS,
    retry: false,
  });
}

async function fetchNetworkInterfaces(
  projectId: string,
  networkName: string
): Promise<NetworkInterface[]> {
  const body = await proxyFetch<RawNetworkInterfaceList>(
    projectId,
    `${NETWORKINTERFACES_PATH}?limit=100`
  );
  return toNetworkInterfaceList(body.items ?? []).filter((iface) => iface.network === networkName);
}

export function useNetworkInterfaces(
  projectId: string | undefined,
  networkName: string | undefined
): UseQueryResult<NetworkInterface[], ApiError> {
  return useQuery({
    queryKey: [PLUGIN_ID, 'networkInterfaces', projectId, networkName],
    enabled: !!projectId && !!networkName,
    queryFn: () => fetchNetworkInterfaces(projectId as string, networkName as string),
    refetchInterval: REFETCH_INTERVAL_MS,
    retry: false,
  });
}

async function fetchNetworkServices(projectId: string): Promise<NetworkService[]> {
  const body = await proxyFetch<RawNetworkServiceList>(projectId, `${NETWORKSERVICES_PATH}?limit=100`);
  return toNetworkServiceList(body.items ?? []);
}

// No networkName param: a NetworkService's resolved network is never written
// back onto it, so there's no field to filter on. This lists every service
// in the project.
export function useNetworkServices(
  projectId: string | undefined
): UseQueryResult<NetworkService[], ApiError> {
  return useQuery({
    queryKey: [PLUGIN_ID, 'networkServices', projectId],
    enabled: !!projectId,
    queryFn: () => fetchNetworkServices(projectId as string),
    refetchInterval: REFETCH_INTERVAL_MS,
    retry: false,
  });
}

async function fetchHTTPProxies(projectId: string): Promise<HTTPProxy[]> {
  const body = await proxyFetch<RawHTTPProxyList>(projectId, `${HTTPPROXIES_PATH}?limit=100`);
  return toHTTPProxyList(body.items ?? []);
}

// No networkName param, same limitation as useNetworkServices: an
// HTTPProxy's backend never references a Network. This lists every proxy in
// the project; the topology diagram treats them all as candidates.
export function useHTTPProxies(projectId: string | undefined): UseQueryResult<HTTPProxy[], ApiError> {
  return useQuery({
    queryKey: [PLUGIN_ID, 'httpProxies', projectId],
    enabled: !!projectId,
    queryFn: () => fetchHTTPProxies(projectId as string),
    refetchInterval: REFETCH_INTERVAL_MS,
    retry: false,
  });
}

async function fetchNetworkLoadBalancers(projectId: string): Promise<NetworkLoadBalancer[]> {
  const body = await proxyFetch<RawNetworkLoadBalancerList>(
    projectId,
    `${NETWORKLOADBALANCERS_PATH}?limit=100`
  );
  return toNetworkLoadBalancerList(body.items ?? []);
}

export function useNetworkLoadBalancers(
  projectId: string | undefined
): UseQueryResult<NetworkLoadBalancer[], ApiError> {
  return useQuery({
    queryKey: [PLUGIN_ID, 'networkLoadBalancers', projectId],
    enabled: !!projectId,
    queryFn: () => fetchNetworkLoadBalancers(projectId as string),
    refetchInterval: REFETCH_INTERVAL_MS,
    retry: false,
  });
}

async function fetchNetworkLoadBalancerRoutes(projectId: string): Promise<NetworkLoadBalancerRoute[]> {
  const body = await proxyFetch<RawNetworkLoadBalancerRouteList>(
    projectId,
    `${NETWORKLOADBALANCERROUTES_PATH}?limit=100`
  );
  return toNetworkLoadBalancerRouteList(body.items ?? []);
}

// Every route in the project: a route names its NLBs in parentRefs, and
// there's no field selector for that, so callers filter by parent name.
export function useNetworkLoadBalancerRoutes(
  projectId: string | undefined
): UseQueryResult<NetworkLoadBalancerRoute[], ApiError> {
  return useQuery({
    queryKey: [PLUGIN_ID, 'networkLoadBalancerRoutes', projectId],
    enabled: !!projectId,
    queryFn: () => fetchNetworkLoadBalancerRoutes(projectId as string),
    refetchInterval: REFETCH_INTERVAL_MS,
    retry: false,
  });
}

export function computeWorkloadsHref(projectId: string): string {
  return `/project/${projectId}/services/compute-datumapis-com`;
}

export function albHref(projectId: string): string {
  return `/project/${projectId}/alb`;
}

export function computeWorkloadHref(projectId: string, workloadName: string): string {
  return `/project/${projectId}/services/compute-datumapis-com/${encodeURIComponent(workloadName)}`;
}

export interface NetworkSpecPatch {
  ipFamilies?: ('IPv4' | 'IPv6')[];
  mtu?: number;
}

async function patchNetwork(
  projectId: string,
  network: Network,
  spec: NetworkSpecPatch,
  action: string
): Promise<void> {
  const url = `${getProjectScopedBase(projectId)}${NETWORKS_PATH}/${encodeURIComponent(network.name)}`;
  const res = await fetch(url, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/merge-patch+json',
      Accept: 'application/json',
    },
    body: JSON.stringify({
      metadata: { resourceVersion: network.resourceVersion },
      spec,
    }),
  });
  if (!res.ok) {
    let message = `Request failed (${res.status}): ${action} ${network.name}`;
    try {
      const body = (await res.json()) as { message?: string };
      if (body.message) message = body.message;
    } catch {
    }
    throw new ApiError(res.status, message);
  }
}

function useNetworkPatch<TInput>(
  projectId: string | undefined,
  toPatch: (input: TInput) => { network: Network; spec: NetworkSpecPatch; action: string }
): UseMutationResult<void, ApiError, TInput> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: TInput) => {
      const { network, spec, action } = toPatch(input);
      return patchNetwork(projectId as string, network, spec, action);
    },
    onSuccess: (_data, input) => {
      const { network } = toPatch(input);
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'networks', projectId] });
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'network', projectId, network.name] });
    },
  });
}

export function useEnableIPv6(
  projectId: string | undefined
): UseMutationResult<void, ApiError, Network> {
  return useNetworkPatch(projectId, (network: Network) => ({
    network,
    spec: { ipFamilies: [...new Set([...network.ipFamilies, 'IPv6' as const])] },
    action: 'enable IPv6 on',
  }));
}

export function useUpdateNetwork(
  projectId: string | undefined
): UseMutationResult<void, ApiError, { network: Network; spec: NetworkSpecPatch }> {
  return useNetworkPatch(projectId, ({ network, spec }: { network: Network; spec: NetworkSpecPatch }) => ({
    network,
    spec,
    action: 'update network',
  }));
}

export interface CreateNetworkInput {
  name: string;
  ipFamilies: ('IPv4' | 'IPv6')[];
  mtu?: number;
}

async function createNetwork(projectId: string, input: CreateNetworkInput): Promise<void> {
  const url = `${getProjectScopedBase(projectId)}${NETWORKS_PATH}`;
  const res = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({
      apiVersion: 'networking.datumapis.com/v1alpha',
      kind: 'Network',
      metadata: { name: input.name, namespace: 'default' },
      spec: {
        ipam: { mode: 'Auto' },
        ipFamilies: input.ipFamilies,
        ...(input.mtu ? { mtu: input.mtu } : {}),
      },
    }),
  });
  if (!res.ok) {
    let message = `Request failed (${res.status}): create network ${input.name}`;
    try {
      const body = (await res.json()) as { message?: string };
      if (body.message) message = body.message;
    } catch {
    }
    throw new ApiError(res.status, message);
  }
}

export function useCreateNetwork(
  projectId: string | undefined
): UseMutationResult<void, ApiError, CreateNetworkInput> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateNetworkInput) => createNetwork(projectId as string, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'networks', projectId] });
    },
  });
}

export function useDeleteNetwork(
  projectId: string | undefined
): UseMutationResult<void, ApiError, string> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      proxyDelete(
        projectId as string,
        `${NETWORKS_PATH}/${encodeURIComponent(name)}`,
        `delete network ${name}`
      ),
    onSuccess: (_data, name) => {
      // Invalidate alone only marks the query stale, so a caller navigating
      // straight to the list would still render the deleted network until
      // the background refetch lands. Drop it from cache immediately instead.
      queryClient.setQueryData<Network[]>([PLUGIN_ID, 'networks', projectId], (networks) =>
        networks?.filter((n) => n.name !== name)
      );
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'networks', projectId] });
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'network', projectId, name] });
    },
  });
}

export interface CreateNetworkServiceInput {
  name: string;
  matchLabels: Record<string, string>;
  ports: { name: string; port: number; protocol: 'TCP' | 'UDP' }[];
}

async function createNetworkService(
  projectId: string,
  input: CreateNetworkServiceInput
): Promise<void> {
  const url = `${getProjectScopedBase(projectId)}${NETWORKSERVICES_PATH}`;
  const res = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: JSON.stringify({
      apiVersion: 'networking.datumapis.com/v1alpha',
      kind: 'NetworkService',
      metadata: { name: input.name, namespace: 'default' },
      spec: {
        networkInterfaces: { selector: { matchLabels: input.matchLabels } },
        ports: input.ports.map((p) => ({ name: p.name, port: p.port, protocol: p.protocol })),
      },
    }),
  });
  if (!res.ok) {
    let message = `Request failed (${res.status}): create service ${input.name}`;
    try {
      const body = (await res.json()) as { message?: string };
      if (body.message) message = body.message;
    } catch {
    }
    throw new ApiError(res.status, message);
  }
}

export function useCreateNetworkService(
  projectId: string | undefined
): UseMutationResult<void, ApiError, CreateNetworkServiceInput> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateNetworkServiceInput) =>
      createNetworkService(projectId as string, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'networkServices', projectId] });
    },
  });
}

export function useDeleteNetworkService(
  projectId: string | undefined
): UseMutationResult<void, ApiError, string> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      proxyDelete(
        projectId as string,
        `${NETWORKSERVICES_PATH}/${encodeURIComponent(name)}`,
        `delete service ${name}`
      ),
    onSuccess: (_data, name) => {
      queryClient.setQueryData<NetworkService[]>([PLUGIN_ID, 'networkServices', projectId], (services) =>
        services?.filter((s) => s.name !== name)
      );
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'networkServices', projectId] });
    },
  });
}

async function toApiError(res: Response, fallback: string): Promise<ApiError> {
  try {
    const body = (await res.json()) as { message?: string; details?: { causes?: ApiErrorCause[] } };
    return new ApiError(res.status, body.message ?? fallback, body.details?.causes ?? []);
  } catch {
    return new ApiError(res.status, fallback);
  }
}

async function proxyCreate(projectId: string, path: string, body: unknown, describe: string): Promise<void> {
  const res = await fetch(`${getProjectScopedBase(projectId)}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw await toApiError(res, `Request failed (${res.status}): ${describe}`);
}

export interface NlbCreateRequests {
  nlb: { metadata: { name: string; namespace: string } } & Record<string, unknown>;
  route: { metadata: { name: string; namespace: string } } & Record<string, unknown>;
}

export function createNetworkLoadBalancer(projectId: string, body: NlbCreateRequests['nlb']): Promise<void> {
  return proxyCreate(projectId, NETWORKLOADBALANCERS_PATH, body, `create load balancer ${body.metadata.name}`);
}

export function createNetworkLoadBalancerRoute(projectId: string, body: NlbCreateRequests['route']): Promise<void> {
  return proxyCreate(projectId, NETWORKLOADBALANCERROUTES_PATH, body, `create route ${body.metadata.name}`);
}

/**
 * What deleting an NLB does to its routes. A route attached only to this NLB
 * goes with it. One also attached elsewhere just drops this NLB from its
 * parentRefs, so the other NLB keeps its backend.
 */
export function planNlbDelete(nlbName: string, routes: NetworkLoadBalancerRoute[]) {
  const attached = routes.filter((r) => r.parents.some((p) => p.name === nlbName));
  return {
    deleteRoutes: attached.filter((r) => r.parents.every((p) => p.name === nlbName)),
    detachRoutes: attached
      .filter((r) => r.parents.some((p) => p.name !== nlbName))
      .map((route) => ({ route, parents: route.parents.filter((p) => p.name !== nlbName) })),
  };
}

async function detachRoute(projectId: string, route: NetworkLoadBalancerRoute, parents: NetworkLoadBalancerRoute['parents']) {
  const res = await fetch(
    `${getProjectScopedBase(projectId)}${NETWORKLOADBALANCERROUTES_PATH}/${encodeURIComponent(route.name)}`,
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/merge-patch+json', Accept: 'application/json' },
      // parentRefs is an atomic list, so a merge patch replaces it whole.
      body: JSON.stringify({ metadata: { resourceVersion: route.resourceVersion }, spec: { parentRefs: parents } }),
    }
  );
  if (!res.ok) throw await toApiError(res, `Request failed (${res.status}): update route ${route.name}`);
}

export function useDeleteNetworkLoadBalancer(
  projectId: string | undefined
): UseMutationResult<void, ApiError, { name: string; routes: NetworkLoadBalancerRoute[] }> {
  const queryClient = useQueryClient();
  return useMutation({
    // Routes first, so a failure part way leaves the NLB in place to retry
    // from rather than routes pointing at nothing.
    mutationFn: async ({ name, routes }) => {
      const plan = planNlbDelete(name, routes);
      for (const { route, parents } of plan.detachRoutes) await detachRoute(projectId as string, route, parents);
      for (const route of plan.deleteRoutes) {
        await proxyDelete(
          projectId as string,
          `${NETWORKLOADBALANCERROUTES_PATH}/${encodeURIComponent(route.name)}`,
          `delete route ${route.name}`
        );
      }
      await proxyDelete(
        projectId as string,
        `${NETWORKLOADBALANCERS_PATH}/${encodeURIComponent(name)}`,
        `delete load balancer ${name}`
      );
    },
    onSuccess: (_data, { name }) => {
      queryClient.setQueryData<NetworkLoadBalancer[]>([PLUGIN_ID, 'networkLoadBalancers', projectId], (nlbs) =>
        nlbs?.filter((n) => n.name !== name)
      );
    },
    // Even a delete that failed part way may have changed routes.
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'networkLoadBalancers', projectId] });
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'networkLoadBalancerRoutes', projectId] });
    },
  });
}

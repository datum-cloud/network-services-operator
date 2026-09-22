import {
  toHTTPProxyList,
  toNetwork,
  toNetworkInterfaceList,
  toNetworkList,
  toNetworkServiceList,
  toSubnetList,
} from '../adapter';
import type {
  RawHTTPProxyList,
  RawNetwork,
  RawNetworkInterfaceList,
  RawNetworkList,
  RawNetworkServiceList,
  RawSubnetList,
} from '../adapter';
import type { HTTPProxy, Network, NetworkInterface, NetworkService, Subnet } from '../schema';
import { useMutation, useQueryClient, type UseMutationResult, useQuery, type UseQueryResult } from '@tanstack/react-query';

export const PLUGIN_ID = 'network.networking.datumapis.com';

const REFETCH_INTERVAL_MS = 10_000;

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
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

interface EnableIPv6Args {
  projectId: string;
  network: Network;
}

async function enableIPv6({ projectId, network }: EnableIPv6Args): Promise<void> {
  const url = `${getProjectScopedBase(projectId)}${NETWORKS_PATH}/${encodeURIComponent(network.name)}`;
  const res = await fetch(url, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/merge-patch+json',
      Accept: 'application/json',
    },
    body: JSON.stringify({
      metadata: { resourceVersion: network.resourceVersion },
      spec: { ipFamilies: [...new Set([...network.ipFamilies, 'IPv6'])] },
    }),
  });
  if (!res.ok) {
    let message = `Request failed (${res.status}): enable IPv6 on ${network.name}`;
    try {
      const body = (await res.json()) as { message?: string };
      if (body.message) message = body.message;
    } catch {
    }
    throw new ApiError(res.status, message);
  }
}

export function useEnableIPv6(
  projectId: string | undefined
): UseMutationResult<void, ApiError, Network> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (network: Network) => enableIPv6({ projectId: projectId as string, network }),
    onSuccess: (_data, network) => {
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'networks', projectId] });
      void queryClient.invalidateQueries({ queryKey: [PLUGIN_ID, 'network', projectId, network.name] });
    },
  });
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
  ports: { name: string; port: number }[];
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
        ports: input.ports.map((p) => ({ name: p.name, port: p.port, protocol: 'TCP' })),
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

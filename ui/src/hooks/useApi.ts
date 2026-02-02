import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiClient } from '../api/client';
import type {
  HTTPProxy,
  Domain,
  TrafficProtectionPolicy,
  Connector,
  ConnectorAdvertisement,
  TestProxyRequest,
} from '../api/types';

// Query Keys
export const queryKeys = {
  httpProxies: (namespace?: string) => ['httpProxies', namespace] as const,
  httpProxy: (name: string, namespace: string) => ['httpProxy', namespace, name] as const,
  domains: (namespace?: string) => ['domains', namespace] as const,
  domain: (name: string, namespace: string) => ['domain', namespace, name] as const,
  policies: (namespace?: string) => ['policies', namespace] as const,
  policy: (name: string, namespace: string) => ['policy', namespace, name] as const,
  connectors: (namespace?: string) => ['connectors', namespace] as const,
  connector: (name: string, namespace: string) => ['connector', namespace, name] as const,
  connectorAdvertisements: (namespace?: string) => ['connectorAdvertisements', namespace] as const,
  connectorAdvertisementsByConnector: (name: string, namespace: string) =>
    ['connectorAdvertisements', 'byConnector', namespace, name] as const,
  dashboardStats: () => ['dashboardStats'] as const,
  recentActivity: () => ['recentActivity'] as const,
};

// HTTPProxy Hooks
export function useHTTPProxies(namespace?: string) {
  return useQuery({
    queryKey: queryKeys.httpProxies(namespace),
    queryFn: () => apiClient.listHTTPProxies(namespace),
  });
}

export function useHTTPProxy(name: string, namespace: string) {
  return useQuery({
    queryKey: queryKeys.httpProxy(name, namespace),
    queryFn: () => apiClient.getHTTPProxy(name, namespace),
    enabled: !!name && !!namespace,
  });
}

export function useCreateHTTPProxy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (proxy: HTTPProxy) => apiClient.createHTTPProxy(proxy),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['httpProxies'] });
    },
  });
}

export function useUpdateHTTPProxy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (proxy: HTTPProxy) => apiClient.updateHTTPProxy(proxy),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['httpProxies'] });
      queryClient.invalidateQueries({
        queryKey: queryKeys.httpProxy(data.metadata.name, data.metadata.namespace || ''),
      });
    },
  });
}

export function useDeleteHTTPProxy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, namespace }: { name: string; namespace: string }) =>
      apiClient.deleteHTTPProxy(name, namespace),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['httpProxies'] });
    },
  });
}

export function useTestHTTPProxy() {
  return useMutation({
    mutationFn: ({
      name,
      namespace,
      request,
    }: {
      name: string;
      namespace: string;
      request: TestProxyRequest;
    }) => apiClient.testHTTPProxy(name, namespace, request),
  });
}

// Domain Hooks
export function useDomains(namespace?: string) {
  return useQuery({
    queryKey: queryKeys.domains(namespace),
    queryFn: () => apiClient.listDomains(namespace),
  });
}

export function useDomain(name: string, namespace: string) {
  return useQuery({
    queryKey: queryKeys.domain(name, namespace),
    queryFn: () => apiClient.getDomain(name, namespace),
    enabled: !!name && !!namespace,
  });
}

export function useCreateDomain() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (domain: Domain) => apiClient.createDomain(domain),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['domains'] });
    },
  });
}

export function useDeleteDomain() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, namespace }: { name: string; namespace: string }) =>
      apiClient.deleteDomain(name, namespace),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['domains'] });
    },
  });
}

// Policy Hooks
export function usePolicies(namespace?: string) {
  return useQuery({
    queryKey: queryKeys.policies(namespace),
    queryFn: () => apiClient.listPolicies(namespace),
  });
}

export function usePolicy(name: string, namespace: string) {
  return useQuery({
    queryKey: queryKeys.policy(name, namespace),
    queryFn: () => apiClient.getPolicy(name, namespace),
    enabled: !!name && !!namespace,
  });
}

export function useCreatePolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (policy: TrafficProtectionPolicy) => apiClient.createPolicy(policy),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['policies'] });
    },
  });
}

export function useUpdatePolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (policy: TrafficProtectionPolicy) => apiClient.updatePolicy(policy),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['policies'] });
      queryClient.invalidateQueries({
        queryKey: queryKeys.policy(data.metadata.name, data.metadata.namespace || ''),
      });
    },
  });
}

export function useDeletePolicy() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ name, namespace }: { name: string; namespace: string }) =>
      apiClient.deletePolicy(name, namespace),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['policies'] });
    },
  });
}

// Connector Hooks
export function useConnectors(namespace?: string) {
  return useQuery({
    queryKey: queryKeys.connectors(namespace),
    queryFn: () => apiClient.listConnectors(namespace),
  });
}

export function useConnector(name: string, namespace: string) {
  return useQuery({
    queryKey: queryKeys.connector(name, namespace),
    queryFn: () => apiClient.getConnector(name, namespace),
    enabled: !!name && !!namespace,
  });
}

// ConnectorAdvertisement Hooks
export function useConnectorAdvertisements(namespace?: string) {
  return useQuery({
    queryKey: queryKeys.connectorAdvertisements(namespace),
    queryFn: () => apiClient.listConnectorAdvertisements(namespace),
  });
}

export function useConnectorAdvertisementsByConnector(name: string, namespace: string) {
  return useQuery({
    queryKey: queryKeys.connectorAdvertisementsByConnector(name, namespace),
    queryFn: () => apiClient.getConnectorAdvertisementsByConnector(name, namespace),
    enabled: !!name && !!namespace,
  });
}

// Dashboard Hooks
export function useDashboardStats() {
  return useQuery({
    queryKey: queryKeys.dashboardStats(),
    queryFn: () => apiClient.getDashboardStats(),
  });
}

export function useRecentActivity() {
  return useQuery({
    queryKey: queryKeys.recentActivity(),
    queryFn: () => apiClient.getRecentActivity(),
  });
}

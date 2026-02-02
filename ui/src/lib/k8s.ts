import * as k8s from '@kubernetes/client-node';

const kc = new k8s.KubeConfig();

// Load kubeconfig based on environment:
// 1. If KUBECONFIG env var is set, use that file
// 2. Try in-cluster config (for running in a pod)
// 3. Fall back to default kubeconfig (~/.kube/config)
if (process.env.KUBECONFIG) {
  kc.loadFromFile(process.env.KUBECONFIG);
} else {
  try {
    kc.loadFromCluster();
  } catch {
    kc.loadFromDefault();
  }
}

export const customObjectsApi = kc.makeApiClient(k8s.CustomObjectsApi);
export const coreV1Api = kc.makeApiClient(k8s.CoreV1Api);
export const discoveryV1Api = kc.makeApiClient(k8s.DiscoveryV1Api);

// Constants for API groups
export const NETWORKING_GROUP = 'networking.datumapis.com';
export const GATEWAY_GROUP = 'gateway.networking.k8s.io';
export const DISCOVERY_GROUP = 'discovery.k8s.io';
export const V1ALPHA = 'v1alpha';
export const V1ALPHA1 = 'v1alpha1';
export const V1 = 'v1';

// Resource plural names - networking.datumapis.com
export const HTTPPROXIES = 'httpproxies';
export const DOMAINS = 'domains';
export const TRAFFIC_PROTECTION_POLICIES = 'trafficprotectionpolicies';
export const CONNECTORS = 'connectors';
export const CONNECTOR_ADVERTISEMENTS = 'connectoradvertisements';

// Resource plural names - gateway.networking.k8s.io
export const GATEWAYS = 'gateways';
export const HTTPROUTES = 'httproutes';

// Resource plural names - discovery.k8s.io
export const ENDPOINTSLICES = 'endpointslices';

// Helper function to check if running in development mode
export function isDevelopment(): boolean {
  return process.env.NODE_ENV === 'development';
}

import { NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA,
  V1ALPHA1,
  HTTPPROXIES,
  DOMAINS,
  TRAFFIC_PROTECTION_POLICIES,
  CONNECTORS,
  isDevelopment,
} from '@/lib/k8s';
import { getMockDashboardStats, mockRecentActivity } from '@/lib/mock-data';

interface K8sCondition {
  type: string;
  status: string;
}

interface K8sResource {
  status?: {
    conditions?: K8sCondition[];
  };
  spec?: {
    mode?: string;
  };
}

interface K8sListResponse {
  body: {
    items: K8sResource[];
  };
}

export async function GET() {
  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const stats = getMockDashboardStats();
    return NextResponse.json({
      ...stats,
      recentActivity: mockRecentActivity,
    });
  }

  try {
    // Fetch all resources across all namespaces
    const [proxiesRes, domainsRes, policiesRes, connectorsRes] = await Promise.all([
      customObjectsApi.listClusterCustomObject(
        NETWORKING_GROUP,
        V1ALPHA,
        HTTPPROXIES
      ) as Promise<K8sListResponse>,
      customObjectsApi.listClusterCustomObject(
        NETWORKING_GROUP,
        V1ALPHA,
        DOMAINS
      ) as Promise<K8sListResponse>,
      customObjectsApi.listClusterCustomObject(
        NETWORKING_GROUP,
        V1ALPHA,
        TRAFFIC_PROTECTION_POLICIES
      ) as Promise<K8sListResponse>,
      customObjectsApi.listClusterCustomObject(
        NETWORKING_GROUP,
        V1ALPHA1,
        CONNECTORS
      ) as Promise<K8sListResponse>,
    ]);

    const proxies = proxiesRes.body.items || [];
    const domains = domainsRes.body.items || [];
    const policies = policiesRes.body.items || [];
    const connectors = connectorsRes.body.items || [];

    // Calculate gateway stats
    const healthyGateways = proxies.filter((p: K8sResource) =>
      p.status?.conditions?.some(
        (c: K8sCondition) => c.type === 'Programmed' && c.status === 'True'
      )
    ).length;

    // Calculate domain stats
    const verifiedDomains = domains.filter((d: K8sResource) =>
      d.status?.conditions?.some(
        (c: K8sCondition) => c.type === 'Verified' && c.status === 'True'
      )
    ).length;

    // Calculate connector stats
    const connectedConnectors = connectors.filter((c: K8sResource) =>
      c.status?.conditions?.some(
        (cond: K8sCondition) => cond.type === 'Ready' && cond.status === 'True'
      )
    ).length;

    // Calculate policy stats
    const enforcingPolicies = policies.filter(
      (p: K8sResource) => p.spec?.mode === 'Enforce'
    ).length;
    const observingPolicies = policies.filter(
      (p: K8sResource) => p.spec?.mode === 'Observe'
    ).length;
    const disabledPolicies = policies.filter(
      (p: K8sResource) => p.spec?.mode === 'Disabled'
    ).length;

    return NextResponse.json({
      gateways: {
        total: proxies.length,
        healthy: healthyGateways,
        unhealthy: proxies.length - healthyGateways,
      },
      domains: {
        total: domains.length,
        verified: verifiedDomains,
        pending: domains.length - verifiedDomains,
      },
      policies: {
        total: policies.length,
        enforcing: enforcingPolicies,
        observing: observingPolicies,
        disabled: disabledPolicies,
      },
      connectors: {
        total: connectors.length,
        connected: connectedConnectors,
        disconnected: connectors.length - connectedConnectors,
      },
    });
  } catch (error) {
    console.error('Failed to fetch dashboard stats:', error);
    return NextResponse.json(
      { error: 'Failed to fetch dashboard stats' },
      { status: 500 }
    );
  }
}

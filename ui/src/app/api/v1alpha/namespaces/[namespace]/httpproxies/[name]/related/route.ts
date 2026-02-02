import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  discoveryV1Api,
  NETWORKING_GROUP,
  GATEWAY_GROUP,
  V1ALPHA,
  V1,
  HTTPPROXIES,
  DOMAINS,
  GATEWAYS,
  HTTPROUTES,
  isDevelopment,
} from '@/lib/k8s';
import { mockHTTPProxies, mockDomains } from '@/lib/mock-data';
import type {
  HTTPProxy,
  Gateway,
  HTTPRoute,
  EndpointSlice,
  Domain,
  HTTPProxyRelatedResources,
} from '@/api/types';

type RouteParams = { params: Promise<{ namespace: string; name: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
): Promise<NextResponse<HTTPProxyRelatedResources | { error: string }>> {
  const { namespace, name } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const proxy = mockHTTPProxies.find(
      (p) => p.metadata.name === name && p.metadata.namespace === namespace
    );
    if (!proxy) {
      return NextResponse.json(
        { error: 'HTTPProxy not found' },
        { status: 404 }
      );
    }

    // Return mock related resources
    const hostnames = proxy.spec.hostnames || [];
    const relatedDomains = mockDomains.filter((d) =>
      hostnames.some((h) => h === d.spec.domainName || h.endsWith('.' + d.spec.domainName))
    );

    return NextResponse.json({
      gateway: null, // No mock gateway
      httpRoute: null, // No mock httproute
      endpointSlices: [], // No mock endpointslices
      domains: relatedDomains,
    });
  }

  try {
    // First fetch the HTTPProxy to get hostnames
    const httpProxyResponse = await customObjectsApi.getNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      HTTPPROXIES,
      name
    );
    const httpProxy = httpProxyResponse.body as HTTPProxy;

    // Fetch related resources in parallel
    const [gatewayResult, httpRouteResult, endpointSlicesResult, domainsResult] =
      await Promise.allSettled([
        // Gateway - same name as HTTPProxy
        customObjectsApi.getNamespacedCustomObject(
          GATEWAY_GROUP,
          V1,
          namespace,
          GATEWAYS,
          name
        ),
        // HTTPRoute - same name as HTTPProxy
        customObjectsApi.getNamespacedCustomObject(
          GATEWAY_GROUP,
          V1,
          namespace,
          HTTPROUTES,
          name
        ),
        // EndpointSlices - list all in namespace, filter by owner
        discoveryV1Api.listNamespacedEndpointSlice(namespace),
        // Domains - list all in namespace, filter by hostname
        customObjectsApi.listNamespacedCustomObject(
          NETWORKING_GROUP,
          V1ALPHA,
          namespace,
          DOMAINS
        ),
      ]);

    // Extract Gateway
    let gateway: Gateway | null = null;
    if (gatewayResult.status === 'fulfilled') {
      gateway = gatewayResult.value.body as Gateway;
    } else {
      console.error(`Failed to fetch Gateway ${namespace}/${name}:`, gatewayResult.reason);
    }

    // Extract HTTPRoute
    let httpRoute: HTTPRoute | null = null;
    if (httpRouteResult.status === 'fulfilled') {
      httpRoute = httpRouteResult.value.body as HTTPRoute;
    } else {
      console.error(`Failed to fetch HTTPRoute ${namespace}/${name}:`, httpRouteResult.reason);
    }

    // Extract and filter EndpointSlices by owner reference
    let endpointSlices: EndpointSlice[] = [];
    if (endpointSlicesResult.status === 'fulfilled') {
      const allSlices = endpointSlicesResult.value.body.items || [];
      endpointSlices = allSlices.filter((slice) => {
        const ownerRefs = slice.metadata?.ownerReferences || [];
        return ownerRefs.some(
          (ref) =>
            ref.kind === 'HTTPProxy' &&
            ref.name === name &&
            ref.apiVersion?.startsWith('networking.datumapis.com')
        );
      }) as EndpointSlice[];
    } else {
      console.error(`Failed to fetch EndpointSlices in ${namespace}:`, endpointSlicesResult.reason);
    }

    // Extract and filter Domains by hostname match
    let domains: Domain[] = [];
    if (domainsResult.status === 'fulfilled') {
      const allDomains = (domainsResult.value.body as { items: Domain[] }).items || [];
      const hostnames = httpProxy.spec.hostnames || [];
      domains = allDomains.filter((domain) => {
        const domainName = domain.spec.domainName;
        return hostnames.some(
          (h) => h === domainName || h.endsWith('.' + domainName)
        );
      });
    } else {
      console.error(`Failed to fetch Domains in ${namespace}:`, domainsResult.reason);
    }

    return NextResponse.json({
      gateway,
      httpRoute,
      endpointSlices,
      domains,
    });
  } catch (error: unknown) {
    const err = error as { response?: { statusCode?: number } };
    if (err.response?.statusCode === 404) {
      return NextResponse.json(
        { error: 'HTTPProxy not found' },
        { status: 404 }
      );
    }
    console.error('Failed to get related resources:', error);
    return NextResponse.json(
      { error: 'Failed to get related resources' },
      { status: 500 }
    );
  }
}

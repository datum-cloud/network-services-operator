import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA,
  HTTPPROXIES,
  isDevelopment,
} from '@/lib/k8s';
import { mockHTTPProxies } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const items = mockHTTPProxies.filter((p) => p.metadata.namespace === namespace);
    return NextResponse.json({
      apiVersion: `${NETWORKING_GROUP}/${V1ALPHA}`,
      kind: 'HTTPProxyList',
      items,
    });
  }

  try {
    const response = await customObjectsApi.listNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      HTTPPROXIES
    );
    return NextResponse.json(response.body);
  } catch (error) {
    console.error('Failed to list HTTPProxies:', error);
    return NextResponse.json(
      { error: 'Failed to list HTTPProxies' },
      { status: 500 }
    );
  }
}

export async function POST(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace } = await context.params;
  const body = await request.json();

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const newProxy = {
      ...body,
      metadata: {
        ...body.metadata,
        namespace,
        uid: `uuid-${Date.now()}`,
        creationTimestamp: new Date().toISOString(),
      },
    };
    return NextResponse.json(newProxy, { status: 201 });
  }

  try {
    const response = await customObjectsApi.createNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      HTTPPROXIES,
      body
    );
    return NextResponse.json(response.body, { status: 201 });
  } catch (error) {
    console.error('Failed to create HTTPProxy:', error);
    return NextResponse.json(
      { error: 'Failed to create HTTPProxy' },
      { status: 500 }
    );
  }
}

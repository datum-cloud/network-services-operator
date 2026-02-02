import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA,
  HTTPPROXIES,
  isDevelopment,
} from '@/lib/k8s';
import { mockHTTPProxies } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string; name: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
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
    return NextResponse.json(proxy);
  }

  try {
    const response = await customObjectsApi.getNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      HTTPPROXIES,
      name
    );
    return NextResponse.json(response.body);
  } catch (error: unknown) {
    const err = error as { response?: { statusCode?: number } };
    if (err.response?.statusCode === 404) {
      return NextResponse.json(
        { error: 'HTTPProxy not found' },
        { status: 404 }
      );
    }
    console.error('Failed to get HTTPProxy:', error);
    return NextResponse.json(
      { error: 'Failed to get HTTPProxy' },
      { status: 500 }
    );
  }
}

export async function PUT(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace, name } = await context.params;
  const body = await request.json();

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    return NextResponse.json(body);
  }

  try {
    const response = await customObjectsApi.replaceNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      HTTPPROXIES,
      name,
      body
    );
    return NextResponse.json(response.body);
  } catch (error) {
    console.error('Failed to update HTTPProxy:', error);
    return NextResponse.json(
      { error: 'Failed to update HTTPProxy' },
      { status: 500 }
    );
  }
}

export async function DELETE(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace, name } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    return new NextResponse(null, { status: 204 });
  }

  try {
    await customObjectsApi.deleteNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      HTTPPROXIES,
      name
    );
    return new NextResponse(null, { status: 204 });
  } catch (error) {
    console.error('Failed to delete HTTPProxy:', error);
    return NextResponse.json(
      { error: 'Failed to delete HTTPProxy' },
      { status: 500 }
    );
  }
}

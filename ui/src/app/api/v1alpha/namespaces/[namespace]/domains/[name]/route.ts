import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA,
  DOMAINS,
  isDevelopment,
} from '@/lib/k8s';
import { mockDomains } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string; name: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace, name } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const domain = mockDomains.find(
      (d) => d.metadata.name === name && d.metadata.namespace === namespace
    );
    if (!domain) {
      return NextResponse.json({ error: 'Domain not found' }, { status: 404 });
    }
    return NextResponse.json(domain);
  }

  try {
    const response = await customObjectsApi.getNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      DOMAINS,
      name
    );
    return NextResponse.json(response.body);
  } catch (error: unknown) {
    const err = error as { response?: { statusCode?: number } };
    if (err.response?.statusCode === 404) {
      return NextResponse.json({ error: 'Domain not found' }, { status: 404 });
    }
    console.error('Failed to get Domain:', error);
    return NextResponse.json(
      { error: 'Failed to get Domain' },
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
      DOMAINS,
      name,
      body
    );
    return NextResponse.json(response.body);
  } catch (error) {
    console.error('Failed to update Domain:', error);
    return NextResponse.json(
      { error: 'Failed to update Domain' },
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
      DOMAINS,
      name
    );
    return new NextResponse(null, { status: 204 });
  } catch (error) {
    console.error('Failed to delete Domain:', error);
    return NextResponse.json(
      { error: 'Failed to delete Domain' },
      { status: 500 }
    );
  }
}

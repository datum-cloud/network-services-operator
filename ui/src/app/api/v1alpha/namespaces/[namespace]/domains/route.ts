import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA,
  DOMAINS,
  isDevelopment,
} from '@/lib/k8s';
import { mockDomains } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const items = mockDomains.filter((d) => d.metadata.namespace === namespace);
    return NextResponse.json({
      apiVersion: `${NETWORKING_GROUP}/${V1ALPHA}`,
      kind: 'DomainList',
      items,
    });
  }

  try {
    const response = await customObjectsApi.listNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      DOMAINS
    );
    return NextResponse.json(response.body);
  } catch (error) {
    console.error('Failed to list Domains:', error);
    return NextResponse.json(
      { error: 'Failed to list Domains' },
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
    const newDomain = {
      ...body,
      metadata: {
        ...body.metadata,
        namespace,
        uid: `domain-uuid-${Date.now()}`,
        creationTimestamp: new Date().toISOString(),
      },
    };
    return NextResponse.json(newDomain, { status: 201 });
  }

  try {
    const response = await customObjectsApi.createNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      DOMAINS,
      body
    );
    return NextResponse.json(response.body, { status: 201 });
  } catch (error) {
    console.error('Failed to create Domain:', error);
    return NextResponse.json(
      { error: 'Failed to create Domain' },
      { status: 500 }
    );
  }
}

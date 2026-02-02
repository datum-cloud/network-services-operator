import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA1,
  CONNECTORS,
  isDevelopment,
} from '@/lib/k8s';
import { mockConnectors } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string; name: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace, name } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const connector = mockConnectors.find(
      (c) => c.metadata.name === name && c.metadata.namespace === namespace
    );
    if (!connector) {
      return NextResponse.json(
        { error: 'Connector not found' },
        { status: 404 }
      );
    }
    return NextResponse.json(connector);
  }

  try {
    const response = await customObjectsApi.getNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA1,
      namespace,
      CONNECTORS,
      name
    );
    return NextResponse.json(response.body);
  } catch (error: unknown) {
    const err = error as { response?: { statusCode?: number } };
    if (err.response?.statusCode === 404) {
      return NextResponse.json(
        { error: 'Connector not found' },
        { status: 404 }
      );
    }
    console.error('Failed to get Connector:', error);
    return NextResponse.json(
      { error: 'Failed to get Connector' },
      { status: 500 }
    );
  }
}

import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA1,
  CONNECTORS,
  isDevelopment,
} from '@/lib/k8s';
import { mockConnectors } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const items = mockConnectors.filter((c) => c.metadata.namespace === namespace);
    return NextResponse.json({
      apiVersion: `${NETWORKING_GROUP}/${V1ALPHA1}`,
      kind: 'ConnectorList',
      items,
    });
  }

  try {
    const response = await customObjectsApi.listNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA1,
      namespace,
      CONNECTORS
    );
    return NextResponse.json(response.body);
  } catch (error) {
    console.error('Failed to list Connectors:', error);
    return NextResponse.json(
      { error: 'Failed to list Connectors' },
      { status: 500 }
    );
  }
}

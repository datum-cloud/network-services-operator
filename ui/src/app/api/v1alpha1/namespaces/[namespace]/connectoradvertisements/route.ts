import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA1,
  CONNECTOR_ADVERTISEMENTS,
  isDevelopment,
} from '@/lib/k8s';
import { mockConnectorAdvertisements } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace } = await context.params;
  const { searchParams } = new URL(request.url);
  const connectorName = searchParams.get('connector');

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    let items = mockConnectorAdvertisements.filter(
      (a) => a.metadata.namespace === namespace
    );
    if (connectorName) {
      items = items.filter((a) => a.spec.connectorRef.name === connectorName);
    }
    return NextResponse.json({
      apiVersion: `${NETWORKING_GROUP}/${V1ALPHA1}`,
      kind: 'ConnectorAdvertisementList',
      items,
    });
  }

  try {
    const response = await customObjectsApi.listNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA1,
      namespace,
      CONNECTOR_ADVERTISEMENTS
    );

    // Filter by connector if specified
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let body = response.body as any;
    if (connectorName && body.items) {
      body = {
        ...body,
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        items: body.items.filter((a: any) => a.spec?.connectorRef?.name === connectorName),
      };
    }

    return NextResponse.json(body);
  } catch (error) {
    console.error('Failed to list ConnectorAdvertisements:', error);
    return NextResponse.json(
      { error: 'Failed to list ConnectorAdvertisements' },
      { status: 500 }
    );
  }
}

import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA,
  TRAFFIC_PROTECTION_POLICIES,
  isDevelopment,
} from '@/lib/k8s';
import { mockPolicies } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const items = mockPolicies.filter((p) => p.metadata.namespace === namespace);
    return NextResponse.json({
      apiVersion: `${NETWORKING_GROUP}/${V1ALPHA}`,
      kind: 'TrafficProtectionPolicyList',
      items,
    });
  }

  try {
    const response = await customObjectsApi.listNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      TRAFFIC_PROTECTION_POLICIES
    );
    return NextResponse.json(response.body);
  } catch (error) {
    console.error('Failed to list TrafficProtectionPolicies:', error);
    return NextResponse.json(
      { error: 'Failed to list TrafficProtectionPolicies' },
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
    const newPolicy = {
      ...body,
      metadata: {
        ...body.metadata,
        namespace,
        uid: `policy-uuid-${Date.now()}`,
        creationTimestamp: new Date().toISOString(),
      },
    };
    return NextResponse.json(newPolicy, { status: 201 });
  }

  try {
    const response = await customObjectsApi.createNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      TRAFFIC_PROTECTION_POLICIES,
      body
    );
    return NextResponse.json(response.body, { status: 201 });
  } catch (error) {
    console.error('Failed to create TrafficProtectionPolicy:', error);
    return NextResponse.json(
      { error: 'Failed to create TrafficProtectionPolicy' },
      { status: 500 }
    );
  }
}

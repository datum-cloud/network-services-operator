import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA,
  TRAFFIC_PROTECTION_POLICIES,
  isDevelopment,
} from '@/lib/k8s';
import { mockPolicies } from '@/lib/mock-data';

type RouteParams = { params: Promise<{ namespace: string; name: string }> };

export async function GET(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace, name } = await context.params;

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const policy = mockPolicies.find(
      (p) => p.metadata.name === name && p.metadata.namespace === namespace
    );
    if (!policy) {
      return NextResponse.json(
        { error: 'TrafficProtectionPolicy not found' },
        { status: 404 }
      );
    }
    return NextResponse.json(policy);
  }

  try {
    const response = await customObjectsApi.getNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      TRAFFIC_PROTECTION_POLICIES,
      name
    );
    return NextResponse.json(response.body);
  } catch (error: unknown) {
    const err = error as { response?: { statusCode?: number } };
    if (err.response?.statusCode === 404) {
      return NextResponse.json(
        { error: 'TrafficProtectionPolicy not found' },
        { status: 404 }
      );
    }
    console.error('Failed to get TrafficProtectionPolicy:', error);
    return NextResponse.json(
      { error: 'Failed to get TrafficProtectionPolicy' },
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
      TRAFFIC_PROTECTION_POLICIES,
      name,
      body
    );
    return NextResponse.json(response.body);
  } catch (error) {
    console.error('Failed to update TrafficProtectionPolicy:', error);
    return NextResponse.json(
      { error: 'Failed to update TrafficProtectionPolicy' },
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
      TRAFFIC_PROTECTION_POLICIES,
      name
    );
    return new NextResponse(null, { status: 204 });
  } catch (error) {
    console.error('Failed to delete TrafficProtectionPolicy:', error);
    return NextResponse.json(
      { error: 'Failed to delete TrafficProtectionPolicy' },
      { status: 500 }
    );
  }
}

import { NextResponse } from 'next/server';
import { coreV1Api, isDevelopment } from '@/lib/k8s';
import { mockNamespaces } from '@/lib/mock-data';

export async function GET() {
  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    return NextResponse.json({ items: mockNamespaces });
  }

  try {
    const response = await coreV1Api.listNamespace();
    const items = response.body.items.map((ns) => ({
      name: ns.metadata?.name || '',
      creationTimestamp: ns.metadata?.creationTimestamp?.toISOString() || '',
    }));
    return NextResponse.json({ items });
  } catch (error) {
    console.error('Failed to list namespaces:', error);
    return NextResponse.json(
      { error: 'Failed to list namespaces' },
      { status: 500 }
    );
  }
}

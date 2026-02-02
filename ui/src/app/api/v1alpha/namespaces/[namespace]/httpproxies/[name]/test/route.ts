import { NextRequest, NextResponse } from 'next/server';
import {
  customObjectsApi,
  NETWORKING_GROUP,
  V1ALPHA,
  HTTPPROXIES,
  isDevelopment,
} from '@/lib/k8s';
import { mockHTTPProxies } from '@/lib/mock-data';
import type { HTTPProxy, TestProxyRequest, TestProxyResponse } from '@/api/types';

type RouteParams = { params: Promise<{ namespace: string; name: string }> };

export async function POST(
  request: NextRequest,
  context: RouteParams
) {
  const { namespace, name } = await context.params;
  const testRequest: TestProxyRequest = await request.json();

  // Validate request
  if (!testRequest.method) {
    return NextResponse.json(
      { error: { code: 400, message: 'method is required' } },
      { status: 400 }
    );
  }

  // Use mock data in development without cluster
  if (isDevelopment() && process.env.NEXT_PUBLIC_USE_MOCK_DATA === 'true') {
    const proxy = mockHTTPProxies.find(
      (p) => p.metadata.name === name && p.metadata.namespace === namespace
    );
    if (!proxy) {
      return NextResponse.json(
        { error: { code: 404, message: 'HTTPProxy not found' } },
        { status: 404 }
      );
    }

    // Return mock response
    const mockResponse: TestProxyResponse = {
      statusCode: 200,
      statusText: 'OK',
      headers: {
        'content-type': 'application/json',
        'x-request-id': 'mock-' + Date.now(),
      },
      body: JSON.stringify({
        message: 'Mock response from development mode',
        method: testRequest.method,
        path: testRequest.path,
      }, null, 2),
      latencyMs: Math.floor(Math.random() * 200) + 50,
      timestamp: new Date().toISOString(),
    };
    return NextResponse.json(mockResponse);
  }

  try {
    // Get the HTTPProxy to find its addresses
    const proxyResponse = await customObjectsApi.getNamespacedCustomObject(
      NETWORKING_GROUP,
      V1ALPHA,
      namespace,
      HTTPPROXIES,
      name
    );
    const proxy = proxyResponse.body as HTTPProxy;

    // Find the proxy address to use
    const addresses = proxy.status?.addresses || [];
    const hostnames = proxy.status?.hostnames || proxy.spec.hostnames || [];

    // Prefer hostname addresses, fall back to IP addresses
    let targetHost: string | undefined;

    // First try to find a Hostname type address
    const hostnameAddr = addresses.find(a => a.type === 'Hostname');
    if (hostnameAddr) {
      targetHost = hostnameAddr.value;
    } else if (hostnames.length > 0) {
      // Use the first hostname from spec/status
      targetHost = hostnames[0];
    } else {
      // Fall back to IP address
      const ipAddr = addresses.find(a => a.type === 'IPAddress');
      if (ipAddr) {
        targetHost = ipAddr.value;
      }
    }

    if (!targetHost) {
      return NextResponse.json(
        { error: { code: 400, message: 'No address available for HTTPProxy' } },
        { status: 400 }
      );
    }

    // Build the target URL
    const path = testRequest.path || '/';
    const url = `https://${targetHost}${path.startsWith('/') ? path : '/' + path}`;

    // Prepare headers
    const headers: Record<string, string> = {
      'Host': hostnames[0] || targetHost,
      ...testRequest.headers,
    };

    // Make the request
    const startTime = Date.now();

    const fetchOptions: RequestInit = {
      method: testRequest.method,
      headers,
      // Skip TLS verification for self-signed certs in test environments
      // @ts-expect-error - Node.js fetch extension
      rejectUnauthorized: false,
    };

    // Add body for methods that support it
    if (testRequest.body && ['POST', 'PUT', 'PATCH'].includes(testRequest.method)) {
      fetchOptions.body = testRequest.body;
    }

    let response: Response;
    try {
      // Use a custom agent for Node.js to skip TLS verification
      const https = await import('https');
      const agent = new https.Agent({ rejectUnauthorized: false });

      response = await fetch(url, {
        ...fetchOptions,
        // @ts-expect-error - Node.js fetch extension for custom agent
        agent,
      });
    } catch (fetchError) {
      // If the agent approach doesn't work, try without it
      response = await fetch(url, fetchOptions);
    }

    const latencyMs = Date.now() - startTime;

    // Read response body
    const contentType = response.headers.get('content-type') || '';
    let body: string;
    if (contentType.includes('application/json')) {
      try {
        const json = await response.json();
        body = JSON.stringify(json, null, 2);
      } catch {
        body = await response.text();
      }
    } else {
      body = await response.text();
    }

    // Collect response headers
    const responseHeaders: Record<string, string> = {};
    response.headers.forEach((value, key) => {
      responseHeaders[key] = value;
    });

    const testResponse: TestProxyResponse = {
      statusCode: response.status,
      statusText: response.statusText,
      headers: responseHeaders,
      body,
      latencyMs,
      timestamp: new Date().toISOString(),
    };

    return NextResponse.json(testResponse);
  } catch (error: unknown) {
    console.error('Failed to test HTTPProxy:', error);

    const err = error as { response?: { statusCode?: number }; message?: string };
    if (err.response?.statusCode === 404) {
      return NextResponse.json(
        { error: { code: 404, message: 'HTTPProxy not found' } },
        { status: 404 }
      );
    }

    // Return error as a test response so the UI can display it
    const testResponse: TestProxyResponse = {
      statusCode: 0,
      statusText: 'Error',
      headers: {},
      body: '',
      latencyMs: 0,
      timestamp: new Date().toISOString(),
      error: err.message || 'Failed to send request to proxy',
    };

    return NextResponse.json(testResponse);
  }
}

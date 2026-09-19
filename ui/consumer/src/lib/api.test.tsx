import { useNetworkInterfaces, useNetworkServices, useSubnets } from './api';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

function wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe('useSubnets', () => {
  it('fetches subnets scoped to the network via a label selector', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ items: [] }) });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const { result } = renderHook(() => useSubnets('demo-project', 'my-network'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const [url] = fetchMock.mock.calls[0] as [string];
    expect(url).toContain('/subnets?labelSelector=');
    expect(url).toContain(encodeURIComponent('networking.datumapis.com/network=my-network'));
  });
});

describe('useNetworkInterfaces', () => {
  it('fetches every interface in the namespace and filters client-side by network name', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        items: [
          { metadata: { name: 'a' }, spec: { network: { name: 'my-network' } } },
          { metadata: { name: 'b' }, spec: { network: { name: 'other-network' } } },
        ],
      }),
    });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const { result } = renderHook(() => useNetworkInterfaces('demo-project', 'my-network'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const [url] = fetchMock.mock.calls[0] as [string];
    expect(url).toContain('/networkinterfaces?limit=100');
    expect(url).not.toContain('labelSelector');
    expect(result.current.data?.map((i) => i.name)).toEqual(['a']);
  });
});

describe('useNetworkServices', () => {
  it('fetches every service in the project, with no network filter', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ items: [{ metadata: { name: 'storefront' } }] }),
    });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const { result } = renderHook(() => useNetworkServices('demo-project'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const [url] = fetchMock.mock.calls[0] as [string];
    expect(url).toContain('/networkservices?limit=100');
    expect(url).not.toContain('labelSelector');
    expect(result.current.data?.map((s) => s.name)).toEqual(['storefront']);
  });
});

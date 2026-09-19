import { EnableIPv6Dialog } from './enable-ipv6-dialog';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

function makeNetwork(overrides: Partial<Network> = {}): Network {
  return {
    uid: 'default',
    name: 'default',
    resourceVersion: '123',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    readyStatus: 'False',
    readyReason: 'IPv6Required',
    ipFamilies: ['IPv4'],
    conditions: [],
    ...overrides,
  };
}

function renderDialog(network: Network) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <EnableIPv6Dialog projectId="demo-project" network={network} />
    </QueryClientProvider>
  );
}

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

describe('EnableIPv6Dialog', () => {
  it('opens a confirmation dialog naming the network', () => {
    renderDialog(makeNetwork());
    fireEvent.click(screen.getByRole('button', { name: 'Enable IPv6' }));
    expect(screen.getByText(/Enable IPv6 on this network/)).toBeInTheDocument();
    expect(screen.getByText(/Adds IPv6 to default's address families/)).toBeInTheDocument();
  });

  it('PATCHes the network with IPv6 added to ipFamilies on confirm', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    renderDialog(makeNetwork());
    fireEvent.click(screen.getByRole('button', { name: 'Enable IPv6' }));
    const buttons = await screen.findAllByRole('button', { name: 'Enable IPv6' });
    fireEvent.click(buttons[buttons.length - 1]);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain('/networks/default');
    expect(init.method).toBe('PATCH');
    const body = JSON.parse(init.body as string);
    expect(body.spec.ipFamilies).toEqual(['IPv4', 'IPv6']);
    expect(body.metadata.resourceVersion).toBe('123');
  });

  it('shows an error toast and keeps the dialog open on failure', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({ ok: false, status: 403 }) as unknown as typeof fetch;

    renderDialog(makeNetwork());
    fireEvent.click(screen.getByRole('button', { name: 'Enable IPv6' }));
    const buttons = await screen.findAllByRole('button', { name: 'Enable IPv6' });
    fireEvent.click(buttons[buttons.length - 1]);

    await waitFor(() => expect(screen.getByText(/Enable IPv6 on this network/)).toBeInTheDocument());
  });
});

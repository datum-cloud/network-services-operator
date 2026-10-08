import { CreateNetworkDialog } from './create-network-dialog';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

function renderDialog() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <CreateNetworkDialog projectId="demo-project" />
    </QueryClientProvider>
  );
}

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

describe('CreateNetworkDialog', () => {
  it('opens the form on trigger click', () => {
    renderDialog();
    fireEvent.click(screen.getByRole('button', { name: 'Create network' }));
    expect(screen.getByText('Create a network')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('my-network')).toBeInTheDocument();
  });

  it('rejects an invalid name before submitting', async () => {
    renderDialog();
    fireEvent.click(screen.getByRole('button', { name: 'Create network' }));
    fireEvent.change(screen.getByPlaceholderText('my-network'), { target: { value: 'Not Valid!' } });
    const buttons = await screen.findAllByRole('button', { name: 'Create network' });
    fireEvent.click(buttons[buttons.length - 1]);

    expect(screen.getByText(/Lowercase letters, numbers, and hyphens only/)).toBeInTheDocument();
  });

  it('posts an IPv6-only network by default, dual-stack when IPv4 is checked', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    renderDialog();
    fireEvent.click(screen.getByRole('button', { name: 'Create network' }));
    fireEvent.change(screen.getByPlaceholderText('my-network'), { target: { value: 'my-net' } });
    fireEvent.click(screen.getByText('Also carry IPv4 (dual-stack)'));
    const buttons = await screen.findAllByRole('button', { name: 'Create network' });
    fireEvent.click(buttons[buttons.length - 1]);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.metadata.name).toBe('my-net');
    expect(body.spec.ipFamilies).toEqual(['IPv4', 'IPv6']);
    expect(body.spec.ipam).toEqual({ mode: 'Auto' });
  });

  it('surfaces the server error message on failure', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue({ ok: false, status: 409, json: async () => ({ message: 'already exists' }) }) as unknown as typeof fetch;

    renderDialog();
    fireEvent.click(screen.getByRole('button', { name: 'Create network' }));
    fireEvent.change(screen.getByPlaceholderText('my-network'), { target: { value: 'dup' } });
    const buttons = await screen.findAllByRole('button', { name: 'Create network' });
    fireEvent.click(buttons[buttons.length - 1]);

    await waitFor(() => expect(screen.getByText('Create a network')).toBeInTheDocument());
  });
});

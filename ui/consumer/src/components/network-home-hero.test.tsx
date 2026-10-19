import NetworkHomeHero from './network-home-hero';
import * as api from '../lib/api';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetwork: vi.fn() };
});

const useNetworkMock = vi.mocked(api.useNetwork);

function networkResult(network: Network | undefined) {
  return { data: network, isLoading: !network, error: null } as unknown as ReturnType<typeof api.useNetwork>;
}

function makeNetwork(name: string, readyStatus: Network['readyStatus'], readyReason?: string): Network {
  return {
    uid: name,
    name,
    createdAt: new Date('2026-01-01T00:00:00Z'),
    readyStatus,
    readyReason,
    ipFamilies: ['IPv6'],
    conditions: [],
  };
}

function renderHero() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/project/demo-project']}>
        <Routes>
          <Route path="/project/:projectId" element={<NetworkHomeHero />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

const originalFetch = globalThis.fetch;

beforeEach(() => {
  useNetworkMock.mockReturnValue(networkResult(undefined));
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

describe('NetworkHomeHero', () => {
  it('starts with an empty name and lists what attaches', () => {
    renderHero();
    expect(screen.getByText('Create your private network')).toBeInTheDocument();
    expect(screen.getByLabelText(/Network name/)).toHaveValue('');
    expect(screen.getByPlaceholderText('e.g. production')).toBeInTheDocument();
    for (const title of ['Network', 'Workloads', 'Load balancers', 'Connectors']) {
      expect(screen.getByText(title)).toBeInTheDocument();
    }
    expect(screen.getByText('datumctl compute deploy')).toBeInTheDocument();
  });

  it('asks for a name before submitting', async () => {
    const fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    renderHero();
    fireEvent.click(screen.getByRole('button', { name: /Create network/ }));

    expect(await screen.findByText('Name is required.')).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('rejects an invalid name before submitting', async () => {
    const fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    renderHero();
    fireEvent.change(screen.getByLabelText(/Network name/), { target: { value: 'Not Valid!' } });
    fireEvent.click(screen.getByRole('button', { name: /Create network/ }));

    expect(await screen.findByText(/Lowercase letters, numbers, and hyphens only/)).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('shows the creating state, then waits for the network to be ready', async () => {
    let resolvePost: (value: unknown) => void = () => {};
    const fetchMock = vi.fn().mockReturnValue(new Promise((resolve) => (resolvePost = resolve)));
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    renderHero();
    fireEvent.change(screen.getByLabelText(/Network name/), { target: { value: 'default' } });
    fireEvent.click(screen.getByRole('button', { name: /Create network/ }));

    expect(await screen.findByRole('button', { name: /Creating default\.\.\./ })).toBeDisabled();
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.metadata.name).toBe('default');
    expect(body.spec.ipFamilies).toEqual(['IPv6']);
    expect(body.spec.mtu).toBeUndefined();

    resolvePost({ ok: true, json: async () => ({}) });
    expect(await screen.findByText('Waiting for default to be ready...')).toBeInTheDocument();
    expect(useNetworkMock).toHaveBeenLastCalledWith('demo-project', 'default');
  });

  it('shows the ready state once the network reports Ready', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }) as unknown as typeof fetch;
    useNetworkMock.mockImplementation((_projectId, name) =>
      networkResult(name ? makeNetwork(name, 'True') : undefined)
    );

    renderHero();
    fireEvent.change(screen.getByLabelText(/Network name/), { target: { value: 'prod' } });
    fireEvent.click(screen.getByRole('button', { name: /Create network/ }));

    expect(await screen.findByText('Network prod is ready')).toBeInTheDocument();
    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('shows the API error inline and keeps the form editable', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      json: async () => ({ message: 'networks "default" already exists' }),
    }) as unknown as typeof fetch;

    renderHero();
    fireEvent.change(screen.getByLabelText(/Network name/), { target: { value: 'default' } });
    fireEvent.click(screen.getByRole('button', { name: /Create network/ }));

    expect(await screen.findByTestId('networking-plugin-home-hero-error')).toHaveTextContent(
      'networks "default" already exists'
    );
    const input = screen.getByLabelText(/Network name/);
    expect(input).toBeEnabled();
    expect(screen.getByRole('button', { name: /Create network/ })).toBeEnabled();
    fireEvent.change(input, { target: { value: 'other' } });
    await waitFor(() => expect(input).toHaveValue('other'));
  });

  it('explains a permission error in plain words', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 403,
      json: async () => ({ message: 'forbidden' }),
    }) as unknown as typeof fetch;

    renderHero();
    fireEvent.change(screen.getByLabelText(/Network name/), { target: { value: 'prod' } });
    fireEvent.click(screen.getByRole('button', { name: /Create network/ }));

    expect(await screen.findByTestId('networking-plugin-home-hero-error')).toHaveTextContent(
      "You don't have permission to create networks in this project."
    );
  });
});

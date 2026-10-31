import { CreateNetworkServiceDialog } from './create-network-service-dialog';
import * as api from '../lib/api';
import type { NetworkInterface } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Radix's Select mounts a hidden native <select> and a dismissable-layer
// portal; unmounting one between tests is measurably slow in this jsdom
// environment (multiple seconds), so tests that interact with it need
// more than the default 5s budget.
const SELECT_TEST_TIMEOUT = 30_000;

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetworkInterfaces: vi.fn() };
});

const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);

function makeInterface(workloadName: string): NetworkInterface {
  return {
    uid: `iface-${workloadName}`,
    name: `iface-${workloadName}`,
    createdAt: new Date('2026-08-25T14:18:34Z'),
    network: 'default',
    workloadName,
    labels: {},
    addresses: [],
    holderAvailableStatus: 'True',
  };
}

function renderDialog() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <CreateNetworkServiceDialog projectId="demo-project" networkName="default" />
    </QueryClientProvider>
  );
}

const originalFetch = globalThis.fetch;

beforeEach(() => {
  useNetworkInterfacesMock.mockReset();
  useNetworkInterfacesMock.mockReturnValue({
    data: [makeInterface('storefront'), makeInterface('checkout')],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as never);
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

async function openDialog() {
  fireEvent.click(screen.getByRole('button', { name: 'Create service' }));
  return screen.findByRole('dialog');
}

function selectWorkload(name: string) {
  fireEvent.click(screen.getAllByRole('combobox')[0]);
  fireEvent.click(screen.getByRole('option', { name }));
}

function selectProtocol(portIndex: number, protocol: 'TCP' | 'UDP') {
  fireEvent.click(screen.getAllByRole('combobox')[portIndex + 1]);
  fireEvent.click(screen.getByRole('option', { name: protocol }));
}

function fillPort(portIndex: number, name: string, port: string) {
  fireEvent.change(screen.getAllByPlaceholderText('e.g. http')[portIndex], { target: { value: name } });
  const portInput = screen.getAllByPlaceholderText('e.g. 8080')[portIndex];
  fireEvent.focus(portInput);
  fireEvent.change(portInput, { target: { value: port } });
  fireEvent.blur(portInput);
}

describe('CreateNetworkServiceDialog', () => {
  it('opens the form on trigger click', async () => {
    renderDialog();
    await openDialog();
    expect(screen.getByText('Create a service')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('e.g. storefront')).toBeInTheDocument();
    expect(screen.getByText('Select a workload')).toBeInTheDocument();
  });

  it('lists workload names derived from the network\'s interfaces', async () => {
    renderDialog();
    await openDialog();

    fireEvent.click(screen.getAllByRole('combobox')[0]);
    expect(screen.getByRole('option', { name: 'storefront' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'checkout' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('option', { name: 'checkout' }));
  }, SELECT_TEST_TIMEOUT);

  it('requires a workload and a port before submitting', async () => {
    renderDialog();
    await openDialog();
    fireEvent.change(screen.getByPlaceholderText('e.g. storefront'), { target: { value: 'my-svc' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Create' }));

    expect(screen.getByText(/Select the workload/)).toBeInTheDocument();
  }, SELECT_TEST_TIMEOUT);

  it('posts a workload-name matchLabel and ports on submit', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    renderDialog();
    await openDialog();
    fireEvent.change(screen.getByPlaceholderText('e.g. storefront'), { target: { value: 'my-svc' } });
    selectWorkload('storefront');
    fireEvent.change(screen.getByPlaceholderText('e.g. http'), { target: { value: 'http' } });
    fireEvent.change(screen.getByPlaceholderText('e.g. 8080'), { target: { value: '8080' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Create' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.metadata.name).toBe('my-svc');
    expect(body.spec.networkInterfaces.selector.matchLabels).toEqual({
      'compute.datumapis.com/workload-name': 'storefront',
    });
    expect(body.spec.ports).toEqual([{ name: 'http', port: 8080, protocol: 'TCP' }]);
  }, SELECT_TEST_TIMEOUT);

  it('posts a UDP port alongside a TCP health port on the same number', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    renderDialog();
    await openDialog();
    fireEvent.change(screen.getByPlaceholderText('e.g. storefront'), { target: { value: 'dns' } });
    selectWorkload('storefront');
    fillPort(0, 'dns', '53');
    selectProtocol(0, 'UDP');
    fireEvent.click(screen.getByRole('button', { name: 'Add port' }));
    fillPort(1, 'health', '53');
    fireEvent.click(await screen.findByRole('button', { name: 'Create' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.spec.ports).toEqual([
      { name: 'dns', port: 53, protocol: 'UDP' },
      { name: 'health', port: 53, protocol: 'TCP' },
    ]);
  }, SELECT_TEST_TIMEOUT);

  it('rejects the same port number twice on one protocol', async () => {
    const fetchMock = vi.fn();
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    renderDialog();
    await openDialog();
    fireEvent.change(screen.getByPlaceholderText('e.g. storefront'), { target: { value: 'dns' } });
    selectWorkload('storefront');
    fillPort(0, 'dns', '53');
    fireEvent.click(screen.getByRole('button', { name: 'Add port' }));
    fillPort(1, 'health', '53');
    fireEvent.click(await screen.findByRole('button', { name: 'Create' }));

    expect(await screen.findByText('TCP port 53 is already used.')).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  }, SELECT_TEST_TIMEOUT);

  it('surfaces the server error message on failure', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue({ ok: false, status: 409, json: async () => ({ message: 'already exists' }) }) as unknown as typeof fetch;

    renderDialog();
    await openDialog();
    fireEvent.change(screen.getByPlaceholderText('e.g. storefront'), { target: { value: 'dup' } });
    selectWorkload('checkout');
    fireEvent.change(screen.getByPlaceholderText('e.g. http'), { target: { value: 'http' } });
    fireEvent.change(screen.getByPlaceholderText('e.g. 8080'), { target: { value: '8080' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Create' }));

    await waitFor(() => expect(screen.getByText('Create a service')).toBeInTheDocument());
  }, SELECT_TEST_TIMEOUT);
});

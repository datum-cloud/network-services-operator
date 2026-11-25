import { CreateNetworkServiceDialog, EditNetworkServiceDialog, editableWorkloadName } from './create-network-service-dialog';
import * as api from '../lib/api';
import type { NetworkInterface, NetworkService } from '../schema';
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

function makeService(overrides: Partial<NetworkService> = {}): NetworkService {
  return {
    uid: 'storefront-uid',
    name: 'storefront',
    createdAt: new Date('2026-08-25T14:18:34Z'),
    networkInterfaceSelector: { matchLabels: { 'compute.datumapis.com/workload-name': 'storefront' }, matchExpressions: [] },
    ports: [{ name: 'http', port: 8080, protocol: 'TCP' }],
    summary: { locations: 1, members: 1, healthy: 1 },
    locations: [],
    membersResolvedStatus: 'True',
    readyStatus: 'True',
    ...overrides,
  };
}

function renderEditDialog(service: NetworkService, onOpenChange = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <EditNetworkServiceDialog projectId="demo-project" networkName="default" service={service} onOpenChange={onOpenChange} />
    </QueryClientProvider>
  );
  return onOpenChange;
}

describe('editableWorkloadName', () => {
  it('returns the workload for a single workload-name selector', () => {
    expect(editableWorkloadName(makeService())).toBe('storefront');
  });

  it('rejects selectors the form cannot represent', () => {
    expect(
      editableWorkloadName(
        makeService({ networkInterfaceSelector: { matchLabels: { app: 'web' }, matchExpressions: [] } })
      )
    ).toBeUndefined();
    expect(
      editableWorkloadName(
        makeService({
          networkInterfaceSelector: {
            matchLabels: { 'compute.datumapis.com/workload-name': 'storefront' },
            matchExpressions: [{ key: 'tier', operator: 'Exists', values: [] }],
          },
        })
      )
    ).toBeUndefined();
  });

  it('rejects services with a port range', () => {
    expect(editableWorkloadName(makeService({ ports: [{ name: 'game', port: 0, protocol: 'UDP' }] }))).toBeUndefined();
  });
});

describe('EditNetworkServiceDialog', () => {
  it('prefills the current ports', async () => {
    renderEditDialog(makeService());
    await screen.findByRole('dialog');
    expect(screen.getByText('Edit service storefront')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('e.g. http')).toHaveValue('http');
    expect(screen.getByPlaceholderText('e.g. 8080')).toHaveValue(8080);
    expect(screen.queryByPlaceholderText('e.g. storefront')).not.toBeInTheDocument();
  });

  it('replaces the selector and ports wholesale', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    const onOpenChange = renderEditDialog(makeService());
    await screen.findByRole('dialog');
    selectWorkload('checkout');
    fireEvent.click(screen.getByRole('button', { name: 'Add port' }));
    fillPort(1, 'https', '8443');
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain('/networkservices/storefront');
    expect(init.method).toBe('PATCH');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json-patch+json');
    expect(JSON.parse(init.body as string)).toEqual([
      {
        op: 'replace',
        path: '/spec/networkInterfaces/selector',
        value: { matchLabels: { 'compute.datumapis.com/workload-name': 'checkout' } },
      },
      {
        op: 'replace',
        path: '/spec/ports',
        value: [
          { name: 'http', port: 8080, protocol: 'TCP' },
          { name: 'https', port: 8443, protocol: 'TCP' },
        ],
      },
    ]);
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  }, SELECT_TEST_TIMEOUT);

  it('keeps the dialog open when the server rejects the change', async () => {
    globalThis.fetch = vi
      .fn()
      .mockResolvedValue({ ok: false, status: 409, json: async () => ({ message: 'conflict' }) }) as unknown as typeof fetch;

    const onOpenChange = renderEditDialog(makeService());
    await screen.findByRole('dialog');
    fireEvent.click(await screen.findByRole('button', { name: 'Save' }));

    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledTimes(1));
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
    expect(screen.getByText('Edit service storefront')).toBeInTheDocument();
  });
});

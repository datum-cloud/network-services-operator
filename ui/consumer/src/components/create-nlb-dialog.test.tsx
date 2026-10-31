import { CreateNlbDialog } from './create-nlb-dialog';
import type { NlbCreatePreset } from './nlb-empty-state';
import * as api from '../lib/api';
import type { Network, NetworkService } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Radix Select is slow to unmount in jsdom; see create-network-service-dialog.test.tsx.
const SELECT_TEST_TIMEOUT = 30_000;

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetworks: vi.fn(), useNetworkServices: vi.fn() };
});

const useNetworksMock = vi.mocked(api.useNetworks);
const useNetworkServicesMock = vi.mocked(api.useNetworkServices);

const network: Network = {
  uid: 'net',
  name: 'default',
  createdAt: new Date('2026-01-01T00:00:00Z'),
  readyStatus: 'True',
  ipFamilies: ['IPv6'],
  conditions: [],
};

function makeService(name: string, ports: NetworkService['ports']): NetworkService {
  return {
    uid: name,
    name,
    createdAt: new Date('2026-01-01T00:00:00Z'),
    networkInterfaceSelector: { matchLabels: {}, matchExpressions: [] },
    ports,
    summary: { locations: 1, members: 1, healthy: 1 },
    locations: [],
    membersResolvedStatus: 'True',
    readyStatus: 'True',
  };
}

const services = [
  makeService('postgres', [{ name: 'pg', port: 5432, protocol: 'TCP' }]),
  makeService('game-server', [
    { name: 'game', port: 7777, protocol: 'UDP' },
    { name: 'health', port: 8080, protocol: 'TCP' },
  ]),
];

function renderDialog(preset?: NlbCreatePreset, onCreated?: (name: string) => void) {
  const onOpenChange = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <CreateNlbDialog
        projectId="demo-project"
        open
        onOpenChange={onOpenChange}
        preset={preset}
        onCreated={onCreated}
      />
    </QueryClientProvider>
  );
  return { onOpenChange };
}

// Comboboxes in form order: network, protocol, backend, backend port, probe port.
function choose(index: number, option: string) {
  fireEvent.click(screen.getAllByRole('combobox')[index]);
  fireEvent.click(screen.getByRole('option', { name: option }));
}

function ok() {
  return { ok: true, status: 201, json: async () => ({}) };
}

function failure(status: number, body: unknown) {
  return { ok: false, status, json: async () => body };
}

function postedBodies(fetchMock: ReturnType<typeof vi.fn>) {
  return fetchMock.mock.calls.map(([url, init]) => ({
    url: url as string,
    body: JSON.parse((init as RequestInit).body as string),
  }));
}

const originalFetch = globalThis.fetch;

beforeEach(() => {
  useNetworksMock.mockReturnValue({ data: [network], isLoading: false, error: null } as never);
  useNetworkServicesMock.mockReturnValue({
    data: services,
    isLoading: false,
    error: null,
  } as never);
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.restoreAllMocks();
});

describe('CreateNlbDialog', () => {
  it(
    'creates a TCP NLB, then its route probing the serving port',
    async () => {
      const fetchMock = vi.fn().mockResolvedValue(ok());
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      const { onOpenChange } = renderDialog({ protocol: 'TCP', port: 5432 });

      fireEvent.change(screen.getByPlaceholderText('e.g. edge-nlb'), { target: { value: 'db' } });
      choose(2, 'postgres');
      choose(3, 'pg · 5432/TCP');
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      const [nlb, route] = postedBodies(fetchMock);
      expect(nlb.url).toMatch(/\/networkloadbalancers$/);
      expect(nlb.body.spec).toEqual({
        networkRef: { name: 'default' },
        exposure: { addresses: [{ name: 'tcp-5432', families: ['IPv6'] }] },
        listeners: [{ name: 'tcp-5432', protocol: 'TCP', port: 5432, addressRefs: ['tcp-5432'] }],
      });
      expect(route.url).toMatch(/\/networkloadbalancerroutes$/);
      expect(route.body.spec.backends).toEqual([
        { networkService: { name: 'postgres', port: 'pg' }, healthCheck: { protocol: 'TCP' } },
      ]);
      await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'creates a UDP NLB whose route probes a named TCP port',
    async () => {
      const fetchMock = vi.fn().mockResolvedValue(ok());
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderDialog({ protocol: 'UDP', port: 7777 });

      fireEvent.change(screen.getByPlaceholderText('e.g. edge-nlb'), {
        target: { value: 'edge-nlb' },
      });
      choose(2, 'game-server');
      // A UDP listener only offers the backend's UDP ports.
      fireEvent.click(screen.getAllByRole('combobox')[3]);
      expect(screen.queryByRole('option', { name: 'health · 8080/TCP' })).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole('option', { name: 'game · 7777/UDP' }));
      choose(4, 'health · 8080/TCP');
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
      const [nlb, route] = postedBodies(fetchMock);
      expect(nlb.body.spec.listeners).toEqual([
        { name: 'udp-7777', protocol: 'UDP', port: 7777, addressRefs: ['udp-7777'] },
      ]);
      expect(route.body).toMatchObject({
        metadata: { name: 'edge-nlb-udp-7777' },
        spec: {
          parentRefs: [{ name: 'edge-nlb', listener: 'udp-7777' }],
          backends: [
            {
              networkService: { name: 'game-server', port: 'game' },
              healthCheck: { protocol: 'TCP', port: 'health' },
            },
          ],
        },
      });
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'shows an API error next to its field and stays open',
    async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        failure(422, {
          message: 'invalid',
          details: {
            causes: [{ field: 'spec.listeners[0].port', message: 'port 5432 is reserved' }],
          },
        })
      );
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      const { onOpenChange } = renderDialog({ protocol: 'TCP', port: 5432 });

      fireEvent.change(screen.getByPlaceholderText('e.g. edge-nlb'), { target: { value: 'db' } });
      choose(2, 'postgres');
      choose(3, 'pg · 5432/TCP');
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));

      expect(await screen.findByText('port 5432 is reserved')).toBeInTheDocument();
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(onOpenChange).not.toHaveBeenCalledWith(false);
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'retries only the route when the NLB was created but its route failed',
    async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(ok())
        .mockResolvedValueOnce(failure(500, { message: 'route webhook unavailable' }))
        .mockResolvedValueOnce(ok());
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      const { onOpenChange } = renderDialog({ protocol: 'TCP', port: 5432 });

      fireEvent.change(screen.getByPlaceholderText('e.g. edge-nlb'), { target: { value: 'db' } });
      choose(2, 'postgres');
      choose(3, 'pg · 5432/TCP');
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));

      expect(
        await screen.findByText(/Created load balancer db, but couldn’t attach its backend/)
      ).toBeInTheDocument();
      expect(screen.getByText('route webhook unavailable')).toBeInTheDocument();
      expect(screen.queryByPlaceholderText('e.g. edge-nlb')).not.toBeInTheDocument();
      expect(screen.getByText('TCP 5432 on network default · created')).toBeInTheDocument();

      fireEvent.click(screen.getByRole('button', { name: 'Retry backend' }));
      await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
      expect(postedBodies(fetchMock).map((r) => r.url.split('/').pop())).toEqual([
        'networkloadbalancers',
        'networkloadbalancerroutes',
        'networkloadbalancerroutes',
      ]);
      await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'needs a probe port before creating a UDP listener',
    async () => {
      const fetchMock = vi.fn();
      globalThis.fetch = fetchMock as unknown as typeof fetch;
      renderDialog({ protocol: 'UDP', port: 7777 });

      fireEvent.change(screen.getByPlaceholderText('e.g. edge-nlb'), {
        target: { value: 'edge-nlb' },
      });
      choose(2, 'game-server');
      choose(3, 'game · 7777/UDP');
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));

      expect(
        await screen.findByText('A UDP listener needs a TCP port on the backend to health check.')
      ).toBeInTheDocument();
      expect(fetchMock).not.toHaveBeenCalled();
    },
    SELECT_TEST_TIMEOUT
  );

  it(
    'hands the new NLB to onCreated, staying open and loading until it navigates',
    async () => {
      globalThis.fetch = vi.fn().mockResolvedValue(ok()) as unknown as typeof fetch;
      const onCreated = vi.fn();
      const { onOpenChange } = renderDialog({ protocol: 'TCP', port: 5432 }, onCreated);

      fireEvent.change(screen.getByPlaceholderText('e.g. edge-nlb'), { target: { value: 'db' } });
      choose(2, 'postgres');
      choose(3, 'pg · 5432/TCP');
      fireEvent.click(screen.getByRole('button', { name: 'Create' }));

      await waitFor(() => expect(onCreated).toHaveBeenCalledWith('db'));
      expect(onOpenChange).not.toHaveBeenCalledWith(false);
      // Still open and loading, so it doesn't flash before the new page replaces it.
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(screen.getByRole('dialog')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Create' })).not.toBeInTheDocument();
    },
    SELECT_TEST_TIMEOUT
  );
});

import { NlbTable, toNlbRows } from './nlb-table';
import * as api from '../lib/api';
import type { NetworkLoadBalancer, NetworkLoadBalancerRoute } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useDeleteNetworkLoadBalancer: vi.fn() };
});

const useDeleteMock = vi.mocked(api.useDeleteNetworkLoadBalancer);
const deleteMock = vi.fn();

beforeEach(() => {
  deleteMock.mockReset().mockResolvedValue(undefined);
  useDeleteMock.mockReset().mockReturnValue({ mutateAsync: deleteMock } as never);
});

function makeNlb(overrides: Partial<NetworkLoadBalancer>): NetworkLoadBalancer {
  return {
    uid: overrides.name ?? 'uid',
    name: 'edge-nlb',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    networkName: 'default',
    listeners: [{ name: 'udp-7777', protocol: 'UDP', port: 7777 }],
    endpoints: [],
    readyStatus: 'True',
    ...overrides,
  };
}

function makeRoute(
  name: string,
  parents: string[],
  backendNames: string[]
): NetworkLoadBalancerRoute {
  return {
    uid: name,
    name,
    resourceVersion: '1',
    parents: parents.map((p) => ({ name: p, listener: 'udp-7777' })),
    backendNames,
    backends: backendNames.map((b) => ({ name: b, port: 'p' })),
    parentStatuses: [],
  };
}

function renderTable(nlbs: NetworkLoadBalancer[], routes: NetworkLoadBalancerRoute[] | undefined) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <NlbTable
          nlbs={nlbs}
          routes={routes}
          projectId="demo-project"
          nlbHref={(name) => `/nlb/${name}`}
        />
      </QueryClientProvider>
    </MemoryRouter>
  );
}

function rowFor(name: string): HTMLElement {
  return screen.getByText(name).closest('tr') as HTMLElement;
}

describe('toNlbRows', () => {
  it('joins each NLB with the routes that name it and their backends', () => {
    const rows = toNlbRows(
      [makeNlb({ name: 'a' }), makeNlb({ name: 'b' })],
      [
        makeRoute('a-udp', ['a'], ['game-server']),
        makeRoute('shared', ['a', 'b'], ['dns', 'game-server']),
      ]
    );
    expect(rows.map((r) => [r.nlb.name, r.routes?.map((x) => x.name), r.backendNames])).toEqual([
      ['a', ['a-udp', 'shared'], ['dns', 'game-server']],
      ['b', ['shared'], ['dns', 'game-server']],
    ]);
  });

  it('leaves routes and backends unknown until routes load', () => {
    expect(toNlbRows([makeNlb({})], undefined)[0]).toEqual({ nlb: makeNlb({}) });
  });
});

describe('planNlbDelete', () => {
  it('deletes routes only this NLB uses and detaches shared ones', () => {
    const plan = api.planNlbDelete('a', [
      makeRoute('a-only', ['a'], ['x']),
      makeRoute('shared', ['a', 'b'], ['y']),
      makeRoute('b-only', ['b'], ['z']),
    ]);
    expect(plan.deleteRoutes.map((r) => r.name)).toEqual(['a-only']);
    expect(plan.detachRoutes.map((d) => [d.route.name, d.parents.map((p) => p.name)])).toEqual([
      ['shared', ['b']],
    ]);
  });
});

describe('NlbTable', () => {
  it('shows listeners, backends and a readable status for each NLB', () => {
    renderTable(
      [
        makeNlb({ name: 'game' }),
        makeNlb({
          name: 'pending',
          readyStatus: 'False',
          readyReason: 'AllocationPending',
          listeners: [{ name: 'tcp-80', protocol: 'TCP', port: 80 }],
        }),
      ],
      [makeRoute('game-udp', ['game'], ['game-server'])]
    );

    const game = within(rowFor('game'));
    expect(game.getByText('UDP 7777')).toBeInTheDocument();
    expect(game.getByText('game-server')).toBeInTheDocument();
    expect(game.getByText('Ready')).toBeInTheDocument();

    const pending = within(rowFor('pending'));
    expect(pending.getByText('TCP 80')).toBeInTheDocument();
    expect(pending.getByText('No route')).toBeInTheDocument();
    expect(pending.getByText('Address pending')).toBeInTheDocument();
  });

  it('links each name to its overview', () => {
    renderTable([makeNlb({ name: 'game' })], []);
    expect(screen.getByRole('link', { name: 'game' })).toHaveAttribute('href', '/nlb/game');
  });

  it('shows backends as unknown and hides delete until routes load', () => {
    renderTable([makeNlb({ name: 'game' })], undefined);
    const row = within(rowFor('game'));
    expect(row.getByText('—')).toBeInTheDocument();
    fireEvent.click(row.getByRole('button', { name: 'Actions' }));
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument();
  });

  it('says which routes go with the NLB and which stay, then deletes once confirmed', async () => {
    const routes = [
      makeRoute('game-udp', ['game'], ['game-server']),
      makeRoute('shared', ['game', 'other'], ['dns']),
    ];
    renderTable([makeNlb({ name: 'game' })], routes);

    fireEvent.click(within(rowFor('game')).getByRole('button', { name: 'Actions' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    const dialog = within(await screen.findByRole('dialog'));
    expect(dialog.getByText(/It also deletes its route game-udp\./)).toBeInTheDocument();
    expect(
      dialog.getByText(/shared also serves another load balancer, so it stays/)
    ).toBeInTheDocument();
    fireEvent.change(dialog.getByPlaceholderText('Type in here...'), {
      target: { value: 'DELETE' },
    });
    fireEvent.click(dialog.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(deleteMock).toHaveBeenCalledWith({ name: 'game', routes }));
  });
});

describe('useDeleteNetworkLoadBalancer requests', () => {
  it('detaches shared routes, deletes its own, then deletes the NLB', async () => {
    const actual = await vi.importActual<typeof api>('../lib/api');
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    const originalFetch = globalThis.fetch;
    globalThis.fetch = fetchMock as unknown as typeof fetch;

    let mutateAsync:
      | ((input: { name: string; routes: NetworkLoadBalancerRoute[] }) => Promise<void>)
      | undefined;
    function Probe() {
      mutateAsync = actual.useDeleteNetworkLoadBalancer('demo-project').mutateAsync;
      return null;
    }
    const client = new QueryClient();
    render(
      <QueryClientProvider client={client}>
        <Probe />
      </QueryClientProvider>
    );

    await mutateAsync!({
      name: 'game',
      routes: [
        makeRoute('game-udp', ['game'], ['x']),
        makeRoute('shared', ['game', 'other'], ['y']),
      ],
    });

    const calls = fetchMock.mock.calls.map(([url, init]) => [
      (init as RequestInit).method,
      (url as string).split('/default/')[1],
      (init as RequestInit).body ? JSON.parse((init as RequestInit).body as string) : undefined,
    ]);
    expect(calls).toEqual([
      [
        'PATCH',
        'networkloadbalancerroutes/shared',
        {
          metadata: { resourceVersion: '1' },
          spec: { parentRefs: [{ name: 'other', listener: 'udp-7777' }] },
        },
      ],
      ['DELETE', 'networkloadbalancerroutes/game-udp', undefined],
      ['DELETE', 'networkloadbalancers/game', undefined],
    ]);
    globalThis.fetch = originalFetch;
  });
});

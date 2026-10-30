import NlbList from './nlb-list';
import { ApiError } from '../lib/api';
import * as api from '../lib/api';
import type { NetworkLoadBalancer, NetworkService } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useNetworkLoadBalancers: vi.fn(), useNetworkServices: vi.fn() };
});

const useNetworkLoadBalancersMock = vi.mocked(api.useNetworkLoadBalancers);
const useNetworkServicesMock = vi.mocked(api.useNetworkServices);

function LocationProbe() {
  const location = useLocation();
  return <p data-testid="location-search">{location.search}</p>;
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/project/demo-project/services/networking/nlb']}>
        <Routes>
          <Route
            path="/project/:projectId/services/:serviceSlug/nlb"
            element={
              <>
                <NlbList />
                <LocationProbe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  );
}

function mockNlbs(result: {
  data?: NetworkLoadBalancer[];
  isLoading?: boolean;
  error?: Error | null;
}) {
  useNetworkLoadBalancersMock.mockReturnValue({
    data: result.data,
    isLoading: result.isLoading ?? false,
    error: result.error ?? null,
    refetch: vi.fn(),
  } as never);
}

function mockServices(services: NetworkService[] | undefined) {
  useNetworkServicesMock.mockReturnValue({
    data: services,
    isLoading: services === undefined,
    error: null,
  } as never);
}

function makeService(name: string, ready = true): NetworkService {
  return {
    uid: name,
    name,
    createdAt: new Date('2026-01-01T00:00:00Z'),
    networkInterfaceSelector: { matchLabels: {}, matchExpressions: [] },
    ports: [],
    summary: { locations: 1, members: 1, healthy: ready ? 1 : 0 },
    locations: [],
    membersResolvedStatus: 'True',
    readyStatus: ready ? 'True' : 'False',
  };
}

function makeNlb(name: string): NetworkLoadBalancer {
  return {
    uid: name,
    name,
    createdAt: new Date('2026-01-01T00:00:00Z'),
    networkName: 'default',
    listeners: [{ name: 'pg', protocol: 'TCP', port: 5432 }],
    readyStatus: 'True',
  };
}

beforeEach(() => {
  useNetworkLoadBalancersMock.mockReset();
  useNetworkServicesMock.mockReset();
  mockServices([]);
});

describe('NlbList state machine', () => {
  it('shows the loading skeleton while fetching', () => {
    mockNlbs({ isLoading: true });
    renderPage();
    expect(screen.getByTestId('networking-plugin-loading')).toBeInTheDocument();
  });

  it('shows the restricted state on a 403 ApiError', () => {
    mockNlbs({ error: new ApiError(403, 'forbidden') });
    renderPage();
    expect(screen.getByTestId('networking-plugin-restricted')).toBeInTheDocument();
  });

  it('shows a retryable error state on a non-403 error', () => {
    mockNlbs({ error: new ApiError(500, 'boom') });
    renderPage();
    expect(screen.getByTestId('networking-plugin-error')).toBeInTheDocument();
  });

  it('lists NLBs instead of the empty state once there are any', () => {
    mockNlbs({ data: [makeNlb('edge-nlb')] });
    renderPage();
    expect(screen.queryByTestId('networking-plugin-nlb-empty')).not.toBeInTheDocument();
    expect(screen.getByText('edge-nlb')).toBeInTheDocument();
    expect(screen.getByText('TCP 5432')).toBeInTheDocument();
  });
});

describe('NLB empty state', () => {
  beforeEach(() => mockNlbs({ data: [] }));

  it('shows the pitch, the datumctl command and the NLB or ALB comparison', () => {
    renderPage();
    expect(screen.getByTestId('networking-plugin-nlb-empty')).toBeInTheDocument();
    expect(screen.getByText('Forward TCP and UDP straight to a workload')).toBeInTheDocument();
    expect(screen.getByText('datumctl apply -f nlb.yaml')).toBeInTheDocument();
    expect(screen.getByText('Use an NLB when')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Go to ALB/ })).toHaveAttribute(
      'href',
      '/project/demo-project/alb'
    );
  });

  it('opens create with nothing filled in from the main button', () => {
    renderPage();
    fireEvent.click(screen.getByRole('button', { name: /Create load balancer/ }));
    expect(screen.getByTestId('location-search')).toHaveTextContent(/^\?create=1$/);
  });

  it.each([
    ['database', 'TCP', '5432'],
    ['game-server', 'UDP', '7777'],
    ['dns', 'UDP', '53'],
    ['any-tcp', 'TCP', '443'],
  ])('opens create with the %s starter protocol and port filled in', (id, protocol, port) => {
    renderPage();
    fireEvent.click(screen.getByTestId(`networking-plugin-nlb-starter-${id}`));
    expect(screen.getByTestId('location-search')).toHaveTextContent(
      `?create=1&protocol=${protocol}&port=${port}`
    );
  });

  it('links to Compute when the project has no backends', () => {
    mockServices([]);
    renderPage();
    expect(screen.getByTestId('networking-plugin-nlb-backends')).toHaveTextContent(
      'No backends yet.'
    );
    expect(screen.getByRole('link', { name: 'Deploy a workload' })).toHaveAttribute(
      'href',
      '/project/demo-project/services/compute-datumapis-com'
    );
  });

  it('names a single ready backend', () => {
    mockServices([makeService('guestbook')]);
    renderPage();
    expect(screen.getByTestId('networking-plugin-nlb-backends')).toHaveTextContent(
      '1 backend ready to use: guestbook · IPv4 coming soon'
    );
  });

  it('names the first three ready backends and counts the rest', () => {
    mockServices([
      makeService('a'),
      makeService('b'),
      makeService('not-ready', false),
      makeService('c'),
      makeService('d'),
      makeService('e'),
    ]);
    renderPage();
    expect(screen.getByTestId('networking-plugin-nlb-backends')).toHaveTextContent(
      '5 backends ready to use: a, b, c +2 more'
    );
  });

  it('says when backends exist but none is ready', () => {
    mockServices([makeService('a', false), makeService('b', false)]);
    renderPage();
    expect(screen.getByTestId('networking-plugin-nlb-backends')).toHaveTextContent(
      '2 backends, none ready yet'
    );
  });

  it('leaves out the backend line until services load', () => {
    mockServices(undefined);
    renderPage();
    expect(screen.queryByTestId('networking-plugin-nlb-backends')).not.toBeInTheDocument();
    expect(screen.getByTestId('networking-plugin-nlb-empty')).toBeInTheDocument();
  });
});

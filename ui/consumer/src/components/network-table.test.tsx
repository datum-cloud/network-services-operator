import { NetworkTable } from './network-table';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { describe, expect, it } from 'vitest';

function LocationDisplay() {
  return <div data-testid="location-display">{useLocation().pathname}</div>;
}

function renderTable(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter initialEntries={['/project/demo-project/services/networking/networks']}>
      <QueryClientProvider client={client}>{ui}</QueryClientProvider>
      <LocationDisplay />
    </MemoryRouter>
  );
}

function makeNetwork(overrides: Partial<Network>): Network {
  return {
    uid: overrides.name ?? 'uid',
    name: 'default',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    readyStatus: 'True',
    ipFamilies: ['IPv6'],
    conditions: [],
    ...overrides,
  };
}

describe('NetworkTable', () => {
  it('renders zero rows', () => {
    renderTable(<NetworkTable networks={[]} />);
    expect(screen.queryAllByTestId('network-table-row')).toHaveLength(0);
  });

  it('renders one row', () => {
    renderTable(<NetworkTable networks={[makeNetwork({ name: 'default' })]} />);
    const rows = screen.getAllByTestId('network-table-row');
    expect(rows).toHaveLength(1);
    expect(within(rows[0]).getByText('default')).toBeInTheDocument();
  });

  it('renders N rows', () => {
    const networks = [
      makeNetwork({ name: 'zeta', uid: '1' }),
      makeNetwork({ name: 'alpha', uid: '2' }),
      makeNetwork({ name: 'mu', uid: '3' }),
    ];
    renderTable(<NetworkTable networks={networks} />);
    expect(screen.getAllByTestId('network-table-row')).toHaveLength(3);
  });

  it('sorts by name ascending by default', () => {
    const networks = [
      makeNetwork({ name: 'zeta', uid: '1' }),
      makeNetwork({ name: 'alpha', uid: '2' }),
      makeNetwork({ name: 'mu', uid: '3' }),
    ];
    renderTable(<NetworkTable networks={networks} />);
    const rows = screen.getAllByTestId('network-table-row');
    expect(within(rows[0]).getByText('alpha')).toBeInTheDocument();
    expect(within(rows[1]).getByText('mu')).toBeInTheDocument();
    expect(within(rows[2]).getByText('zeta')).toBeInTheDocument();
  });

  it('reverses name sort on second click', () => {
    const networks = [
      makeNetwork({ name: 'zeta', uid: '1' }),
      makeNetwork({ name: 'alpha', uid: '2' }),
    ];
    renderTable(<NetworkTable networks={networks} />);
    const nameHeader = screen.getByLabelText('Sort by Name');
    fireEvent.click(nameHeader);

    const rows = screen.getAllByTestId('network-table-row');
    expect(within(rows[0]).getByText('zeta')).toBeInTheDocument();
    expect(within(rows[1]).getByText('alpha')).toBeInTheDocument();
  });

  it('sorts by Ready status: not-ready first, then unknown, then ready', () => {
    const networks = [
      makeNetwork({ name: 'ready-net', uid: '1', readyStatus: 'True' }),
      makeNetwork({ name: 'unready-net', uid: '2', readyStatus: 'False', readyReason: 'IPv6Required' }),
      makeNetwork({ name: 'unknown-net', uid: '3', readyStatus: 'Unknown' }),
    ];
    renderTable(<NetworkTable networks={networks} />);
    const readyHeader = screen.getByLabelText('Sort by Ready');
    fireEvent.click(readyHeader);

    const rows = screen.getAllByTestId('network-table-row');
    expect(within(rows[0]).getByText('unready-net')).toBeInTheDocument();
    expect(within(rows[1]).getByText('unknown-net')).toBeInTheDocument();
    expect(within(rows[2]).getByText('ready-net')).toBeInTheDocument();
  });

  it('shows plain-language reason text for a not-ready network, not the raw reason', () => {
    renderTable(<NetworkTable
        networks={[makeNetwork({ name: 'n', readyStatus: 'False', readyReason: 'IPv6Required' })]}
      />
    );
    expect(screen.getByText('IPv6 required')).toBeInTheDocument();
    expect(screen.queryByText('IPv6Required')).not.toBeInTheDocument();
  });

  it('shows "Unknown" when the Ready condition is absent', () => {
    renderTable(<NetworkTable networks={[makeNetwork({ name: 'n', readyStatus: 'Unknown' })]} />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });

  it('shows an Enable IPv6 action only for a network missing IPv6', () => {
    renderTable(
      <NetworkTable
        networks={[
          makeNetwork({ name: 'v4-only', uid: '1', ipFamilies: ['IPv4'] }),
          makeNetwork({ name: 'dual-stack', uid: '2', ipFamilies: ['IPv4', 'IPv6'] }),
        ]}
        projectId="demo-project"
      />
    );
    const v4OnlyRow = screen.getByText('v4-only').closest('tr') as HTMLElement;
    const dualStackRow = screen.getByText('dual-stack').closest('tr') as HTMLElement;
    expect(within(v4OnlyRow).getByRole('button', { name: 'Enable IPv6' })).toBeInTheDocument();
    expect(within(dualStackRow).queryByRole('button', { name: 'Enable IPv6' })).not.toBeInTheDocument();
  });

  it('filters rows by name via the search box', () => {
    const networks = [
      makeNetwork({ name: 'prod-net', uid: '1' }),
      makeNetwork({ name: 'staging-net', uid: '2' }),
    ];
    renderTable(<NetworkTable networks={networks} />);
    fireEvent.change(screen.getByLabelText('Search networks'), { target: { value: 'prod' } });

    const rows = screen.getAllByTestId('network-table-row');
    expect(rows).toHaveLength(1);
    expect(within(rows[0]).getByText('prod-net')).toBeInTheDocument();
  });

  it('shows the total count regardless of the active filter', () => {
    const networks = [
      makeNetwork({ name: 'prod-net', uid: '1' }),
      makeNetwork({ name: 'staging-net', uid: '2' }),
    ];
    renderTable(<NetworkTable networks={networks} />);
    fireEvent.change(screen.getByLabelText('Search networks'), { target: { value: 'prod' } });

    expect(screen.getByText('2')).toBeInTheDocument();
  });

  it('navigates to the network detail page when a row is clicked', () => {
    renderTable(<NetworkTable networks={[makeNetwork({ name: 'prod-net' })]} />);
    fireEvent.click(screen.getByTestId('network-table-row'));

    expect(screen.getByTestId('location-display')).toHaveTextContent(
      '/project/demo-project/services/networking/networks/prod-net'
    );
  });

  it('does not navigate when the Enable IPv6 action is clicked', () => {
    renderTable(
      <NetworkTable networks={[makeNetwork({ name: 'v4-only', ipFamilies: ['IPv4'] })]} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Enable IPv6' }));

    expect(screen.getByTestId('location-display')).toHaveTextContent(
      '/project/demo-project/services/networking/networks'
    );
  });
});

import { NetworkTable } from './network-table';
import * as api from '../lib/api';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useDeleteNetwork: vi.fn() };
});

const useDeleteNetworkMock = vi.mocked(api.useDeleteNetwork);
const deleteMock = vi.fn();

beforeEach(() => {
  deleteMock.mockReset().mockResolvedValue(undefined);
  useDeleteNetworkMock.mockReset().mockReturnValue({ mutateAsync: deleteMock } as never);
});

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

function bodyRows(): HTMLElement[] {
  const table = within(screen.getByTestId('networking-plugin-network-table'));
  return table.queryAllByRole('row').filter((row) => within(row).queryAllByRole('link').length > 0);
}

function rowNames(): string[] {
  return bodyRows().map((row) => within(row).getByRole('link').textContent ?? '');
}

function rowFor(name: string): HTMLElement {
  return screen.getByRole('link', { name }).closest('tr') as HTMLElement;
}

function openRowMenu(name: string) {
  fireEvent.click(within(rowFor(name)).getByRole('button', { name: 'Actions' }));
}

const location = () => screen.getByTestId('location-display');

describe('NetworkTable', () => {
  it('shows no rows when there are no networks', () => {
    renderTable(<NetworkTable networks={[]} />);
    expect(bodyRows()).toHaveLength(0);
  });

  it('renders a row per network, sorted by name', () => {
    renderTable(
      <NetworkTable
        networks={[
          makeNetwork({ name: 'zeta', uid: '1' }),
          makeNetwork({ name: 'alpha', uid: '2' }),
          makeNetwork({ name: 'mu', uid: '3' }),
        ]}
      />
    );
    expect(rowNames()).toEqual(['alpha', 'mu', 'zeta']);
  });

  it('reverses the name sort from the header', () => {
    renderTable(
      <NetworkTable
        networks={[makeNetwork({ name: 'zeta', uid: '1' }), makeNetwork({ name: 'alpha', uid: '2' })]}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: /^Sort by Name/ }));
    expect(rowNames()).toEqual(['zeta', 'alpha']);
  });

  it('sorts by status: not-ready first, then unknown, then ready', () => {
    renderTable(
      <NetworkTable
        networks={[
          makeNetwork({ name: 'ready-net', uid: '1', readyStatus: 'True' }),
          makeNetwork({ name: 'unready-net', uid: '2', readyStatus: 'False', readyReason: 'IPv6Required' }),
          makeNetwork({ name: 'unknown-net', uid: '3', readyStatus: 'Unknown' }),
        ]}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: /^Sort by Status/ }));
    expect(rowNames()).toEqual(['unready-net', 'unknown-net', 'ready-net']);
  });

  it('shows plain-language reason text for a not-ready network, not the raw reason', () => {
    renderTable(
      <NetworkTable
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

  it('filters rows via the search box', async () => {
    renderTable(
      <NetworkTable
        networks={[makeNetwork({ name: 'prod-net', uid: '1' }), makeNetwork({ name: 'staging-net', uid: '2' })]}
      />
    );
    fireEvent.change(screen.getByPlaceholderText('Search networks'), { target: { value: 'prod' } });
    await waitFor(() => expect(rowNames()).toEqual(['prod-net']));
  });

  it('opens the network when a row is clicked', () => {
    renderTable(<NetworkTable networks={[makeNetwork({ name: 'prod-net' })]} />);
    fireEvent.click(within(rowFor('prod-net')).getAllByRole('cell')[1]);
    expect(location()).toHaveTextContent('/project/demo-project/services/networking/networks/prod-net');
  });

  it('opens the network from the View action', () => {
    renderTable(<NetworkTable networks={[makeNetwork({ name: 'prod-net' })]} />);
    openRowMenu('prod-net');
    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    expect(location()).toHaveTextContent('/project/demo-project/services/networking/networks/prod-net');
  });

  it('offers Enable IPv6 only for a network missing IPv6', () => {
    renderTable(
      <NetworkTable
        networks={[
          makeNetwork({ name: 'v4-only', uid: '1', ipFamilies: ['IPv4'] }),
          makeNetwork({ name: 'dual-stack', uid: '2', ipFamilies: ['IPv4', 'IPv6'] }),
        ]}
        projectId="demo-project"
      />
    );
    openRowMenu('dual-stack');
    expect(screen.queryByRole('button', { name: 'Enable IPv6' })).not.toBeInTheDocument();
    openRowMenu('dual-stack');

    openRowMenu('v4-only');
    fireEvent.click(screen.getByRole('button', { name: 'Enable IPv6' }));
    expect(screen.getByText(/Adds IPv6 to v4-only's address families/)).toBeInTheDocument();
    expect(location()).toHaveTextContent(/\/networks$/);
  });

  it('deletes a network from the row menu once DELETE is typed', async () => {
    renderTable(<NetworkTable networks={[makeNetwork({ name: 'prod-net' })]} projectId="demo-project" />);
    openRowMenu('prod-net');
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    const dialog = within(await screen.findByRole('dialog'));
    const confirm = dialog.getByRole('button', { name: 'Delete' });
    expect(confirm).toBeDisabled();
    fireEvent.change(dialog.getByPlaceholderText('Type in here...'), { target: { value: 'DELETE' } });
    fireEvent.click(confirm);

    await waitFor(() => expect(deleteMock).toHaveBeenCalledWith('prod-net'));
    expect(location()).toHaveTextContent(/\/networks$/);
  });
});

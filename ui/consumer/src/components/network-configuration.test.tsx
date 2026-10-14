import { NetworkConfiguration } from './network-configuration';
import * as api from '../lib/api';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useDeleteNetwork: vi.fn(), useUpdateNetwork: vi.fn() };
});

const useDeleteNetworkMock = vi.mocked(api.useDeleteNetwork);
const useUpdateNetworkMock = vi.mocked(api.useUpdateNetwork);
const deleteMutateAsyncMock = vi.fn();
const updateMutateMock = vi.fn();

function makeNetwork(overrides: Partial<Network> = {}): Network {
  return {
    uid: 'default',
    name: 'default',
    resourceVersion: '123',
    createdAt: new Date('2026-01-01T00:00:00Z'),
    readyStatus: 'True',
    ipFamilies: ['IPv4', 'IPv6'],
    ipv6Prefix: 'fd20:0:a::/48',
    ipamMode: 'Auto',
    mtu: 1460,
    conditions: [],
    ...overrides,
  };
}

function renderConfiguration(overrides: Partial<Network> = {}, onDeleted = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return rtlRender(
    <QueryClientProvider client={client}>
      <NetworkConfiguration network={makeNetwork(overrides)} projectId="demo-project" onDeleted={onDeleted} />
    </QueryClientProvider>
  );
}

const ipCard = () => within(screen.getByTestId('networking-plugin-config-ip'));

beforeEach(() => {
  deleteMutateAsyncMock.mockReset().mockResolvedValue(undefined);
  useDeleteNetworkMock.mockReset().mockReturnValue({
    mutateAsync: deleteMutateAsyncMock,
    isPending: false,
  } as never);
  updateMutateMock.mockReset();
  useUpdateNetworkMock.mockReset().mockReturnValue({
    mutate: updateMutateMock,
    isPending: false,
  } as never);
});

describe('NetworkConfiguration', () => {
  it('links every section from the settings nav', () => {
    renderConfiguration();

    expect(screen.getByRole('link', { name: 'General' })).toHaveAttribute('href', '#general');
    expect(screen.getByRole('link', { name: 'IP Configuration' })).toHaveAttribute('href', '#ip-configuration');
    expect(screen.getByRole('link', { name: 'Danger Zone' })).toHaveAttribute('href', '#danger');
  });

  it('shows the resource name and status in General', () => {
    renderConfiguration();

    const general = within(screen.getByTestId('networking-plugin-config-general'));
    expect(general.getByText('default')).toBeInTheDocument();
    expect(general.getByText('Ready')).toBeInTheDocument();
  });

  it('does not show fields we have no data for, like display name or labels', () => {
    renderConfiguration();

    expect(screen.queryByText('Display name')).not.toBeInTheDocument();
    expect(screen.queryByText('Labels')).not.toBeInTheDocument();
    expect(screen.queryByText('Description')).not.toBeInTheDocument();
  });

  it('shows real ip family, prefix, ipam mode, and mtu in IP Configuration', () => {
    renderConfiguration();

    expect(ipCard().getByText('fd20:0:a::/48')).toBeInTheDocument();
    expect(ipCard().getByText('IPv4')).toBeInTheDocument();
    expect(ipCard().getByText('IPv6')).toBeInTheDocument();
    expect(ipCard().getByText('Auto')).toBeInTheDocument();
    expect(ipCard().getByText('1460')).toBeInTheDocument();
  });

  it('offers Enable IPv6 only when the network lacks it', () => {
    renderConfiguration();
    expect(ipCard().queryByRole('button', { name: 'Enable IPv6' })).not.toBeInTheDocument();
  });

  it('opens the Enable IPv6 dialog for an IPv4-only network', () => {
    renderConfiguration({ ipFamilies: ['IPv4'] });

    fireEvent.click(ipCard().getByRole('button', { name: 'Enable IPv6' }));

    expect(screen.getByText(/Adds IPv6 to default's address families/)).toBeInTheDocument();
  });

  it('edits the MTU inline and saves it', () => {
    renderConfiguration();

    fireEvent.click(ipCard().getByRole('button', { name: 'Edit' }));
    const input = ipCard().getByLabelText('MTU');
    expect(input).toHaveValue(1460);
    expect(ipCard().getByRole('button', { name: 'Save changes' })).toBeDisabled();

    fireEvent.change(input, { target: { value: '1500' } });
    fireEvent.click(ipCard().getByRole('button', { name: 'Save changes' }));

    expect(updateMutateMock).toHaveBeenCalledWith(
      { network: expect.objectContaining({ name: 'default' }), spec: { mtu: 1500 } },
      expect.anything()
    );
  });

  it('blocks saving an out-of-range MTU', () => {
    renderConfiguration();

    fireEvent.click(ipCard().getByRole('button', { name: 'Edit' }));
    fireEvent.change(ipCard().getByLabelText('MTU'), { target: { value: '9000' } });

    expect(ipCard().getByText('Must be between 1300 and 8856.')).toBeInTheDocument();
    expect(ipCard().getByRole('button', { name: 'Save changes' })).toBeDisabled();
  });

  it('discards an MTU edit on cancel', () => {
    renderConfiguration();

    fireEvent.click(ipCard().getByRole('button', { name: 'Edit' }));
    fireEvent.change(ipCard().getByLabelText('MTU'), { target: { value: '1500' } });
    fireEvent.click(ipCard().getByRole('button', { name: 'Cancel' }));

    expect(ipCard().getByText('1460')).toBeInTheDocument();
    expect(updateMutateMock).not.toHaveBeenCalled();
  });

  it('deletes the network from the danger zone and calls onDeleted', async () => {
    const onDeleted = vi.fn();
    renderConfiguration({ name: 'prod-net' }, onDeleted);

    fireEvent.click(screen.getByRole('button', { name: 'Delete network' }));
    const dialog = within(await screen.findByRole('dialog'));
    fireEvent.change(dialog.getByPlaceholderText('Type in here...'), { target: { value: 'DELETE' } });
    fireEvent.click(dialog.getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(deleteMutateAsyncMock).toHaveBeenCalledWith('prod-net'));
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
  });
});

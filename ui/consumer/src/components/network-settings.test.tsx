import { NetworkSettings } from './network-settings';
import * as api from '../lib/api';
import type { Network } from '../schema';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render as rtlRender, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return { ...actual, useDeleteNetwork: vi.fn() };
});

const useDeleteNetworkMock = vi.mocked(api.useDeleteNetwork);
const deleteMutateAsyncMock = vi.fn();

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

function render(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return rtlRender(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

function renderSettings(overrides: Partial<Network> = {}, onDeleted = vi.fn()) {
  return render(
    <NetworkSettings network={makeNetwork(overrides)} projectId="demo-project" onDeleted={onDeleted} />
  );
}

beforeEach(() => {
  deleteMutateAsyncMock.mockReset().mockResolvedValue(undefined);
  useDeleteNetworkMock.mockReset().mockReturnValue({
    mutateAsync: deleteMutateAsyncMock,
    isPending: false,
  } as never);
});

describe('NetworkSettings', () => {
  it('shows every section at once, not just the selected one', () => {
    renderSettings();

    expect(screen.getByText('Resource name')).toBeInTheDocument();
    expect(screen.getByText('IPv6 prefix')).toBeInTheDocument();
  });

  it('only renders the sections we actually implement, no Coming soon placeholders', () => {
    renderSettings();

    expect(screen.queryByText('Coming soon')).not.toBeInTheDocument();
  });

  it('does not show fields we have no data for, like display name or labels', () => {
    renderSettings();

    expect(screen.queryByText('Display name')).not.toBeInTheDocument();
    expect(screen.queryByText('Labels')).not.toBeInTheDocument();
    expect(screen.queryByText('Description')).not.toBeInTheDocument();
  });

  it('shows real ip family, prefix, ipam mode, and mtu in the IP Configuration section', () => {
    renderSettings();

    expect(screen.getByText('fd20:0:a::/48')).toBeInTheDocument();
    expect(screen.getByText('IPv4')).toBeInTheDocument();
    expect(screen.getByText('IPv6')).toBeInTheDocument();
    expect(screen.getByText('Auto')).toBeInTheDocument();
    expect(screen.getByText('1460')).toBeInTheDocument();
  });

  it('scrolls the corresponding section into view when a nav item is clicked, without hiding the rest', () => {
    renderSettings();
    const scrollIntoViewSpy = vi.fn();
    Element.prototype.scrollIntoView = scrollIntoViewSpy;

    fireEvent.click(screen.getByRole('button', { name: /IP Configuration/ }));

    expect(scrollIntoViewSpy).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' });
    // General is still in the document — the nav did not replace the content.
    expect(screen.getByText('Resource name')).toBeInTheDocument();
  });

  it('disables every per-card Edit action', () => {
    renderSettings();
    const editButtons = screen.getAllByRole('button', { name: /Edit/ });
    expect(editButtons.length).toBeGreaterThan(0);
    for (const button of editButtons) {
      expect(button).toBeDisabled();
    }
  });

  it('shows a Danger Zone with a delete action, not a Coming soon placeholder', () => {
    renderSettings();

    // Appears both as the nav item and the section title.
    expect(screen.getAllByText('Danger Zone')).toHaveLength(2);
    expect(screen.getByRole('button', { name: 'Delete network' })).toBeEnabled();
  });

  it('deletes the network and calls onDeleted on confirm', async () => {
    const onDeleted = vi.fn();
    renderSettings({ name: 'prod-net' }, onDeleted);

    fireEvent.click(screen.getByRole('button', { name: 'Delete network' }));

    const dialogButtons = await screen.findAllByRole('button', { name: 'Delete' });
    fireEvent.click(dialogButtons[dialogButtons.length - 1]);

    await waitFor(() => expect(deleteMutateAsyncMock).toHaveBeenCalledWith('prod-net'));
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
  });
});

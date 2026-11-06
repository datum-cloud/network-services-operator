import { MetricsToolbar } from './metrics-toolbar';
import { PLUGIN_ID } from '../lib/api';
import { METRICS_QUERY_KEY } from '../lib/metrics';
import type { MetricsControls } from '../lib/metrics-controls';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

const NOW = 1_800_000_000_000;

function controls(overrides: Partial<MetricsControls> = {}): MetricsControls {
  return {
    range: 'now-1h',
    resolved: { start: NOW - 3_600_000, end: NOW, preset: 'now-1h' },
    window: { start: NOW - 3_600_000, end: NOW, stepSeconds: 60 },
    step: 'auto',
    refresh: 'off',
    regions: [],
    instances: [],
    advance: vi.fn(),
    setRange: vi.fn(),
    setStep: vi.fn(),
    setRefresh: vi.fn(),
    setRegions: vi.fn(),
    setInstances: vi.fn(),
    ...overrides,
  };
}

function renderToolbar(c: MetricsControls, client = new QueryClient()) {
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <MetricsToolbar
          controls={c}
          regionOptions={[
            { value: 'eu-west-1', label: 'eu-west-1' },
            { value: 'us-east-1', label: 'us-east-1' },
          ]}
          instanceOptions={[
            { value: 'web-0-eth0', label: 'web · instance 0' },
            { value: 'web-1-eth0', label: 'web · instance 1' },
          ]}
          pickedRegions={['us-east-1']}
          pickedInstances={[]}
        />
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe('MetricsToolbar', () => {
  it('moves a relative window forward on refresh without refetching the old one', () => {
    const client = new QueryClient();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const c = controls();
    renderToolbar(c, client);

    fireEvent.click(screen.getByTestId('networking-plugin-metrics-refresh'));

    expect(c.advance).toHaveBeenCalledTimes(1);
    expect(invalidate).not.toHaveBeenCalled();
  });

  it('refetches an absolute range on refresh', async () => {
    const client = new QueryClient();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const c = controls({ range: '100_200' });
    renderToolbar(c, client);

    fireEvent.click(screen.getByTestId('networking-plugin-metrics-refresh'));

    expect(c.advance).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith({ queryKey: [PLUGIN_ID, METRICS_QUERY_KEY] })
    );
  });

  it('shows only the picks that match something', () => {
    renderToolbar(controls({ regions: ['us-east-1', 'gone-1'], instances: ['web-9-eth0'] }));

    const toolbar = screen.getByTestId('networking-plugin-metrics-toolbar');
    expect(toolbar).toHaveTextContent('us-east-1');
    expect(toolbar).not.toHaveTextContent('gone-1');
    expect(toolbar).not.toHaveTextContent('web-9-eth0');
    expect(toolbar).toHaveTextContent('All instances');
  });
});

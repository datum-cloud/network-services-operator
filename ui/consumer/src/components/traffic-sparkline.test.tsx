import { TrafficSparkCell, sparkPath, type InterfaceSparks } from './traffic-sparkline';
import type { NetworkInterface } from '../schema';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';

const IFACE = { name: 'web-0-eth0' } as NetworkInterface;

function sparks(overrides: Partial<InterfaceSparks> = {}): InterfaceSparks {
  return { byInterface: new Map(), isLoading: false, denied: false, failed: false, ...overrides };
}

describe('sparkPath', () => {
  it('scales points into the box with the peak at the top', () => {
    expect(sparkPath([[0, 0], [10, 5], [20, 10]], { start: 0, end: 20, max: 10 }, 20, 12)).toBe(
      'M0.0,11.0 L10.0,6.0 L20.0,1.0'
    );
    expect(sparkPath([], { start: 0, end: 1, max: 1 })).toBe('');
  });
});

describe('TrafficSparkCell', () => {
  it('shows the latest rates and links to the instance in Metrics', () => {
    render(
      <MemoryRouter>
        <TrafficSparkCell
          iface={IFACE}
          metricsHref="/networks/prod-net/metrics"
          sparks={sparks({
            byInterface: new Map([
              ['web-0-eth0', { receive: [[0, 1000], [60, 2500]], transmit: [[0, 400], [60, 900]] }],
            ]),
          })}
        />
      </MemoryRouter>
    );

    expect(screen.getByTestId('network-interface-traffic')).toHaveTextContent('↓ 2.50 kb/s↑ 900 b/s');
    expect(screen.getByRole('link')).toHaveAttribute(
      'href',
      '/networks/prod-net/metrics?instances=web-0-eth0'
    );
  });

  it('shows a dash when there is no traffic yet', () => {
    render(
      <MemoryRouter>
        <TrafficSparkCell iface={IFACE} sparks={sparks()} />
      </MemoryRouter>
    );
    expect(screen.getByText('—')).toBeInTheDocument();
  });
});

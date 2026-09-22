import { NetworkStats } from './network-stats';
import type { Network } from '../schema';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

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

describe('NetworkStats', () => {
  it('counts total, ready, needs-attention, and dual-stack networks', () => {
    render(
      <NetworkStats
        networks={[
          makeNetwork({ name: 'a', uid: '1', readyStatus: 'True', ipFamilies: ['IPv6'] }),
          makeNetwork({ name: 'b', uid: '2', readyStatus: 'False', ipFamilies: ['IPv4'] }),
          makeNetwork({ name: 'c', uid: '3', readyStatus: 'True', ipFamilies: ['IPv4', 'IPv6'] }),
        ]}
      />
    );

    expect(screen.getByText('Networks')).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByText('Needs attention')).toBeInTheDocument();
    expect(screen.getByText('Dual-stack')).toBeInTheDocument();

    const values = screen.getAllByText(/^[0-9]+$/).map((el) => el.textContent);
    expect(values).toEqual(['3', '2', '1', '1']);
  });
});

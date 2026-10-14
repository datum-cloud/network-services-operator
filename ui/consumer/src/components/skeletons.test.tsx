import {
  ConfigurationSkeleton,
  NetworkListSkeleton,
  NetworkOverviewSkeleton,
  ResourcesSkeleton,
  ServicesSkeleton,
} from './skeletons';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

describe('skeletons', () => {
  it.each([
    ['network-list', <NetworkListSkeleton />, ['Name', 'IPv6 prefix', 'MTU']],
    ['services', <ServicesSkeleton />, ['Ports', 'Members', 'Ready']],
    ['resources', <ResourcesSkeleton />, ['Workload', 'Address']],
    ['overview', <NetworkOverviewSkeleton />, ['Workload', 'Address']],
  ] as const)('marks the %s skeleton busy and labels its table columns', (kind, ui, columns) => {
    render(ui);

    const loading = screen.getByTestId('networking-plugin-loading');
    expect(loading).toHaveAttribute('aria-busy', 'true');
    expect(loading).toHaveAttribute('data-skeleton', kind);
    for (const column of columns) {
      expect(screen.getByText(column)).toBeInTheDocument();
    }
  });

  it('renders a single loading region for the overview', () => {
    render(<NetworkOverviewSkeleton />);
    expect(screen.getAllByTestId('networking-plugin-loading')).toHaveLength(1);
  });

  it('marks the configuration skeleton busy', () => {
    render(<ConfigurationSkeleton />);
    expect(screen.getByTestId('networking-plugin-loading')).toHaveAttribute('data-skeleton', 'configuration');
  });
});

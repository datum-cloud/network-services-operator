import { NetworkResources } from './network-resources';
import * as api from '../lib/api';
import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof api>('../lib/api');
  return {
    ...actual,
    useSubnets: vi.fn(),
    useNetworkInterfaces: vi.fn(),
    useHTTPProxies: vi.fn(),
    useNetworkServices: vi.fn(),
  };
});

const useSubnetsMock = vi.mocked(api.useSubnets);
const useNetworkInterfacesMock = vi.mocked(api.useNetworkInterfaces);
const useHTTPProxiesMock = vi.mocked(api.useHTTPProxies);
const useNetworkServicesMock = vi.mocked(api.useNetworkServices);
const emptyResult = { data: [], isLoading: false, error: null, refetch: vi.fn() } as never;

beforeEach(() => {
  useSubnetsMock.mockReset().mockReturnValue(emptyResult);
  useNetworkInterfacesMock.mockReset().mockReturnValue(emptyResult);
  useHTTPProxiesMock.mockReset().mockReturnValue(emptyResult);
  useNetworkServicesMock.mockReset().mockReturnValue(emptyResult);
});

describe('NetworkResources', () => {
  it('shows Regions and Connected workloads together, not as separate tabs', () => {
    render(<NetworkResources projectId="demo-project" networkName="default" />);

    expect(screen.getByRole('heading', { name: 'Regions' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Connected workloads' })).toBeInTheDocument();
  });

  it('does not show a Routes section, since there is no Route resource to back it', () => {
    render(<NetworkResources projectId="demo-project" networkName="default" />);

    expect(screen.queryByRole('heading', { name: 'Routes' })).not.toBeInTheDocument();
  });
});

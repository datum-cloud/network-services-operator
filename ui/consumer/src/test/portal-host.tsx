import type { QuotaIncreaseRequest, ResourceQuotaVerdict } from '@datum-cloud/portal-plugin-sdk';
import { type PortalPluginHostBindings, PortalPluginHostProvider } from '@datum-cloud/portal-plugin-sdk/host';
import type { ReactNode } from 'react';
import { vi } from 'vitest';

export const IPV4_VERDICTS = {
  loading: { isLoading: true, isUnknown: true, denied: false },
  missing: { isLoading: false, isUnknown: true, denied: false },
  noAllocation: {
    isLoading: false,
    isUnknown: false,
    denied: true,
    deniedReason: 'You’ve reached your quota (0/0).',
    limit: 0,
    allocated: 0,
    available: 0,
  },
  usedUp: {
    isLoading: false,
    isUnknown: false,
    denied: true,
    deniedReason: 'You’ve reached your quota (1/1).',
    limit: 1,
    allocated: 1,
    available: 0,
  },
  available: { isLoading: false, isUnknown: false, denied: false, limit: 2, allocated: 1, available: 1 },
} satisfies Record<string, ResourceQuotaVerdict>;

export function fakePortalHost(verdict: ResourceQuotaVerdict = IPV4_VERDICTS.available) {
  const requestQuotaIncrease = vi.fn<(request: QuotaIncreaseRequest) => void>();
  const useResourceQuota = vi.fn(() => verdict);
  const bindings: PortalPluginHostBindings = {
    useProjectContext: () => ({ project: undefined, org: undefined, isLoading: false, error: null }),
    usePluginFetch: () => () => Promise.reject(new Error('usePluginFetch is not faked')),
    useResourceWatch: () => ({ lastEvent: null, isConnected: false, error: null }),
    useResourceQuota,
    useRequestQuotaIncrease: () => requestQuotaIncrease,
  };
  function Wrapper({ children }: { children: ReactNode }) {
    return <PortalPluginHostProvider bindings={bindings}>{children}</PortalPluginHostProvider>;
  }
  return { Wrapper, bindings, useResourceQuota, requestQuotaIncrease };
}

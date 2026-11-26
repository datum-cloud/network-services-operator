import { ApiError } from './api';
import {
  type QuotaIncreaseRequest,
  type ResourceQuotaVerdict,
  useRequestQuotaIncrease,
  useResourceQuota,
} from '@datum-cloud/portal-plugin-sdk';
import { useCallback } from 'react';

export const PUBLIC_IPV4_RESOURCE_TYPE = 'ipam.miloapis.com/public-ipv4claims';

export type Ipv4AvailabilityState = 'loading' | 'available' | 'used-up' | 'no-allocation' | 'unknown';

export interface Ipv4Availability {
  state: Ipv4AvailabilityState;
  canChoose: boolean;
  disabledReason?: string;
  limit?: number;
  allocated?: number;
  available?: number;
}

export const IPV4_COPY = {
  optionLabel: 'IPv4',
  paidAddOn: 'Public IPv4 is a paid add-on.',
  requestAction: 'Request IPv4 address',
  noAllocation: 'This project doesn’t have a public IPv4 allocation.',
  usedUp: 'This project has used all of its public IPv4 addresses.',
  checking: 'Checking this project’s public IPv4 allocation…',
  quotaRefused:
    'This load balancer couldn’t get a public IPv4 address because the project’s IPv4 allocation is used up. Its IPv6 address isn’t affected.',
} as const;

export function ipv4LocationsWarning(locations: number, available: number): string {
  const left = `${available} public IPv4 ${available === 1 ? 'address' : 'addresses'}`;
  return `This load balancer serves ${locations} locations and the project has ${left} left. Each location uses one, so some locations won’t get an IPv4 address until more are granted.`;
}

export function releaseIpv4Message(locations: string[]): string {
  const where =
    locations.length > 0
      ? `its public IPv4 ${locations.length === 1 ? 'address' : 'addresses'} in ${locations.join(', ')}`
      : 'its public IPv4 address';
  return `Saving releases ${where}. The same address can’t be kept, and isn’t guaranteed back if you add IPv4 again later. Its IPv6 address isn’t affected.`;
}

export function deriveIpv4Availability(verdict: ResourceQuotaVerdict): Ipv4Availability {
  const counts = { limit: verdict.limit, allocated: verdict.allocated, available: verdict.available };
  if (verdict.isLoading) return { state: 'loading', canChoose: false };
  if (verdict.isUnknown || verdict.limit === undefined || verdict.available === undefined) {
    return { state: 'unknown', canChoose: false, disabledReason: IPV4_COPY.noAllocation };
  }
  if (verdict.limit <= 0) {
    return { state: 'no-allocation', canChoose: false, disabledReason: IPV4_COPY.noAllocation, ...counts };
  }
  if (verdict.denied || verdict.available <= 0) {
    return { state: 'used-up', canChoose: false, disabledReason: IPV4_COPY.usedUp, ...counts };
  }
  return { state: 'available', canChoose: true, ...counts };
}

export function useIpv4Availability(): Ipv4Availability {
  return deriveIpv4Availability(
    useResourceQuota({ resourceType: PUBLIC_IPV4_RESOURCE_TYPE, scope: 'project' })
  );
}

export function buildIpv4QuotaIncreaseRequest(nlbName?: string): QuotaIncreaseRequest {
  return {
    resourceType: PUBLIC_IPV4_RESOURCE_TYPE,
    scope: 'project',
    context: nlbName
      ? `Public IPv4 address for network load balancer ${nlbName}.`
      : 'Public IPv4 address for a network load balancer.',
  };
}

export function useRequestIpv4(): (nlbName?: string) => void {
  const request = useRequestQuotaIncrease();
  return useCallback((nlbName?: string) => request(buildIpv4QuotaIncreaseRequest(nlbName)), [request]);
}

const QUOTA_REFUSAL = /reached your quota|quota exceeded|insufficient quota/i;

export function isQuotaRefusalMessage(message: string | undefined): boolean {
  return !!message && QUOTA_REFUSAL.test(message);
}

export function isQuotaRefusalError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 403 && isQuotaRefusalMessage(error.message);
}

export interface NlbAddressStatusLike {
  family?: string;
  value?: string;
  reason?: string;
  message?: string;
  conditions?: { type?: string; status?: string; reason?: string; message?: string }[];
}

export function isIpv4QuotaRefusal(address: NlbAddressStatusLike): boolean {
  if (address.family !== 'IPv4' || address.value) return false;
  const allocated = address.conditions?.find((c) => c.type === 'Allocated');
  if (allocated && allocated.status !== 'False') return false;
  return isQuotaRefusalMessage(allocated?.message) || isQuotaRefusalMessage(address.message);
}

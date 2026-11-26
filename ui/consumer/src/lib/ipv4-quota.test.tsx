import { fakePortalHost, IPV4_VERDICTS } from '../test/portal-host';
import { ApiError } from './api';
import {
  buildIpv4QuotaIncreaseRequest,
  deriveIpv4Availability,
  IPV4_COPY,
  isIpv4QuotaRefusal,
  isQuotaRefusalError,
  isQuotaRefusalMessage,
  PUBLIC_IPV4_RESOURCE_TYPE,
  useIpv4Availability,
  useRequestIpv4,
} from './ipv4-quota';
import { renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

const MILO_QUOTA_403 =
  'ipclaims.ipam.miloapis.com "nlb-abc-tcp-80-1a2b" is forbidden: You\'ve reached your quota for this resource type (Resource quota exceeded: requested 1, available 0). Delete unused resources to free up capacity, or contact support to request a higher limit.';

describe('deriveIpv4Availability', () => {
  it.each([
    ['loading', IPV4_VERDICTS.loading, 'loading', undefined],
    ['missing bucket', IPV4_VERDICTS.missing, 'unknown', IPV4_COPY.noAllocation],
    ['0 allowed', IPV4_VERDICTS.noAllocation, 'no-allocation', IPV4_COPY.noAllocation],
    ['used up', IPV4_VERDICTS.usedUp, 'used-up', IPV4_COPY.usedUp],
    ['available', IPV4_VERDICTS.available, 'available', undefined],
  ] as const)('%s', (_, verdict, state, disabledReason) => {
    const result = deriveIpv4Availability(verdict);
    expect(result.state).toBe(state);
    expect(result.canChoose).toBe(state === 'available');
    expect(result.disabledReason).toBe(disabledReason);
  });

  it('fails closed when the bucket is unreadable, even though the SDK does not deny', () => {
    const result = deriveIpv4Availability({ isLoading: false, isUnknown: true, denied: false });
    expect(result).toEqual({ state: 'unknown', canChoose: false, disabledReason: IPV4_COPY.noAllocation });
  });

  it('fails closed when a known verdict has no counts', () => {
    expect(deriveIpv4Availability({ isLoading: false, isUnknown: false, denied: false }).canChoose).toBe(false);
  });

  it('never offers IPv4 while loading, even if counts are present', () => {
    expect(deriveIpv4Availability({ ...IPV4_VERDICTS.available, isLoading: true }).state).toBe('loading');
  });

  it('treats available <= 0 as used up even when the host does not set denied', () => {
    const result = deriveIpv4Availability({ ...IPV4_VERDICTS.usedUp, denied: false });
    expect(result.state).toBe('used-up');
    expect(result.canChoose).toBe(false);
  });

  it('passes the counts through for a known bucket', () => {
    expect(deriveIpv4Availability(IPV4_VERDICTS.available)).toMatchObject({ limit: 2, allocated: 1, available: 1 });
  });
});

describe('useIpv4Availability', () => {
  it('reads the project public IPv4 bucket', () => {
    const host = fakePortalHost(IPV4_VERDICTS.available);
    const { result } = renderHook(() => useIpv4Availability(), { wrapper: host.Wrapper });
    expect(result.current.canChoose).toBe(true);
    expect(host.useResourceQuota).toHaveBeenCalledWith({
      resourceType: PUBLIC_IPV4_RESOURCE_TYPE,
      scope: 'project',
    });
  });

  it('is not choosable when the host reports no bucket', () => {
    const host = fakePortalHost(IPV4_VERDICTS.missing);
    const { result } = renderHook(() => useIpv4Availability(), { wrapper: host.Wrapper });
    expect(result.current.canChoose).toBe(false);
  });
});

describe('buildIpv4QuotaIncreaseRequest', () => {
  it('prefills public IPv4 for the project', () => {
    expect(buildIpv4QuotaIncreaseRequest()).toEqual({
      resourceType: 'ipam.miloapis.com/public-ipv4claims',
      scope: 'project',
      context: 'Public IPv4 address for a network load balancer.',
    });
  });

  it('names the load balancer when there is one', () => {
    expect(buildIpv4QuotaIncreaseRequest('web').context).toBe(
      'Public IPv4 address for network load balancer web.'
    );
  });
});

describe('useRequestIpv4', () => {
  it('opens the host quota increase request prefilled for public IPv4', () => {
    const host = fakePortalHost();
    const { result } = renderHook(() => useRequestIpv4(), { wrapper: host.Wrapper });
    result.current('web');
    expect(host.requestQuotaIncrease).toHaveBeenCalledWith(buildIpv4QuotaIncreaseRequest('web'));
  });
});

describe('isQuotaRefusalMessage', () => {
  it.each([
    [MILO_QUOTA_403, true],
    ['Resource quota exceeded: requested 1, available 0', true],
    ['Insufficient quota resources available', true],
    ['networkloadbalancers.networking.datumapis.com is forbidden: User "x" cannot create', false],
    ['', false],
    [undefined, false],
  ])('%s → %s', (message, expected) => {
    expect(isQuotaRefusalMessage(message)).toBe(expected);
  });
});

describe('isQuotaRefusalError', () => {
  it('matches a 403 carrying the quota message', () => {
    expect(isQuotaRefusalError(new ApiError(403, MILO_QUOTA_403))).toBe(true);
  });

  it('ignores an RBAC 403', () => {
    expect(isQuotaRefusalError(new ApiError(403, 'User "x" cannot create resource'))).toBe(false);
  });

  it('ignores quota wording on a non-403', () => {
    expect(isQuotaRefusalError(new ApiError(422, MILO_QUOTA_403))).toBe(false);
  });

  it('ignores non-ApiErrors', () => {
    expect(isQuotaRefusalError(new Error(MILO_QUOTA_403))).toBe(false);
  });
});

describe('isIpv4QuotaRefusal', () => {
  const refused = {
    family: 'IPv4',
    conditions: [{ type: 'Allocated', status: 'False', reason: 'AllocationFailed', message: MILO_QUOTA_403 }],
  };

  it('matches an unallocated IPv4 address refused on quota', () => {
    expect(isIpv4QuotaRefusal(refused)).toBe(true);
  });

  it('reads the address message when there is no Allocated condition', () => {
    expect(isIpv4QuotaRefusal({ family: 'IPv4', message: MILO_QUOTA_403 })).toBe(true);
  });

  it('ignores IPv6', () => {
    expect(isIpv4QuotaRefusal({ ...refused, family: 'IPv6' })).toBe(false);
  });

  it('ignores an address that has a value', () => {
    expect(isIpv4QuotaRefusal({ ...refused, value: '203.0.113.7' })).toBe(false);
  });

  it('ignores an Allocated condition that is not False', () => {
    expect(
      isIpv4QuotaRefusal({
        family: 'IPv4',
        conditions: [{ type: 'Allocated', status: 'True', message: MILO_QUOTA_403 }],
      })
    ).toBe(false);
  });

  it('ignores other allocation failures', () => {
    expect(
      isIpv4QuotaRefusal({
        family: 'IPv4',
        conditions: [
          { type: 'Allocated', status: 'False', reason: 'AddressPoolExhausted', message: 'IPPool "x" is exhausted' },
        ],
      })
    ).toBe(false);
  });
});

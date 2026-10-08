import {
  holderAvailableReasonToLabel,
  holderAvailableStatusToLabel,
  httpProxyProgrammedReasonToLabel,
  httpProxyProgrammedStatusToLabel,
  matchesLabelSelector,
  networkInterfacePrimaryAddress,
  networkListSchema,
  networkResourceSchema,
  networkServiceMembersResolvedReasonToLabel,
  networkServiceMembersResolvedStatusToLabel,
  networkServiceReadyReasonToLabel,
  networkServiceReadyStatusToLabel,
  readyReasonToLabel,
  readyStatusToBadgeType,
  readyStatusToLabel,
  subnetCidr,
  subnetReadyReasonToLabel,
  type NetworkInterface,
  type Subnet,
} from './schema';
import { describe, expect, it } from 'vitest';

const wellFormed = {
  uid: 'abc-123',
  name: 'default',
  namespace: 'default',
  resourceVersion: '42',
  createdAt: '2026-01-01T00:00:00Z',
  ipv6Prefix: 'fd20:1234:5678::/48',
  readyStatus: 'True',
  readyReason: 'Ready',
  readyMessage: 'The network is ready for use.',
  ipFamilies: ['IPv6'],
  ipamMode: 'Auto',
  mtu: 1460,
  conditions: [
    {
      type: 'Ready',
      status: 'True',
      reason: 'Ready',
      message: 'The network is ready for use.',
    },
  ],
};

describe('networkResourceSchema', () => {
  it('parses a well-formed Network', () => {
    const result = networkResourceSchema.safeParse(wellFormed);
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.createdAt).toBeInstanceOf(Date);
      expect(result.data.readyStatus).toBe('True');
    }
  });

  it('parses the minimal required shape, defaulting array fields', () => {
    const result = networkResourceSchema.safeParse({
      uid: 'abc',
      name: 'default',
      createdAt: '2026-01-01T00:00:00Z',
      readyStatus: 'Unknown',
    });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.ipFamilies).toEqual([]);
      expect(result.data.conditions).toEqual([]);
    }
  });

  it('rejects malformed Network JSON: missing required fields', () => {
    const result = networkResourceSchema.safeParse({ name: 'default' });
    expect(result.success).toBe(false);
  });

  it('rejects an invalid readyStatus enum value', () => {
    const result = networkResourceSchema.safeParse({ ...wellFormed, readyStatus: 'Sideways' });
    expect(result.success).toBe(false);
  });

  it('rejects an invalid ipFamilies entry', () => {
    const result = networkResourceSchema.safeParse({ ...wellFormed, ipFamilies: ['IPv9'] });
    expect(result.success).toBe(false);
  });

  it('rejects a non-coercible createdAt', () => {
    const result = networkResourceSchema.safeParse({ ...wellFormed, createdAt: 'not-a-date' });
    expect(result.success).toBe(false);
  });
});

describe('networkListSchema', () => {
  it('parses a list of well-formed items', () => {
    const result = networkListSchema.safeParse({ items: [wellFormed] });
    expect(result.success).toBe(true);
  });

  it('parses an empty list', () => {
    const result = networkListSchema.safeParse({ items: [] });
    expect(result.success).toBe(true);
  });
});

describe('readyStatusToBadgeType', () => {
  it('maps True to success', () => {
    expect(readyStatusToBadgeType('True')).toBe('success');
  });
  it('maps False to danger', () => {
    expect(readyStatusToBadgeType('False')).toBe('danger');
  });
  it('maps Unknown to muted', () => {
    expect(readyStatusToBadgeType('Unknown')).toBe('muted');
  });
});

describe('readyReasonToLabel', () => {
  it('maps known reasons to plain language', () => {
    expect(readyReasonToLabel('IPv6Required')).toBe('IPv6 required');
    expect(readyReasonToLabel('ProjectNamespaceNotFound')).toBe('Project namespace not found');
    expect(readyReasonToLabel('ProjectUnresolved')).toBe('Project could not be resolved');
    expect(readyReasonToLabel('RangeOccupied')).toBe('Address range still in use');
    expect(readyReasonToLabel('RangeUnsupported')).toBe('Address range not supported');
  });

  it('falls back to the raw reason for an unmapped value', () => {
    expect(readyReasonToLabel('SomethingNew')).toBe('SomethingNew');
  });

  it('falls back to a generic label when no reason is present', () => {
    expect(readyReasonToLabel(undefined)).toBe('Not ready');
  });
});

describe('readyStatusToLabel', () => {
  it('returns "Ready" for True regardless of reason', () => {
    expect(readyStatusToLabel('True', 'Ready')).toBe('Ready');
  });

  it('returns plain-language reason for False', () => {
    expect(readyStatusToLabel('False', 'IPv6Required')).toBe('IPv6 required');
  });

  it('returns "Unknown" for Unknown status', () => {
    expect(readyStatusToLabel('Unknown', undefined)).toBe('Unknown');
  });
});

describe('subnetReadyReasonToLabel', () => {
  it('maps known reasons to plain language', () => {
    expect(subnetReadyReasonToLabel('NotProgrammed')).toBe('Not yet programmed');
    expect(subnetReadyReasonToLabel('ProgrammingInProgress')).toBe('Programming in progress');
  });

  it('falls back to the raw reason for an unmapped value', () => {
    expect(subnetReadyReasonToLabel('SomethingNew')).toBe('SomethingNew');
  });

  it('falls back to a generic label when no reason is present', () => {
    expect(subnetReadyReasonToLabel(undefined)).toBe('Not ready');
  });
});

describe('subnetCidr', () => {
  function makeSubnet(overrides: Partial<Subnet>): Subnet {
    return { uid: 'u', name: 'n', createdAt: new Date(), readyStatus: 'True', ...overrides };
  }

  it('joins startAddress and prefixLength', () => {
    expect(subnetCidr(makeSubnet({ startAddress: 'fd20:0:2::', prefixLength: 64 }))).toBe(
      'fd20:0:2::/64'
    );
  });

  it('returns undefined when startAddress is missing', () => {
    expect(subnetCidr(makeSubnet({ prefixLength: 64 }))).toBeUndefined();
  });

  it('returns undefined when prefixLength is missing', () => {
    expect(subnetCidr(makeSubnet({ startAddress: 'fd20:0:2::' }))).toBeUndefined();
  });
});

describe('holderAvailableReasonToLabel', () => {
  it('maps known reasons to plain language', () => {
    expect(holderAvailableReasonToLabel('HolderUnavailable')).toBe('Not serving');
    expect(holderAvailableReasonToLabel('HolderReleased')).toBe('Holder released');
  });

  it('humanizes an opaque, holder-defined PascalCase reason rather than showing it verbatim', () => {
    expect(holderAvailableReasonToLabel('ImageUnavailable')).toBe('Image unavailable');
    expect(holderAvailableReasonToLabel('CrashLoopBackOff')).toBe('Crash loop back off');
  });

  it('falls back to a generic label when no reason is present', () => {
    expect(holderAvailableReasonToLabel(undefined)).toBe('Not serving');
  });
});

describe('holderAvailableStatusToLabel', () => {
  it('returns "Serving" for True regardless of reason', () => {
    expect(holderAvailableStatusToLabel('True', 'HolderAvailable')).toBe('Serving');
  });

  it('returns plain-language reason for False', () => {
    expect(holderAvailableStatusToLabel('False', 'HolderUnavailable')).toBe('Not serving');
  });

  it('returns "Unknown" for Unknown status', () => {
    expect(holderAvailableStatusToLabel('Unknown', undefined)).toBe('Unknown');
  });
});

describe('networkInterfacePrimaryAddress', () => {
  function makeInterface(overrides: Partial<NetworkInterface> = {}): NetworkInterface {
    return {
      uid: 'u',
      name: 'n',
      createdAt: new Date(),
      labels: {},
      addresses: [],
      holderAvailableStatus: 'Unknown',
      ...overrides,
    };
  }

  it('prefers the address marked primary', () => {
    const iface = makeInterface({
      addresses: [
        { family: 'IPv4', address: '10.0.0.1/32', primary: false },
        { family: 'IPv6', address: 'fd20::1/128', primary: true },
      ],
    });
    expect(networkInterfacePrimaryAddress(iface)).toBe('fd20::1/128');
  });

  it('falls back to the first address when none is marked primary', () => {
    const iface = makeInterface({ addresses: [{ family: 'IPv4', address: '10.0.0.1/32' }] });
    expect(networkInterfacePrimaryAddress(iface)).toBe('10.0.0.1/32');
  });

  it('returns undefined when there are no addresses', () => {
    expect(networkInterfacePrimaryAddress(makeInterface())).toBeUndefined();
  });
});

describe('networkServiceMembersResolvedReasonToLabel', () => {
  it('maps known reasons to plain language', () => {
    expect(networkServiceMembersResolvedReasonToLabel('NoMatchingInterfaces')).toBe(
      'No matching interfaces yet'
    );
    expect(networkServiceMembersResolvedReasonToLabel('MultipleNetworks')).toBe(
      'Selector matches interfaces on more than one network'
    );
  });

  it('falls back to the raw reason for an unmapped value', () => {
    expect(networkServiceMembersResolvedReasonToLabel('SomethingNew')).toBe('SomethingNew');
  });

  it('falls back to a generic label when no reason is present', () => {
    expect(networkServiceMembersResolvedReasonToLabel(undefined)).toBe('Not resolved');
  });
});

describe('networkServiceMembersResolvedStatusToLabel', () => {
  it('returns "Resolved" for True regardless of reason', () => {
    expect(networkServiceMembersResolvedStatusToLabel('True', 'Resolved')).toBe('Resolved');
  });

  it('returns plain-language reason for False', () => {
    expect(networkServiceMembersResolvedStatusToLabel('False', 'NoMatchingInterfaces')).toBe(
      'No matching interfaces yet'
    );
  });

  it('returns "Unknown" for Unknown status', () => {
    expect(networkServiceMembersResolvedStatusToLabel('Unknown', undefined)).toBe('Unknown');
  });
});

describe('networkServiceReadyReasonToLabel', () => {
  it('maps known reasons to plain language', () => {
    expect(networkServiceReadyReasonToLabel('NoServingLocations')).toBe(
      'No location is taking traffic'
    );
  });

  it('falls back to the raw reason for an unmapped value', () => {
    expect(networkServiceReadyReasonToLabel('SomethingNew')).toBe('SomethingNew');
  });

  it('falls back to a generic label when no reason is present', () => {
    expect(networkServiceReadyReasonToLabel(undefined)).toBe('Not ready');
  });
});

describe('networkServiceReadyStatusToLabel', () => {
  it('returns "Ready" for True regardless of reason', () => {
    expect(networkServiceReadyStatusToLabel('True', 'Ready')).toBe('Ready');
  });

  it('returns plain-language reason for False', () => {
    expect(networkServiceReadyStatusToLabel('False', 'NoServingLocations')).toBe(
      'No location is taking traffic'
    );
  });

  it('returns "Unknown" for Unknown status', () => {
    expect(networkServiceReadyStatusToLabel('Unknown', undefined)).toBe('Unknown');
  });
});

describe('httpProxyProgrammedReasonToLabel', () => {
  it('maps known reasons to plain language', () => {
    expect(httpProxyProgrammedReasonToLabel('InstanceBackendNotFound')).toBe('Backend not found');
  });

  it('falls back to the raw reason for an unmapped value', () => {
    expect(httpProxyProgrammedReasonToLabel('SomethingNew')).toBe('SomethingNew');
  });

  it('falls back to a generic label when no reason is present', () => {
    expect(httpProxyProgrammedReasonToLabel(undefined)).toBe('Not programmed');
  });
});

describe('httpProxyProgrammedStatusToLabel', () => {
  it('returns "Programmed" for True regardless of reason', () => {
    expect(httpProxyProgrammedStatusToLabel('True', 'Programmed')).toBe('Programmed');
  });

  it('returns plain-language reason for False', () => {
    expect(httpProxyProgrammedStatusToLabel('False', 'Conflict')).toBe('Conflict while programming');
  });

  it('returns "Unknown" for Unknown status', () => {
    expect(httpProxyProgrammedStatusToLabel('Unknown', undefined)).toBe('Unknown');
  });
});

describe('matchesLabelSelector', () => {
  it('matches when every matchLabels entry equals the label set', () => {
    const labels = { 'compute.datumapis.com/workload-name': 'kevload', 'networking.datumapis.com/location': 'us-central-1' };
    expect(
      matchesLabelSelector(labels, {
        matchLabels: { 'compute.datumapis.com/workload-name': 'kevload' },
        matchExpressions: [],
      })
    ).toBe(true);
  });

  it('does not match when a matchLabels entry differs', () => {
    const labels = { 'compute.datumapis.com/workload-name': 'kevload' };
    expect(
      matchesLabelSelector(labels, {
        matchLabels: { 'compute.datumapis.com/workload-name': 'hello-kevin' },
        matchExpressions: [],
      })
    ).toBe(false);
  });

  it('does not match when a matchLabels key is absent from the label set', () => {
    expect(
      matchesLabelSelector(
        {},
        { matchLabels: { 'compute.datumapis.com/workload-name': 'kevload' }, matchExpressions: [] }
      )
    ).toBe(false);
  });

  it('evaluates an In requirement', () => {
    const selector = {
      matchLabels: {},
      matchExpressions: [{ key: 'networking.datumapis.com/location', operator: 'In' as const, values: ['us-central-1', 'us-east-1'] }],
    };
    expect(matchesLabelSelector({ 'networking.datumapis.com/location': 'us-central-1' }, selector)).toBe(true);
    expect(matchesLabelSelector({ 'networking.datumapis.com/location': 'eu-west-1' }, selector)).toBe(false);
    expect(matchesLabelSelector({}, selector)).toBe(false);
  });

  it('evaluates a NotIn requirement', () => {
    const selector = {
      matchLabels: {},
      matchExpressions: [{ key: 'networking.datumapis.com/location', operator: 'NotIn' as const, values: ['us-central-1'] }],
    };
    expect(matchesLabelSelector({ 'networking.datumapis.com/location': 'eu-west-1' }, selector)).toBe(true);
    expect(matchesLabelSelector({ 'networking.datumapis.com/location': 'us-central-1' }, selector)).toBe(false);
    expect(matchesLabelSelector({}, selector)).toBe(true);
  });

  it('evaluates Exists and DoesNotExist requirements', () => {
    const exists = {
      matchLabels: {},
      matchExpressions: [{ key: 'compute.datumapis.com/workload-name', operator: 'Exists' as const, values: [] }],
    };
    expect(matchesLabelSelector({ 'compute.datumapis.com/workload-name': 'kevload' }, exists)).toBe(true);
    expect(matchesLabelSelector({}, exists)).toBe(false);

    const doesNotExist = {
      matchLabels: {},
      matchExpressions: [{ key: 'compute.datumapis.com/workload-name', operator: 'DoesNotExist' as const, values: [] }],
    };
    expect(matchesLabelSelector({}, doesNotExist)).toBe(true);
    expect(matchesLabelSelector({ 'compute.datumapis.com/workload-name': 'kevload' }, doesNotExist)).toBe(false);
  });

  it('requires every clause to match, not just one', () => {
    const selector = {
      matchLabels: { 'compute.datumapis.com/workload-name': 'kevload' },
      matchExpressions: [{ key: 'networking.datumapis.com/location', operator: 'In' as const, values: ['us-central-1'] }],
    };
    expect(
      matchesLabelSelector(
        { 'compute.datumapis.com/workload-name': 'kevload', 'networking.datumapis.com/location': 'us-east-1' },
        selector
      )
    ).toBe(false);
  });
});

import { NETWORK_NAME_MESSAGE, networkFormSchema, toCreateNetworkInput } from './network-form';
import { describe, expect, it } from 'vitest';

describe('networkFormSchema', () => {
  it.each([
    ['a simple name', { name: 'default' }],
    ['a hyphenated name', { name: 'my-net-1' }],
    ['a single character', { name: 'a' }],
    ['63 characters', { name: 'a'.repeat(63) }],
    ['a custom MTU', { name: 'net', mtu: 1500 }],
    ['the MTU bounds', { name: 'net', mtu: 1300 }],
  ])('accepts %s', (_label, input) => {
    expect(networkFormSchema.safeParse(input).success).toBe(true);
  });

  it.each([
    ['uppercase', { name: 'Default' }, NETWORK_NAME_MESSAGE],
    ['a leading hyphen', { name: '-net' }, NETWORK_NAME_MESSAGE],
    ['a trailing hyphen', { name: 'net-' }, NETWORK_NAME_MESSAGE],
    ['spaces', { name: 'my net' }, NETWORK_NAME_MESSAGE],
    ['64 characters', { name: 'a'.repeat(64) }, 'At most 63 characters.'],
    ['a missing name', {}, 'Name is required.'],
    ['an MTU below range', { name: 'net', mtu: 1299 }, 'Must be between 1300 and 8856.'],
    ['an MTU above range', { name: 'net', mtu: 8857 }, 'Must be between 1300 and 8856.'],
    ['a fractional MTU', { name: 'net', mtu: 1400.5 }, 'Enter a whole number.'],
  ])('rejects %s', (_label, input, message) => {
    const result = networkFormSchema.safeParse(input);
    expect(result.success).toBe(false);
    expect(result.error?.issues.map((issue) => issue.message)).toContain(message);
  });
});

describe('toCreateNetworkInput', () => {
  it('creates an IPv6-only network and passes the MTU through', () => {
    expect(toCreateNetworkInput({ name: 'net', mtu: 1500 })).toEqual({
      name: 'net',
      ipFamilies: ['IPv6'],
      mtu: 1500,
    });
  });

  it('leaves the MTU unset when blank', () => {
    expect(toCreateNetworkInput({ name: 'net' }).mtu).toBeUndefined();
  });
});

import { IpFamilyField, locationsWarning } from './ip-family-field';
import { deriveIpv4Availability, IPV4_COPY, type Ipv4Availability } from '../lib/ipv4-quota';
import { IPV4_VERDICTS } from '../test/portal-host';
import { Form } from '@datum-cloud/datum-ui/form';
import { ConformAdapter } from '@datum-cloud/datum-ui/form/adapters/conform';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { z } from 'zod';

const schema = z.object({ ipv4: z.boolean().optional() });

function renderField(
  availability: Ipv4Availability,
  props: { locations?: number; hasIpv4?: boolean; ipv4?: boolean } = {}
) {
  const onRequest = vi.fn();
  render(
    <ConformAdapter>
      <Form.Root schema={schema} defaultValues={{ ipv4: props.ipv4 ?? false }} onSubmit={vi.fn()}>
        <IpFamilyField
          availability={availability}
          onRequest={onRequest}
          locations={props.locations}
          hasIpv4={props.hasIpv4}
        />
      </Form.Root>
    </ConformAdapter>
  );
  return { onRequest };
}

const ipv4Box = () => screen.getByRole('checkbox', { name: IPV4_COPY.optionLabel });
const available = deriveIpv4Availability(IPV4_VERDICTS.available);

describe('IpFamilyField', () => {
  it('keeps IPv6 on and offers IPv4 unchecked when addresses are left', () => {
    renderField(available);
    expect(screen.getByRole('checkbox', { name: 'IPv6' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'IPv6' })).toBeDisabled();
    expect(ipv4Box()).toBeEnabled();
    expect(ipv4Box()).not.toBeChecked();
    expect(screen.queryByText(IPV4_COPY.paidAddOn)).not.toBeInTheDocument();
  });

  it('disables IPv4 with only a checking note while loading', () => {
    renderField(deriveIpv4Availability(IPV4_VERDICTS.loading));
    expect(ipv4Box()).toBeDisabled();
    expect(screen.queryByText(IPV4_COPY.paidAddOn)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: IPV4_COPY.requestAction })).not.toBeInTheDocument();
  });

  it.each([
    ['missing or unreadable', IPV4_VERDICTS.missing, IPV4_COPY.noAllocation],
    ['zero allowed', IPV4_VERDICTS.noAllocation, IPV4_COPY.noAllocation],
    ['used up', IPV4_VERDICTS.usedUp, IPV4_COPY.usedUp],
  ] as const)('disables IPv4 and explains it in order: %s', (_, verdict, reason) => {
    const { onRequest } = renderField(deriveIpv4Availability(verdict));
    expect(ipv4Box()).toBeDisabled();
    const lines = [reason, IPV4_COPY.paidAddOn, IPV4_COPY.requestAction].map((t) => screen.getByText(t));
    for (let i = 1; i < lines.length; i++) {
      expect(lines[i - 1].compareDocumentPosition(lines[i]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
    fireEvent.click(screen.getByRole('button', { name: IPV4_COPY.requestAction }));
    expect(onRequest).toHaveBeenCalledOnce();
  });

  it('warns only once IPv4 is checked for more locations than addresses left', () => {
    const { onRequest } = renderField(available, { locations: 3 });
    expect(screen.queryByText(locationsWarning(3, 1))).not.toBeInTheDocument();
    fireEvent.click(ipv4Box());
    expect(screen.getByText(locationsWarning(3, 1))).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: IPV4_COPY.requestAction }));
    expect(onRequest).toHaveBeenCalledOnce();
  });

  it('doesn’t warn when every location can get an address', () => {
    renderField(available, { locations: 1, ipv4: true });
    expect(ipv4Box()).toBeChecked();
    expect(screen.queryByRole('button', { name: IPV4_COPY.requestAction })).not.toBeInTheDocument();
  });

  it('keeps an address’s existing IPv4 checked and changeable whatever the quota says', () => {
    renderField(deriveIpv4Availability(IPV4_VERDICTS.usedUp), { hasIpv4: true, ipv4: true, locations: 3 });
    expect(ipv4Box()).toBeEnabled();
    expect(ipv4Box()).toBeChecked();
    expect(screen.queryByText(IPV4_COPY.usedUp)).not.toBeInTheDocument();
    expect(screen.queryByText(/won’t get an IPv4 address/)).not.toBeInTheDocument();
  });

  it('words the warning for one or several addresses left', () => {
    expect(locationsWarning(3, 1)).toContain('1 public IPv4 address left');
    expect(locationsWarning(4, 2)).toContain('2 public IPv4 addresses left');
  });
});

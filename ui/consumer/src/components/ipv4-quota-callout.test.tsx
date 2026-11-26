import { IPV4_RETRY_COPY, Ipv4QuotaCallout } from './ipv4-quota-callout';
import { buildIpv4QuotaIncreaseRequest, IPV4_COPY } from '../lib/ipv4-quota';
import { fakePortalHost } from '../test/portal-host';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

function renderCallout(issues: Parameters<typeof Ipv4QuotaCallout>[0]['issues']) {
  const host = fakePortalHost();
  const view = render(
    <host.Wrapper>
      <Ipv4QuotaCallout nlbName="edge-nlb" issues={issues} />
    </host.Wrapper>
  );
  return { host, ...view };
}

describe('Ipv4QuotaCallout', () => {
  it('renders nothing without a problem', () => {
    const { container } = renderCallout({ refused: [], failed: [] });
    expect(container).toBeEmptyDOMElement();
  });

  it('names every refused location and requests IPv4 for the NLB', () => {
    const { host } = renderCallout({ refused: ['iad', 'lhr'], failed: [] });
    expect(screen.getByText('Affected locations:', { exact: false })).toHaveTextContent(
      'Affected locations: iad, lhr'
    );
    expect(screen.getByText(IPV4_RETRY_COPY)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: IPV4_COPY.requestAction }));
    expect(host.requestQuotaIncrease).toHaveBeenCalledWith(
      buildIpv4QuotaIncreaseRequest('edge-nlb')
    );
  });

  it('shows a failure without a message as just its location', () => {
    renderCallout({ refused: [], failed: [{ location: 'iad' }] });
    expect(screen.getByTestId('networking-plugin-nlb-ipv4-failed')).toHaveTextContent('iad');
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});

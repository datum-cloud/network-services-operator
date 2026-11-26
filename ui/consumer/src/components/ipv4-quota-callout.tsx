import type { Ipv4Issues } from './nlb-overview-model';
import { IPV4_COPY, useRequestIpv4 } from '../lib/ipv4-quota';
import { Alert, AlertDescription, AlertTitle } from '@datum-cloud/datum-ui/alert';
import { Button } from '@datum-cloud/datum-ui/button';

export const IPV4_RETRY_COPY =
  'It will be assigned automatically once more IPv4 quota is granted. No change to the load balancer is needed.';

export const IPV4_FAILED_COPY =
  'This load balancer couldn’t get a public IPv4 address. Its IPv6 address isn’t affected.';

function locationList(locations: string[]) {
  return locations.join(', ');
}

function QuotaRefusal({ nlbName, locations }: { nlbName: string; locations: string[] }) {
  const requestIpv4 = useRequestIpv4();
  return (
    <Alert variant="warning" data-testid="networking-plugin-nlb-ipv4-quota">
      <AlertTitle>Public IPv4 address not assigned</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-1.5">
        <p>{IPV4_COPY.quotaRefused}</p>
        <p>
          {locations.length === 1 ? 'Affected location: ' : 'Affected locations: '}
          <span className="font-mono">{locationList(locations)}</span>
        </p>
        <p>{IPV4_RETRY_COPY}</p>
        <p>{IPV4_COPY.paidAddOn}</p>
        <Button
          type="primary"
          theme="outline"
          size="xs"
          htmlType="button"
          onClick={() => requestIpv4(nlbName)}>
          {IPV4_COPY.requestAction}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function AllocationFailure({ failures }: { failures: Ipv4Issues['failed'] }) {
  return (
    <Alert variant="destructive" data-testid="networking-plugin-nlb-ipv4-failed">
      <AlertTitle>Public IPv4 address not assigned</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-1.5">
        <p>{IPV4_FAILED_COPY}</p>
        <ul className="flex flex-col gap-0.5">
          {failures.map((f) => (
            <li key={f.location}>
              <span className="font-mono">{f.location}</span>
              {f.message ? `: ${f.message}` : ''}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  );
}

export function Ipv4QuotaCallout({ nlbName, issues }: { nlbName: string; issues: Ipv4Issues }) {
  if (issues.refused.length === 0 && issues.failed.length === 0) return null;
  return (
    <div className="flex flex-col gap-3">
      {issues.refused.length > 0 && <QuotaRefusal nlbName={nlbName} locations={issues.refused} />}
      {issues.failed.length > 0 && <AllocationFailure failures={issues.failed} />}
    </div>
  );
}

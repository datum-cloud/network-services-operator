import { albHref, computeWorkloadsHref, useNetworkServices } from '../lib/api';
import type { NetworkService, NlbProtocol } from '../schema';
import { CommandBlock } from './cli-section';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { Button } from '@datum-cloud/datum-ui/button';
import { Card } from '@datum-cloud/datum-ui/card';
import { Icon } from '@datum-cloud/datum-ui/icons';
import {
  ArrowRightIcon,
  BoxIcon,
  CheckIcon,
  DatabaseIcon,
  Gamepad2Icon,
  GlobeIcon,
  NetworkIcon,
  PlugIcon,
  PlusIcon,
  SplitIcon,
  type LucideIcon,
} from 'lucide-react';
import { useId } from 'react';
import { Link } from 'react-router';

/** What a starter card fills in on the create form. The user still picks the backend. */
export interface NlbCreatePreset {
  protocol: Exclude<NlbProtocol, 'TLS'>;
  port: number;
}

interface NlbStarter extends NlbCreatePreset {
  id: string;
  icon: LucideIcon;
  title: string;
  description: string;
}

export const NLB_STARTERS: readonly NlbStarter[] = [
  {
    id: 'database',
    icon: DatabaseIcon,
    title: 'Database',
    description: 'Postgres, MySQL, Redis',
    protocol: 'TCP',
    port: 5432,
  },
  {
    id: 'game-server',
    icon: Gamepad2Icon,
    title: 'Game server',
    description: 'Real-time player traffic',
    protocol: 'UDP',
    port: 7777,
  },
  {
    id: 'dns',
    icon: GlobeIcon,
    title: 'DNS',
    description: 'Authoritative or recursive',
    protocol: 'UDP',
    port: 53,
  },
  {
    id: 'any-tcp',
    icon: PlugIcon,
    title: 'Any TCP service',
    description: 'Bring your own TLS',
    protocol: 'TCP',
    port: 443,
  },
];

const NLB_FITS = [
  'Clients speak TCP or UDP, not HTTP',
  'You need a fixed port like 5432, 53 or 7777',
  'The backend handles its own TLS',
];

const ALB_FITS = [
  'You route by hostname, path or header',
  'Datum should terminate TLS for your domain',
  'You want redirects, rewrites or canary splits',
];

// How many ready backends the line names before it switches to "+N more".
const NAMED_BACKENDS = 3;

// Same colour as the primary button, in both themes. See network-home-hero.tsx.
const ACCENT_TEXT = { color: 'var(--btn-primary)' };

/** A listener's protocol, and its port unless a column beside it already shows it. */
export function ProtocolBadge({ protocol, port }: { protocol: NlbProtocol; port?: number }) {
  return (
    <Badge
      type={protocol === 'UDP' ? 'quaternary' : 'info'}
      theme="light"
      className="font-mono text-xs">
      {port === undefined ? protocol : `${protocol} ${port}`}
    </Badge>
  );
}

// Rendered by the NLB list page while the project has no NLB (mockup N0).
export function NlbEmptyState({
  projectId,
  onCreate,
}: {
  projectId: string | undefined;
  onCreate: (preset?: NlbCreatePreset) => void;
}) {
  const startersId = useId();

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-nlb-empty">
      <Card className="flex flex-col items-center gap-6 px-6 py-10 text-center sm:px-10">
        <span
          className="bg-muted flex size-14 items-center justify-center rounded-xl"
          style={ACCENT_TEXT}>
          <Icon icon={NetworkIcon} size={24} />
        </span>

        <div className="flex max-w-2xl flex-col gap-3">
          <h2 className="font-title text-3xl leading-tight">
            Forward TCP and UDP straight to a workload
          </h2>
          <p className="text-muted-foreground text-sm leading-relaxed">
            A network load balancer gives you public IPv6 addresses and forwards each connection on
            the ports you choose to one backend. It never looks inside the traffic. For hostnames,
            paths or TLS, use an ALB instead.
          </p>
        </div>

        <TrafficPath />

        <div className="flex w-full max-w-xl flex-col items-stretch gap-3 sm:flex-row sm:items-center">
          <Button
            type="primary"
            theme="solid"
            htmlType="button"
            className="shrink-0"
            onClick={() => onCreate()}>
            <Icon icon={PlusIcon} className="size-4" />
            Create load balancer
          </Button>
          <div className="min-w-0 flex-1 text-left">
            <CommandBlock value="datumctl apply -f nlb.yaml" />
          </div>
        </div>

        <BackendLine projectId={projectId} />

        <section
          aria-labelledby={startersId}
          className="border-border flex w-full flex-col gap-3 border-t pt-6 text-left">
          <p className="text-xs">
            <span id={startersId} className="font-semibold tracking-wider uppercase">
              Start from a common setup
            </span>
            <span className="text-muted-foreground ml-2">
              prefills the form, you pick the backend
            </span>
          </p>
          <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {NLB_STARTERS.map((starter) => (
              <li key={starter.id}>
                <button
                  type="button"
                  data-testid={`networking-plugin-nlb-starter-${starter.id}`}
                  onClick={() => onCreate({ protocol: starter.protocol, port: starter.port })}
                  className="border-border bg-background hover:bg-muted flex h-full w-full flex-col gap-2 rounded-lg border p-4 text-left transition-colors">
                  <span className="flex items-center gap-2">
                    <Icon icon={starter.icon} size={16} style={ACCENT_TEXT} />
                    <span className="flex-1 text-sm font-medium">{starter.title}</span>
                    <ProtocolBadge protocol={starter.protocol} port={starter.port} />
                  </span>
                  <span className="text-muted-foreground text-xs">{starter.description}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      </Card>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <ComparisonCard
          icon={NetworkIcon}
          title="Use an NLB when"
          layer="Layer 4"
          fits={NLB_FITS}
        />
        <ComparisonCard
          icon={SplitIcon}
          title="Use an ALB when"
          layer="Layer 7"
          fits={ALB_FITS}
          action={
            projectId && (
              <Link
                to={albHref(projectId)}
                className="flex shrink-0 items-center gap-1 text-sm font-medium"
                style={ACCENT_TEXT}>
                Go to ALB
                <Icon icon={ArrowRightIcon} size={14} />
              </Link>
            )
          }
        />
      </div>
    </div>
  );
}

function TrafficPath() {
  const steps = [
    { icon: GlobeIcon, title: 'Client', detail: '[2001:db8::1]:5432' },
    { icon: NetworkIcon, title: 'Listener', detail: 'TCP 5432' },
    { icon: BoxIcon, title: 'Backend', detail: 'postgres :pg' },
  ];

  return (
    <ol aria-label="How traffic flows" className="flex flex-col items-center gap-2 sm:flex-row">
      {steps.map((step, i) => (
        <li key={step.title} className="flex flex-col items-center gap-2 sm:flex-row">
          {i > 0 && (
            <Icon
              icon={ArrowRightIcon}
              size={16}
              className="text-muted-foreground hidden sm:block"
            />
          )}
          <span className="border-border bg-background flex items-center gap-3 rounded-lg border px-4 py-2 text-left">
            <Icon icon={step.icon} size={18} className="text-muted-foreground" />
            <span>
              <span className="block text-sm font-medium">{step.title}</span>
              <span className="text-muted-foreground block font-mono text-xs">{step.detail}</span>
            </span>
          </span>
        </li>
      ))}
    </ol>
  );
}

function BackendLine({ projectId }: { projectId: string | undefined }) {
  // Only the backend line depends on this query, so it fails quietly and the
  // rest of the empty state still renders.
  const { data: services, isLoading, error } = useNetworkServices(projectId);
  if (isLoading || error || !services) return null;

  return (
    <p data-testid="networking-plugin-nlb-backends" className="text-muted-foreground text-sm">
      <BackendSummary projectId={projectId} services={services} /> <span className="mx-1">·</span>{' '}
      IPv4 coming soon
    </p>
  );
}

function BackendSummary({
  projectId,
  services,
}: {
  projectId: string | undefined;
  services: NetworkService[];
}) {
  const ready = services.filter((s) => s.readyStatus === 'True');

  if (services.length === 0) {
    return (
      <>
        No backends yet.{' '}
        {projectId && (
          <Link to={computeWorkloadsHref(projectId)} className="font-medium" style={ACCENT_TEXT}>
            Deploy a workload
          </Link>
        )}
      </>
    );
  }

  if (ready.length === 0) {
    return (
      <>{services.length === 1 ? '1 backend' : `${services.length} backends`}, none ready yet</>
    );
  }

  const named = ready.slice(0, NAMED_BACKENDS);
  const more = ready.length - named.length;
  return (
    <>
      <span className="mr-2 inline-block size-2 rounded-full bg-green-600" aria-hidden />
      {ready.length === 1 ? '1 backend' : `${ready.length} backends`} ready to use:{' '}
      {named.map((s, i) => (
        <span key={s.uid}>
          {i > 0 && ', '}
          <code className="text-foreground font-mono">{s.name}</code>
        </span>
      ))}
      {more > 0 && ` +${more} more`}
    </>
  );
}

function ComparisonCard({
  icon,
  title,
  layer,
  fits,
  action,
}: {
  icon: LucideIcon;
  title: string;
  layer: string;
  fits: string[];
  action?: React.ReactNode;
}) {
  return (
    <Card className="flex flex-col gap-3 p-6">
      <div className="flex items-center gap-2">
        <Icon icon={icon} size={16} className="text-muted-foreground" />
        <h3 className="text-sm font-semibold">{title}</h3>
        <span className="text-muted-foreground font-mono text-xs">{layer}</span>
        <span className="flex-1" />
        {action}
      </div>
      <ul className="flex flex-col gap-2">
        {fits.map((fit) => (
          <li key={fit} className="text-muted-foreground flex items-start gap-2 text-sm">
            <Icon icon={CheckIcon} size={14} className="mt-0.5 shrink-0 text-green-600" />
            {fit}
          </li>
        ))}
      </ul>
    </Card>
  );
}

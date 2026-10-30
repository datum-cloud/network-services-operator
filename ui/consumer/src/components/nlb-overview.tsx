import { ProtocolBadge } from './nlb-empty-state';
import {
  overviewSummary,
  toBackendCards,
  toListenerRows,
  type BackendCard,
  type ListenerRow,
  type ListenerStatus,
} from './nlb-overview-model';
import type { NetworkLoadBalancer, NetworkLoadBalancerRoute, NetworkService } from '../schema';
import { Button } from '@datum-cloud/datum-ui/button';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { cn } from '@datum-cloud/datum-ui/utils';
import {
  ArrowRightIcon,
  BoxIcon,
  ChevronRightIcon,
  GlobeIcon,
  InfoIcon,
  NetworkIcon,
  PlusIcon,
} from 'lucide-react';
import { Link } from 'react-router';

const WORKLOAD_NAME_LABEL = 'compute.datumapis.com/workload-name';

// The plugin ships no stylesheet and the portal's CSS has no utility for a
// fixed-width column beside a fluid one, so the band's two columns are set
// here. Everything else uses utilities the portal already has.
const BAND_STYLE = `
.nlb-band { display: grid; gap: 1.5rem; }
@media (min-width: 1024px) {
  .nlb-band { grid-template-columns: 17rem minmax(0, 1fr); align-items: start; }
  .nlb-band__arrow { display: flex; }
}
.nlb-band__arrow { display: none; }
`;

const DOT: Record<ListenerStatus['type'], string> = {
  success: 'bg-green-500',
  warning: 'bg-amber-500',
  danger: 'bg-destructive',
  muted: 'bg-muted-foreground',
};

function Dot({ type }: { type: ListenerStatus['type'] }) {
  return (
    <span aria-hidden className={cn('inline-block size-1.5 shrink-0 rounded-full', DOT[type])} />
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-muted-foreground text-xs font-semibold tracking-wider uppercase">
      {children}
    </span>
  );
}

function AddressesCard({ nlb }: { nlb: NetworkLoadBalancer }) {
  return (
    <div className="flex items-center gap-3">
      <Card size="sm" className="flex-1" data-testid="networking-plugin-nlb-addresses">
        <CardContent className="flex flex-col gap-3">
          <p className="flex items-center gap-2 text-sm font-medium">
            <Icon icon={GlobeIcon} size={16} className="text-muted-foreground" />
            Public addresses
          </p>
          <dl className="flex flex-col gap-3 text-sm">
            <div>
              <dt className="text-muted-foreground text-xs font-semibold tracking-wider uppercase">
                Network
              </dt>
              <dd className="font-mono">{nlb.networkName ?? '—'}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground text-xs font-semibold tracking-wider uppercase">
                Family
              </dt>
              <dd>IPv6</dd>
            </div>
            <div>
              <dt className="text-muted-foreground text-xs font-semibold tracking-wider uppercase">
                Locations
              </dt>
              <dd>Follows backends</dd>
            </div>
          </dl>
          <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
            <Icon icon={InfoIcon} size={12} />
            IPv4 coming soon
          </p>
        </CardContent>
      </Card>
      <span className="nlb-band__arrow text-muted-foreground">
        <Icon icon={ArrowRightIcon} size={16} />
      </span>
    </div>
  );
}

function AddressCell({ row }: { row: ListenerRow }) {
  if (!row.address) return <span className="text-muted-foreground text-sm">Pending</span>;
  const others = row.locations - 1;
  return (
    <span className="flex flex-wrap items-center gap-2">
      <span className="font-mono text-xs">[{row.address}]</span>
      {others > 0 && (
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">
          +{others} {others === 1 ? 'city' : 'cities'}
        </span>
      )}
    </span>
  );
}

function SendToCell({ row, href }: { row: ListenerRow; href?: string }) {
  const chip = row.backend ? (
    <span className="bg-background inline-flex items-center gap-2 rounded-md border px-2 py-1">
      <Dot type={row.status.type} />
      <span className="font-mono text-xs">{row.backend.name}</span>
      <span className="text-muted-foreground font-mono text-xs">:{row.backend.port}</span>
    </span>
  ) : null;

  return (
    <span className="flex flex-col items-start gap-1" title={row.status.detail}>
      {chip && href ? (
        <Link to={href} className="hover:underline">
          {chip}
        </Link>
      ) : (
        chip
      )}
      {/* The dot says it all when the listener is serving; anything else is spelled out. */}
      {row.status.type === 'success' ? (
        <span className="sr-only">{row.status.label}</span>
      ) : (
        <span
          className={cn(
            'flex items-center gap-1.5 text-xs font-medium',
            row.status.type === 'danger' && 'text-destructive',
            row.status.type === 'warning' && 'text-amber-600',
            row.status.type === 'muted' && 'text-muted-foreground'
          )}>
          {!chip && <Dot type={row.status.type} />}
          {row.status.label}
        </span>
      )}
    </span>
  );
}

function HealthCheckCell({ row }: { row: ListenerRow }) {
  const check = row.backend?.healthCheck;
  if (!row.backend) return <span className="text-muted-foreground">—</span>;
  if (!check) return <span className="text-muted-foreground font-mono text-xs">None</span>;
  return (
    <span className="font-mono text-xs">
      {check.protocol}
      <span className="text-muted-foreground">
        {/* With no port, a TCP probe uses the backend's serving port. */}
        {check.port ? ` :${check.port}` : ' · serving port'}
      </span>
    </span>
  );
}

function ListenersCard({
  rows,
  backendHref,
  onAddListener,
}: {
  rows: ListenerRow[];
  backendHref?: string;
  onAddListener?: () => void;
}) {
  return (
    <div
      className="bg-card overflow-hidden rounded-lg border"
      data-testid="networking-plugin-nlb-listeners">
      <div className="flex flex-wrap items-center gap-2 border-b px-4 py-3">
        <Icon icon={NetworkIcon} size={16} className="text-muted-foreground" />
        <span className="text-sm font-medium">Listeners</span>
        <span className="text-muted-foreground text-sm">one port, one backend</span>
        <span className="flex-1" />
        {onAddListener && (
          <Button
            type="primary"
            theme="solid"
            size="small"
            htmlType="button"
            onClick={onAddListener}>
            <Icon icon={PlusIcon} className="size-4" />
            Add listener
          </Button>
        )}
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead className="text-muted-foreground text-xs tracking-wider uppercase">
            <tr className="border-b">
              <th className="px-4 py-2 font-semibold">Protocol</th>
              <th className="px-4 py-2 font-semibold">Port</th>
              <th className="px-4 py-2 font-semibold">Address</th>
              <th className="px-4 py-2 font-semibold">Send to</th>
              <th className="px-4 py-2 font-semibold">Health check</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {rows.map((row) => (
              <tr key={row.listener.name} data-listener={row.listener.name}>
                <td className="px-4 py-3">
                  <ProtocolBadge protocol={row.listener.protocol} />
                </td>
                <td className="px-4 py-3 font-mono font-medium">{row.listener.port}</td>
                <td className="px-4 py-3">
                  <AddressCell row={row} />
                </td>
                <td className="px-4 py-3">
                  <SendToCell row={row} href={backendHref} />
                </td>
                <td className="px-4 py-3">
                  <HealthCheckCell row={row} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function backendHealth(card: BackendCard): { label: string; type: ListenerStatus['type'] } {
  const summary = card.service?.summary;
  if (!card.service || !summary) return { label: 'Not found', type: 'danger' };
  // No members means the service's selector matches no interfaces yet.
  if (summary.members === 0) return { label: 'No members', type: 'danger' };
  const label = `${summary.healthy}/${summary.members} healthy`;
  if (summary.healthy === 0) return { label, type: 'danger' };
  if (summary.healthy < summary.members) return { label, type: 'warning' };
  return { label, type: 'success' };
}

function backendKind(card: BackendCard): string {
  const workload = card.service?.networkInterfaceSelector.matchLabels[WORKLOAD_NAME_LABEL];
  return workload ? 'Compute workload' : 'Service';
}

const HEALTH_TEXT: Record<ListenerStatus['type'], string> = {
  success: 'text-green-600',
  warning: 'text-amber-600',
  danger: 'text-destructive',
  muted: 'text-muted-foreground',
};

function BackendCards({ cards, href }: { cards: BackendCard[]; href?: string }) {
  if (cards.length === 0) return null;
  return (
    <section className="flex flex-col gap-3" data-testid="networking-plugin-nlb-backends">
      <p className="flex items-center gap-2">
        <Icon icon={BoxIcon} size={14} className="text-muted-foreground" />
        <SectionLabel>Backends</SectionLabel>
        <span className="text-muted-foreground text-xs">
          NetworkServices that listeners forward to
        </span>
      </p>
      <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map((card) => {
          const health = backendHealth(card);
          const body = (
            <CardContent className="flex flex-col gap-1">
              <span className="flex items-center gap-2">
                <Icon icon={BoxIcon} size={16} />
                <span className="flex-1 truncate font-mono text-sm font-medium">{card.name}</span>
                {href && (
                  <Icon icon={ChevronRightIcon} size={14} className="text-muted-foreground" />
                )}
              </span>
              <span className="text-muted-foreground text-xs">
                {backendKind(card)} · {card.ports.length === 1 ? 'port' : 'ports'}{' '}
                {card.ports.join(', ')}
              </span>
              <span className="flex items-center justify-between gap-2 text-xs">
                <span
                  className={cn('flex items-center gap-1.5 font-medium', HEALTH_TEXT[health.type])}>
                  <Dot type={health.type} />
                  {health.label}
                </span>
                <span className="text-muted-foreground">
                  {card.listeners.map((l) => `${l.protocol} ${l.port}`).join(', ')}
                </span>
              </span>
            </CardContent>
          );
          return (
            <li key={card.name} data-health={health.type}>
              <Card
                size="sm"
                className={cn(
                  health.type === 'danger' && 'border-destructive',
                  health.type === 'warning' && 'border-amber-500'
                )}>
                {href ? (
                  <Link to={href} className="block">
                    {body}
                  </Link>
                ) : (
                  body
                )}
              </Card>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

// Mockup N1, without the tabs (no issue yet), the connections chart (#805)
// and the health checks panel, which is left for later.
export function NlbOverview({
  nlb,
  routes,
  services,
  servicesHref,
  onAddListener,
}: {
  nlb: NetworkLoadBalancer;
  routes: NetworkLoadBalancerRoute[];
  services: NetworkService[];
  /** Where a backend links to: its network's services list. */
  servicesHref?: string;
  onAddListener?: () => void;
}) {
  const rows = toListenerRows(nlb, routes, services);
  const cards = toBackendCards(rows);
  const summary = overviewSummary(rows);

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-nlb-overview">
      <style>{BAND_STYLE}</style>
      {/* datum.net's look: white cards on glacier mist, with navy kept for text. */}
      <section className="bg-sidebar-accent flex flex-col gap-6 rounded-xl p-6">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <SectionLabel>Traffic path</SectionLabel>
          <span
            data-testid="networking-plugin-nlb-summary"
            className={cn(
              'bg-card flex items-center gap-2 rounded-full border px-3 py-1 text-sm font-medium',
              HEALTH_TEXT[summary.type]
            )}>
            <Dot type={summary.type} />
            {summary.label}
          </span>
        </div>

        <div className="nlb-band">
          <AddressesCard nlb={nlb} />
          <div className="flex min-w-0 flex-col gap-6">
            <ListenersCard rows={rows} backendHref={servicesHref} onAddListener={onAddListener} />
            <BackendCards cards={cards} href={servicesHref} />
          </div>
        </div>
      </section>
    </div>
  );
}

import { ProtocolBadge } from './nlb-empty-state';
import { overviewSummary, type ListenerRow } from './nlb-overview-model';
import { StatusBadge } from './status-badge';
import type { BadgeType, NetworkLoadBalancer } from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { Button } from '@datum-cloud/datum-ui/button';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { DataTable, type DataTableFeatures } from '@datum-cloud/datum-ui/data-table';
import { useCopyToClipboard } from '@datum-cloud/datum-ui/hooks';
import { Icon, SpinnerIcon } from '@datum-cloud/datum-ui/icons';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { MoreActions, type ActionItem } from '@datum-cloud/datum-ui/more-actions';
import { toast } from '@datum-cloud/datum-ui/toast';
import { cn } from '@datum-cloud/datum-ui/utils';
import type { ColumnDef } from '@tanstack/react-table';
import {
  CheckIcon,
  ChevronRightIcon,
  CopyIcon,
  GlobeIcon,
  InfoIcon,
  PlusIcon,
  SplitIcon,
} from 'lucide-react';
import { useMemo, useState } from 'react';
import { Link } from 'react-router';

const DOT: Record<BadgeType, string> = {
  success: 'bg-green-500',
  warning: 'bg-amber-500',
  danger: 'bg-destructive',
  muted: 'bg-muted-foreground',
};

function Dot({ type }: { type: BadgeType }) {
  return (
    <span aria-hidden className={cn('inline-block size-2 shrink-0 rounded-full', DOT[type])} />
  );
}

export function NlbStatusPill({ rows }: { rows: ListenerRow[] }) {
  const summary = overviewSummary(rows);
  return (
    <Badge
      type={summary.type}
      theme="light"
      data-testid="networking-plugin-nlb-summary"
      className="flex shrink-0 cursor-default items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium">
      {summary.pending ? <SpinnerIcon size="xs" aria-hidden /> : <Dot type={summary.type} />}
      {summary.label}
    </Badge>
  );
}

function CopyText({ value }: { value: string }) {
  const [, copy] = useCopyToClipboard();
  const [copied, setCopied] = useState(false);
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      <span className="truncate font-mono text-xs" title={value}>
        {value}
      </span>
      <button
        type="button"
        aria-label={`Copy ${value}`}
        className="text-muted-foreground hover:text-foreground shrink-0 transition-colors"
        onClick={() =>
          copy(value).then(() => {
            toast.success('Copied to clipboard');
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
          })
        }>
        <Icon icon={copied ? CheckIcon : CopyIcon} size={12} />
      </button>
    </span>
  );
}

const NLB_CSS = `
.nlb-band { background: var(--sidebar); }
.nlb-flow { display: grid; gap: 1rem; grid-template-columns: minmax(0, 1fr); align-items: start; }
.nlb-arrow { display: none; }
@media (min-width: 64rem) {
  .nlb-flow { grid-template-columns: 14rem 3.5rem minmax(0, 1fr); gap: 0; }
  .nlb-arrow { display: flex; align-self: center; }
}`;

function Pending({ width = '10rem' }: { width?: string }) {
  return (
    <Skeleton
      className="h-4 rounded"
      style={{ width, maxWidth: '100%' }}
      title="Allocating an address"
      aria-label="Allocating an address"
    />
  );
}

function Eyebrow({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-muted-foreground text-xs font-semibold tracking-wider uppercase">
      {children}
    </span>
  );
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt
        className="text-muted-foreground font-semibold tracking-wider uppercase"
        style={{ fontSize: '0.625rem' }}>
        {label}
      </dt>
      <dd className="min-w-0 truncate text-xs">{children}</dd>
    </div>
  );
}

function cities(n: number) {
  return `${n} ${n === 1 ? 'city' : 'cities'}`;
}

function AddressesCard({ nlb, networkHref }: { nlb: NetworkLoadBalancer; networkHref?: string }) {
  const locations = new Set(
    nlb.endpoints.filter((e) => e.addresses.length > 0).map((e) => e.location)
  ).size;
  return (
    <Card size="sm" className="w-full" data-testid="networking-plugin-nlb-info">
      <CardContent className="flex flex-col gap-3">
        <span className="text-muted-foreground flex items-center gap-2 text-xs font-medium">
          <Icon icon={GlobeIcon} size={14} />
          Public addresses
        </span>
        <dl className="flex flex-col gap-2.5">
          <Fact label="Network">
            {networkHref && nlb.networkName ? (
              <Link to={networkHref} className="text-primary font-mono hover:underline">
                {nlb.networkName}
              </Link>
            ) : (
              <span className="font-mono">{nlb.networkName ?? '—'}</span>
            )}
          </Fact>
          <Fact label="Family">IPv6</Fact>
          <Fact label="Locations">
            {locations > 0 ? (
              `Follows backends · ${cities(locations)}`
            ) : (
              <span className="text-muted-foreground">Allocating</span>
            )}
          </Fact>
        </dl>
        <span className="text-muted-foreground flex items-center gap-1.5 text-xs font-medium">
          <Icon icon={InfoIcon} size={12} />
          IPv4 coming soon
        </span>
      </CardContent>
    </Card>
  );
}

function FlowArrow() {
  return (
    <div aria-hidden className="nlb-arrow text-muted-foreground items-center px-2">
      <span className="bg-border h-0.5 flex-1" />
      <Icon icon={ChevronRightIcon} size={14} className="-ml-1.5" />
    </div>
  );
}

function AddressCell({ row }: { row: ListenerRow }) {
  if (!row.address) return <Pending />;
  const others = row.locations - 1;
  return (
    <span className="flex min-w-0 flex-col gap-1" style={{ maxWidth: '18rem' }}>
      {row.hostname && <CopyText value={row.hostname} />}
      <span className="flex min-w-0 items-center gap-2">
        <CopyText value={row.address} />
        {others > 0 && (
          <span className="bg-muted text-muted-foreground shrink-0 rounded px-1.5 py-0.5 text-xs">
            +{cities(others)}
          </span>
        )}
      </span>
    </span>
  );
}

function SendToCell({ row, href }: { row: ListenerRow; href?: string }) {
  if (!row.backend) {
    return (
      <StatusBadge type={row.status.type} className="w-fit">
        {row.status.label}
      </StatusBadge>
    );
  }
  const name = <span className="font-mono text-xs">{row.backend.name}</span>;
  return (
    <span className="flex flex-wrap items-center gap-2">
      <span
        className="flex w-fit items-center gap-1.5 rounded border px-2 py-0.5 whitespace-nowrap"
        title={row.status.detail ?? row.status.label}>
        <Dot type={row.status.type} />
        {href ? (
          <Link to={href} className="hover:underline">
            {name}
          </Link>
        ) : (
          name
        )}
        <span className="text-muted-foreground font-mono text-xs">:{row.backend.port}</span>
      </span>
      {row.status.type !== 'success' && (
        <StatusBadge type={row.status.type} pending={row.status.pending}>
          {row.status.label}
        </StatusBadge>
      )}
    </span>
  );
}

function HealthCheckCell({ row }: { row: ListenerRow }) {
  const check = row.backend?.healthCheck;
  if (!row.backend) return <span className="text-muted-foreground">—</span>;
  if (!check) return <span className="text-muted-foreground font-mono text-xs">None</span>;
  return (
    <span className="text-muted-foreground font-mono text-xs whitespace-nowrap">
      {check.protocol}
      {/* With no port, a TCP probe uses the backend's serving port. */}
      {check.port ? ` :${check.port}` : ' · serving port'}
    </span>
  );
}

export interface ListenerActions {
  onAdd: () => void;
  onEdit: (row: ListenerRow) => void;
  onDelete: (row: ListenerRow) => void;
}

type ListenerColumn = ColumnDef<DataTableFeatures, ListenerRow, unknown>;

function ListenersCard({
  rows,
  backendHref,
  actions,
}: {
  rows: ListenerRow[];
  backendHref?: string;
  actions?: ListenerActions;
}) {
  const single = rows.length <= 1;
  const columns = useMemo<ListenerColumn[]>(() => {
    const rowActions: ActionItem<ListenerRow>[] = actions
      ? [
          { key: 'edit', label: 'Edit', onClick: actions.onEdit },
          {
            key: 'delete',
            label: 'Delete',
            variant: 'destructive',
            // An NLB needs at least one listener; the API refuses an empty list.
            disabled: single,
            tooltip: single ? 'A load balancer needs at least one listener.' : undefined,
            onClick: actions.onDelete,
          },
        ]
      : [];
    return [
      {
        id: 'protocol',
        header: 'Protocol',
        size: 88,
        cell: ({ row }) => (
          <span data-listener={row.original.listener.name}>
            <ProtocolBadge protocol={row.original.listener.protocol} />
          </span>
        ),
      },
      {
        id: 'port',
        header: 'Port',
        size: 72,
        cell: ({ row }) => (
          <span className="font-mono text-sm font-semibold tabular-nums">
            {row.original.listener.port}
          </span>
        ),
      },
      {
        id: 'address',
        header: 'Address',
        cell: ({ row }) => <AddressCell row={row.original} />,
      },
      {
        id: 'sendTo',
        header: 'Send to',
        cell: ({ row }) => <SendToCell row={row.original} href={backendHref} />,
      },
      {
        id: 'healthCheck',
        header: 'Health check',
        cell: ({ row }) => <HealthCheckCell row={row.original} />,
      },
      ...(actions
        ? [
            {
              id: 'actions',
              header: '',
              size: 48,
              cell: ({ row }) => (
                <div data-slot="dt-row-actions" className="flex justify-end">
                  <MoreActions
                    row={row.original}
                    actions={rowActions}
                    className="size-6 border"
                    iconClassName="size-3.5"
                  />
                </div>
              ),
            } satisfies ListenerColumn,
          ]
        : []),
    ];
  }, [actions, backendHref, single]);

  return (
    <Card size="sm" sectioned className="w-full min-w-0 overflow-hidden">
      <div className="bg-foreground text-background flex flex-wrap items-center gap-2.5 px-4 py-3">
        <Icon icon={SplitIcon} size={14} className="opacity-70" />
        <h2 className="text-sm font-semibold">Listeners</h2>
        <span className="text-xs opacity-60">One port, one backend</span>
        <span className="flex-1" />
        {actions && (
          <Button
            type="primary"
            theme="solid"
            size="xs"
            htmlType="button"
            icon={<Icon icon={PlusIcon} size={12} />}
            onClick={actions.onAdd}>
            Add listener
          </Button>
        )}
      </div>
      <DataTable.Client data={rows} columns={columns} getRowId={(row) => row.listener.name}>
        <div data-testid="networking-plugin-nlb-listeners">
          <DataTable.Content />
        </div>
      </DataTable.Client>
    </Card>
  );
}

export function NlbOverview({
  nlb,
  rows,
  networkHref,
  servicesHref,
  listenerActions,
}: {
  nlb: NetworkLoadBalancer;
  rows: ListenerRow[];
  /** The NLB's network, for the Network link. */
  networkHref?: string;
  /** Where a backend links to: its network's services list. */
  servicesHref?: string;
  /** Add, edit and delete; without them the listeners are read-only. */
  listenerActions?: ListenerActions;
}) {
  return (
    <section
      className="nlb-band flex flex-col gap-4 rounded-xl px-5 pt-5 pb-6 md:px-7"
      data-testid="networking-plugin-nlb-overview">
      <style>{NLB_CSS}</style>
      <div className="flex flex-wrap items-center gap-2.5">
        <Eyebrow>Traffic path</Eyebrow>
        <span className="flex-1" />
        <NlbStatusPill rows={rows} />
      </div>
      <div className="nlb-flow">
        <AddressesCard nlb={nlb} networkHref={networkHref} />
        <FlowArrow />
        <ListenersCard rows={rows} backendHref={servicesHref} actions={listenerActions} />
      </div>
    </section>
  );
}

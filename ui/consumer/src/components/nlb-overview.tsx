import { ProtocolBadge } from './nlb-empty-state';
import { overviewSummary, type ListenerRow } from './nlb-overview-model';
import { StatusBadge } from './status-badge';
import type { BadgeType, NetworkLoadBalancer } from '../schema';
import { Button } from '@datum-cloud/datum-ui/button';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { DataTable, type DataTableFeatures } from '@datum-cloud/datum-ui/data-table';
import { useCopyToClipboard } from '@datum-cloud/datum-ui/hooks';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { MoreActions, type ActionItem } from '@datum-cloud/datum-ui/more-actions';
import { toast } from '@datum-cloud/datum-ui/toast';
import { cn } from '@datum-cloud/datum-ui/utils';
import type { ColumnDef } from '@tanstack/react-table';
import { BoxIcon, CheckIcon, CopyIcon, PlusIcon } from 'lucide-react';
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

/** The NLB's status beside its name: a dot and badge, as compute's workloads have. */
export function NlbStatusPill({ rows }: { rows: ListenerRow[] }) {
  const summary = overviewSummary(rows);
  return (
    <span
      data-testid="networking-plugin-nlb-summary"
      title={summary.label}
      className="flex shrink-0 items-center gap-2">
      <Dot type={summary.type} />
      <StatusBadge type={summary.type} pending={summary.pending}>
        {summary.type === 'success' ? 'Forwarding' : summary.label}
      </StatusBadge>
    </span>
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

/** Laid out like compute's summary cards: a small title over the value. */
function StatCard({
  title,
  note,
  wide,
  children,
}: {
  title: string;
  note?: string;
  /** Two columns wide, for a value as long as a hostname. */
  wide?: boolean;
  children: React.ReactNode;
}) {
  return (
    <Card size="sm" className={cn('h-full w-full', wide && 'nlb-summary-wide')}>
      <CardContent className="flex min-w-0 flex-col gap-2">
        <span className="text-muted-foreground text-xs font-medium">{title}</span>
        <div className="flex min-h-8 min-w-0 items-center">{children}</div>
        {note && <span className="text-muted-foreground text-xs">{note}</span>}
      </CardContent>
    </Card>
  );
}

// The host compiles no lg:col-span-2, so the layout that gives the address
// card two columns is scoped CSS.
const SUMMARY_CSS = `
.nlb-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.nlb-summary-wide { grid-column: span 2; }
@media (min-width: 64rem) {
  .nlb-summary { grid-template-columns: repeat(4, minmax(0, 1fr)); }
}`;

/** Holds an address's place, the size of the value to come, until it's allocated. */
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

function AddressLine({
  label,
  value,
  pendingWidth,
  children,
}: {
  label: string;
  value?: string;
  pendingWidth?: string;
  /** Shown in place of a value, e.g. for a family that isn't offered yet. */
  children?: React.ReactNode;
}) {
  return (
    <div className="flex min-w-0 items-center gap-3" style={{ minHeight: '1.25rem' }}>
      <dt className="text-muted-foreground w-16 shrink-0 text-xs">{label}</dt>
      <dd className="flex min-w-0 flex-1 items-center">
        {children ?? (value ? <CopyText value={value} /> : <Pending width={pendingWidth} />)}
      </dd>
    </div>
  );
}

/**
 * Each listener has its own address, so the cards can only show one hostname
 * and address when every listener shares it, as with a single listener.
 * Otherwise they show per listener in the table.
 */
function sharedAddress(rows: ListenerRow[]): ListenerRow | undefined {
  const first = rows[0];
  if (rows.length === 1) return first;
  if (!first?.address) return undefined;
  return rows.every((r) => r.address === first.address) ? first : undefined;
}

/** Healthy and total endpoints across the NLB's backends, each counted once. */
function endpointCounts(rows: ListenerRow[]) {
  const services = new Map(rows.flatMap((r) => (r.service ? [[r.service.name, r.service]] : [])));
  if (services.size === 0) return undefined;
  let healthy = 0;
  let members = 0;
  for (const s of services.values()) {
    healthy += s.summary?.healthy ?? 0;
    members += s.summary?.members ?? 0;
  }
  return { healthy, members };
}

function SummaryCards({
  nlb,
  rows,
  shared,
  networkHref,
}: {
  nlb: NetworkLoadBalancer;
  rows: ListenerRow[];
  shared?: ListenerRow;
  networkHref?: string;
}) {
  const counts = endpointCounts(rows);
  const addresses = new Set(rows.flatMap((r) => (r.address ? [r.address] : []))).size;
  return (
    <div
      className={shared ? 'nlb-summary grid gap-6' : 'grid grid-cols-2 gap-6 lg:grid-cols-4'}
      data-testid="networking-plugin-nlb-info">
      {shared ? (
        <>
          <style>{SUMMARY_CSS}</style>
          <StatCard title="Address" wide>
            <dl className="flex min-w-0 flex-1 flex-col gap-1.5">
              <AddressLine label="Hostname" value={shared.hostname} pendingWidth="18rem" />
              <AddressLine label="IPv6" value={shared.address} />
              <AddressLine label="IPv4">
                <span className="text-muted-foreground text-xs">Coming soon</span>
              </AddressLine>
            </dl>
          </StatCard>
        </>
      ) : (
        <>
          <StatCard title="Listeners">
            <span className="text-2xl font-semibold tabular-nums">{rows.length}</span>
          </StatCard>
          <StatCard title="IPv6 addresses" note="IPv4 coming soon">
            <span className="text-2xl font-semibold tabular-nums">{addresses}</span>
          </StatCard>
        </>
      )}
      <StatCard title="Network">
        {networkHref && nlb.networkName ? (
          <Link
            to={networkHref}
            className="text-primary truncate font-mono text-sm hover:underline">
            {nlb.networkName}
          </Link>
        ) : (
          <span className="truncate font-mono text-sm">{nlb.networkName ?? '—'}</span>
        )}
      </StatCard>
      <StatCard title="Healthy endpoints">
        <span className="text-2xl font-semibold tabular-nums">
          {counts ? `${counts.healthy} / ${counts.members}` : '—'}
        </span>
      </StatCard>
    </div>
  );
}

function AddressCell({ row }: { row: ListenerRow }) {
  if (!row.address) return <Pending />;
  const others = row.locations - 1;
  return (
    <span className="flex min-w-0 flex-col gap-1" style={{ maxWidth: '16rem' }}>
      {row.hostname && <CopyText value={row.hostname} />}
      <span className="flex min-w-0 items-center gap-2">
        <CopyText value={row.address} />
        {others > 0 && (
          <span className="bg-muted text-muted-foreground shrink-0 rounded px-1.5 py-0.5 text-xs">
            +{others} {others === 1 ? 'city' : 'cities'}
          </span>
        )}
      </span>
    </span>
  );
}

function BackendCell({ row, href }: { row: ListenerRow; href?: string }) {
  if (!row.backend) return <span className="text-muted-foreground">—</span>;
  const name = <span className="text-primary font-mono text-xs">{row.backend.name}</span>;
  return (
    <span className="flex items-center gap-2 whitespace-nowrap">
      <Icon icon={BoxIcon} size={14} className="text-muted-foreground" />
      {href ? (
        <Link to={href} className="hover:underline">
          {name}
        </Link>
      ) : (
        name
      )}
      <span className="text-muted-foreground font-mono text-xs">:{row.backend.port}</span>
    </span>
  );
}

/** A status badge, with the backend's healthy count beside it while serving. */
function HealthCell({ row }: { row: ListenerRow }) {
  const summary = row.service?.summary;
  const showCount = summary && (row.status.type === 'success' || row.status.label === 'Degraded');
  return (
    <span className="flex items-center gap-2 whitespace-nowrap" title={row.status.detail}>
      <StatusBadge type={row.status.type} pending={row.status.pending}>
        {row.status.label}
      </StatusBadge>
      {showCount && (
        <span className="text-muted-foreground text-xs">
          {summary.healthy}/{summary.members} endpoints healthy
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
    <span className="font-mono text-xs whitespace-nowrap">
      {check.protocol}
      <span className="text-muted-foreground">
        {/* With no port, a TCP probe uses the backend's serving port. */}
        {check.port ? ` :${check.port}` : ' · serving port'}
      </span>
    </span>
  );
}

export interface ListenerActions {
  onAdd: () => void;
  onEdit: (row: ListenerRow) => void;
  onDelete: (row: ListenerRow) => void;
}

type ListenerColumn = ColumnDef<DataTableFeatures, ListenerRow, unknown>;

// The same data table as the NLB list, so the two pages read alike.
function ListenersSection({
  rows,
  showAddress,
  backendHref,
  actions,
}: {
  rows: ListenerRow[];
  showAddress: boolean;
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
        id: 'port',
        header: 'Port',
        cell: ({ row }) => (
          <span data-listener={row.original.listener.name}>
            <ProtocolBadge
              protocol={row.original.listener.protocol}
              port={row.original.listener.port}
              small
            />
          </span>
        ),
      },
      ...(showAddress
        ? [
            {
              id: 'address',
              header: 'Address',
              cell: ({ row }) => <AddressCell row={row.original} />,
            } satisfies ListenerColumn,
          ]
        : []),
      {
        id: 'backend',
        header: 'Backend',
        cell: ({ row }) => <BackendCell row={row.original} href={backendHref} />,
      },
      {
        id: 'health',
        header: 'Health',
        cell: ({ row }) => <HealthCell row={row.original} />,
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
  }, [actions, backendHref, showAddress, single]);

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-base font-semibold">Listeners</h2>
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">
          {rows.length}
        </span>
        <span className="text-muted-foreground text-sm">
          Each listener takes one port and forwards it to one backend.
        </span>
        <span className="flex-1" />
        {actions && (
          <Button
            type="primary"
            theme="solid"
            size="small"
            htmlType="button"
            onClick={actions.onAdd}>
            <Icon icon={PlusIcon} className="size-4" />
            Add listener
          </Button>
        )}
      </div>
      <DataTable.Client data={rows} columns={columns} getRowId={(row) => row.listener.name}>
        <div
          className="overflow-hidden rounded-lg border"
          data-testid="networking-plugin-nlb-listeners">
          <DataTable.Content />
        </div>
      </DataTable.Client>
    </section>
  );
}

// Summary cards and a listeners table. Simpler than mockup N1: no traffic
// path band or backend cards, since backend health sits in the table.
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
  const shared = sharedAddress(rows);
  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-nlb-overview">
      <SummaryCards nlb={nlb} rows={rows} shared={shared} networkHref={networkHref} />
      <ListenersSection
        rows={rows}
        showAddress={!shared}
        backendHref={servicesHref}
        actions={listenerActions}
      />
    </div>
  );
}

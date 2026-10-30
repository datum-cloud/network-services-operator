import { ProtocolBadge } from './nlb-empty-state';
import { overviewSummary, type ListenerRow, type ListenerStatus } from './nlb-overview-model';
import type { NetworkLoadBalancer } from '../schema';
import { Button } from '@datum-cloud/datum-ui/button';
import { useCopyToClipboard } from '@datum-cloud/datum-ui/hooks';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { MoreActions, type ActionItem } from '@datum-cloud/datum-ui/more-actions';
import { toast } from '@datum-cloud/datum-ui/toast';
import { cn } from '@datum-cloud/datum-ui/utils';
import { BoxIcon, CheckIcon, CopyIcon, PlusIcon } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router';

const DOT: Record<ListenerStatus['type'], string> = {
  success: 'bg-green-500',
  warning: 'bg-amber-500',
  danger: 'bg-destructive',
  muted: 'bg-muted-foreground',
};

const STATUS_TEXT: Record<ListenerStatus['type'], string> = {
  success: 'text-green-600',
  warning: 'text-amber-600',
  danger: 'text-destructive',
  muted: 'text-muted-foreground',
};

// The portal's CSS has no flex-basis or max-width utilities these sizes.
const CELL = { flex: '1 1 9rem' };
const WIDE_CELL = { flex: '2 1 18rem' };
const TABLE_TEXT_MAX = { maxWidth: '13rem' };

function Dot({ type }: { type: ListenerStatus['type'] }) {
  return (
    <span aria-hidden className={cn('inline-block size-1.5 shrink-0 rounded-full', DOT[type])} />
  );
}

/** The NLB's status in a pill, for beside its name. */
export function NlbStatusPill({ rows }: { rows: ListenerRow[] }) {
  const summary = overviewSummary(rows);
  return (
    <span
      data-testid="networking-plugin-nlb-summary"
      title={summary.label}
      className={cn(
        'bg-card flex items-center gap-2 rounded-full border px-3 py-1 text-sm font-medium whitespace-nowrap',
        STATUS_TEXT[summary.type]
      )}>
      <Dot type={summary.type} />
      {summary.type === 'success' ? 'Forwarding' : summary.label}
    </span>
  );
}

function CopyText({ value, maxWidth }: { value: string; maxWidth?: boolean }) {
  const [, copy] = useCopyToClipboard();
  const [copied, setCopied] = useState(false);
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      <span
        className="truncate font-mono text-xs"
        title={value}
        style={maxWidth ? TABLE_TEXT_MAX : undefined}>
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

function InfoCell({
  label,
  children,
  wide,
}: {
  label: string;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-4 py-3" style={wide ? WIDE_CELL : CELL}>
      <dt className="text-muted-foreground text-xs font-semibold tracking-wider uppercase">
        {label}
      </dt>
      <dd className="min-w-0 text-sm">{children}</dd>
    </div>
  );
}

/**
 * Each listener has its own address, so the strip can only show one hostname
 * and address when every listener shares it, as with a single listener.
 * Otherwise they show per listener in the table.
 */
function sharedAddress(rows: ListenerRow[]): ListenerRow | undefined {
  const first = rows[0];
  if (rows.length === 1) return first;
  if (!first?.address) return undefined;
  return rows.every((r) => r.address === first.address) ? first : undefined;
}

function InfoStrip({
  nlb,
  shared,
  networkHref,
}: {
  nlb: NetworkLoadBalancer;
  shared?: ListenerRow;
  networkHref?: string;
}) {
  return (
    <dl
      className="bg-card flex flex-wrap rounded-lg border"
      data-testid="networking-plugin-nlb-info">
      {shared && (
        <>
          <InfoCell label="Hostname" wide>
            {shared.hostname ? (
              <CopyText value={shared.hostname} />
            ) : (
              <span className="text-muted-foreground">Pending</span>
            )}
          </InfoCell>
          <InfoCell label="IPv6 address">
            {shared.address ? (
              <CopyText value={shared.address} />
            ) : (
              <span className="text-muted-foreground">Pending</span>
            )}
          </InfoCell>
        </>
      )}
      <InfoCell label="Network">
        {networkHref && nlb.networkName ? (
          <Link to={networkHref} className="text-primary font-mono text-xs hover:underline">
            {nlb.networkName}
          </Link>
        ) : (
          <span className="font-mono text-xs">{nlb.networkName ?? '—'}</span>
        )}
      </InfoCell>
      <InfoCell label="Family">
        IPv6
        <span className="text-muted-foreground block text-xs">IPv4 coming soon</span>
      </InfoCell>
      <InfoCell label="Locations">Follows backends</InfoCell>
    </dl>
  );
}

function AddressCell({ row }: { row: ListenerRow }) {
  if (!row.address) return <span className="text-muted-foreground text-sm">Pending</span>;
  const others = row.locations - 1;
  return (
    <span className="flex min-w-0 flex-col gap-1">
      {row.hostname && <CopyText value={row.hostname} maxWidth />}
      <span className="flex flex-wrap items-center gap-2">
        <CopyText value={row.address} maxWidth />
        {others > 0 && (
          <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">
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

/** Serving shows the backend's healthy count; anything else says what's wrong. */
function HealthCell({ row }: { row: ListenerRow }) {
  const summary = row.service?.summary;
  const label =
    row.status.type === 'success' || row.status.label === 'Degraded'
      ? summary
        ? `${summary.healthy}/${summary.members} endpoints healthy`
        : row.status.label
      : row.status.label;
  return (
    <span
      className={cn(
        'flex items-center gap-1.5 text-xs font-medium whitespace-nowrap',
        STATUS_TEXT[row.status.type]
      )}
      title={row.status.detail}>
      <Dot type={row.status.type} />
      {label}
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
  const rowActions: ActionItem<ListenerRow>[] = actions
    ? [
        { key: 'edit', label: 'Edit', onClick: actions.onEdit },
        {
          key: 'delete',
          label: 'Delete',
          variant: 'destructive',
          // An NLB needs at least one listener; the API refuses an empty list.
          disabled: rows.length <= 1,
          tooltip: rows.length <= 1 ? 'A load balancer needs at least one listener.' : undefined,
          onClick: actions.onDelete,
        },
      ]
    : [];

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
      <div
        className="bg-card overflow-x-auto rounded-lg border"
        data-testid="networking-plugin-nlb-listeners">
        <table className="w-full text-left text-sm">
          <thead className="text-muted-foreground text-xs tracking-wider uppercase">
            <tr className="border-b">
              <th className="px-4 py-2 font-semibold">Port</th>
              {showAddress && <th className="px-4 py-2 font-semibold">Address</th>}
              <th className="px-4 py-2 font-semibold">Backend</th>
              <th className="px-4 py-2 font-semibold">Health</th>
              <th className="px-4 py-2 font-semibold">Health check</th>
              {actions && <th className="w-12 px-4 py-2" aria-label="Actions" />}
            </tr>
          </thead>
          <tbody className="divide-y">
            {rows.map((row) => (
              <tr key={row.listener.name} data-listener={row.listener.name}>
                <td className="px-4 py-3 whitespace-nowrap">
                  <span className="flex items-center gap-2">
                    <ProtocolBadge protocol={row.listener.protocol} />
                    <span className="font-mono font-medium">{row.listener.port}</span>
                  </span>
                </td>
                {showAddress && (
                  <td className="px-4 py-3">
                    <AddressCell row={row} />
                  </td>
                )}
                <td className="px-4 py-3">
                  <BackendCell row={row} href={backendHref} />
                </td>
                <td className="px-4 py-3">
                  <HealthCell row={row} />
                </td>
                <td className="px-4 py-3">
                  <HealthCheckCell row={row} />
                </td>
                {actions && (
                  <td className="px-4 py-3">
                    <div className="flex justify-end">
                      <MoreActions
                        row={row}
                        actions={rowActions}
                        className="size-6 border"
                        iconClassName="size-3.5"
                      />
                    </div>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

// An info strip and a listeners table. Simpler than mockup N1: no traffic
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
      <InfoStrip nlb={nlb} shared={shared} networkHref={networkHref} />
      <ListenersSection
        rows={rows}
        showAddress={!shared}
        backendHref={servicesHref}
        actions={listenerActions}
      />
    </div>
  );
}

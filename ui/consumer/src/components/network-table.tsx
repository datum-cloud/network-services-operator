import { ConfirmDeleteDialog } from './confirm-delete-dialog';
import { EnableIPv6Dialog } from './enable-ipv6-dialog';
import { SortableHeader } from './sortable-header';
import { StatusBadge } from './status-badge';
import { useDeleteNetwork } from '../lib/api';
import { readyStatusToBadgeType, readyStatusToLabel, type Network } from '../schema';
import {
  DataTable,
  useDataTablePagination,
  type DataTableFeatures,
} from '@datum-cloud/datum-ui/data-table';
import { DateTime } from '@datum-cloud/datum-ui/date-time';
import { MoreActions, type ActionItem } from '@datum-cloud/datum-ui/more-actions';
import { toast } from '@datum-cloud/datum-ui/toast';
import type { ColumnDef } from '@tanstack/react-table';
import { useMemo, useState } from 'react';
import { Link, useLocation, useNavigate } from 'react-router';

type NetworkColumn = ColumnDef<DataTableFeatures, Network, unknown>;

const READY_SORT_RANK: Record<Network['readyStatus'], number> = {
  False: 0,
  Unknown: 1,
  True: 2,
};

function hasIPv6(network: Network): boolean {
  return network.ipFamilies.includes('IPv6');
}

function matchesSearch(network: Network, search: string): boolean {
  const needle = search.trim().toLowerCase();
  if (!needle) return true;
  return [network.name, network.ipv6Prefix, ...network.ipFamilies]
    .filter(Boolean)
    .join(' ')
    .toLowerCase()
    .includes(needle);
}

function ConditionalPagination() {
  const { pageCount } = useDataTablePagination();
  if (pageCount <= 1) return null;
  return <DataTable.Pagination className="datum-ui-data-table__pagination" />;
}

function Muted({ children, mono }: { children: React.ReactNode; mono?: boolean }) {
  return (
    <span className={mono ? 'text-muted-foreground font-mono text-xs' : 'text-muted-foreground'}>
      {children}
    </span>
  );
}

export function NetworkTable({
  networks,
  projectId,
}: {
  networks: Network[];
  projectId?: string;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const basePath = location.pathname.replace(/\/$/, '');
  const [enableTarget, setEnableTarget] = useState<Network>();
  const [deleteTarget, setDeleteTarget] = useState<Network>();
  const deleteMutation = useDeleteNetwork(projectId);

  const columns = useMemo<NetworkColumn[]>(() => {
    const href = (name: string) => `${basePath}/${encodeURIComponent(name)}`;
    const actions: ActionItem<Network>[] = [
      { key: 'view', label: 'View', onClick: (network) => navigate(href(network.name)) },
      {
        key: 'enable-ipv6',
        label: 'Enable IPv6',
        hidden: hasIPv6,
        onClick: setEnableTarget,
      },
      {
        key: 'delete',
        label: 'Delete',
        variant: 'destructive',
        onClick: setDeleteTarget,
      },
    ];

    return [
      {
        id: 'name',
        accessorKey: 'name',
        header: ({ column }) => <SortableHeader column={column} title="Name" />,
        cell: ({ row }) => (
          <Link
            to={href(row.original.name)}
            className="truncate font-medium hover:underline"
            title={row.original.name}>
            {row.original.name}
          </Link>
        ),
      },
      {
        id: 'status',
        accessorFn: (network) => READY_SORT_RANK[network.readyStatus],
        sortDescFirst: false,
        header: ({ column }) => <SortableHeader column={column} title="Status" />,
        cell: ({ row }) => (
          <StatusBadge type={readyStatusToBadgeType(row.original.readyStatus)}>
            {readyStatusToLabel(row.original.readyStatus, row.original.readyReason)}
          </StatusBadge>
        ),
      },
      {
        id: 'ipv6Prefix',
        header: 'IPv6 prefix',
        enableSorting: false,
        cell: ({ row }) => <Muted mono>{row.original.ipv6Prefix ?? '—'}</Muted>,
      },
      {
        id: 'ipFamilies',
        header: 'IP families',
        enableSorting: false,
        cell: ({ row }) => (
          <Muted>{row.original.ipFamilies.length > 0 ? row.original.ipFamilies.join(', ') : '—'}</Muted>
        ),
      },
      {
        id: 'mtu',
        accessorFn: (network) => network.mtu ?? 0,
        header: ({ column }) => <SortableHeader column={column} title="MTU" />,
        cell: ({ row }) => <Muted>{row.original.mtu ?? '—'}</Muted>,
      },
      {
        id: 'createdAt',
        accessorFn: (network) => network.createdAt.getTime(),
        header: ({ column }) => <SortableHeader column={column} title="Created" />,
        cell: ({ row }) => (
          <DateTime
            date={row.original.createdAt}
            variant="relative"
            className="text-muted-foreground text-sm"
          />
        ),
      },
      {
        id: 'actions',
        header: '',
        enableSorting: false,
        size: 48,
        // The menu renders in a portal, but React still bubbles its clicks
        // through here to the row, which would open the network instead of
        // the chosen action. Stop them at the cell, and fill the cell so a
        // click beside the button doesn't open the row either.
        cell: ({ row }) => (
          <div
            data-slot="dt-row-actions"
            className="-m-2 flex justify-end p-2"
            onClick={(event) => event.stopPropagation()}>
            <MoreActions
              row={row.original}
              actions={actions}
              className="size-6 border"
              iconClassName="size-3.5"
            />
          </div>
        ),
      },
    ];
  }, [basePath, navigate]);

  return (
    <>
      <DataTable.Client
        data={networks}
        columns={columns}
        getRowId={(network) => network.uid || network.name}
        defaultSort={[{ id: 'name', desc: false }]}
        searchFn={matchesSearch}
        className="space-y-6">
        <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <DataTable.Search placeholder="Search networks" className="w-full sm:max-w-xs" />
        </div>
        <div
          className="overflow-hidden rounded-lg border"
          data-testid="networking-plugin-network-table">
          <DataTable.Content
            onRowClick={(network) => navigate(`${basePath}/${encodeURIComponent(network.name)}`)}
            rowClassName="cursor-pointer"
            emptyMessage={
              <p className="text-muted-foreground py-8 text-center text-sm">
                No networks match your search.
              </p>
            }
          />
        </div>
        <ConditionalPagination />
      </DataTable.Client>

      <EnableIPv6Dialog
        projectId={projectId}
        network={enableTarget}
        open={enableTarget !== undefined}
        onOpenChange={(open) => !open && setEnableTarget(undefined)}
      />

      <ConfirmDeleteDialog
        open={deleteTarget !== undefined}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title={`Delete network ${deleteTarget?.name ?? ''}?`}
        description="This permanently deletes the network and everything provisioned on it. This can't be undone."
        onConfirm={async () => {
          if (!deleteTarget) return;
          try {
            await deleteMutation.mutateAsync(deleteTarget.name);
            toast.success(`Deleted network ${deleteTarget.name}`);
          } catch (err) {
            toast.error((err as Error).message);
            throw err;
          }
        }}
      />
    </>
  );
}

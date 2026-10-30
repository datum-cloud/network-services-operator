import { ConfirmDeleteDialog } from './confirm-delete-dialog';
import { ProtocolBadge } from './nlb-empty-state';
import { SortableHeader } from './sortable-header';
import { StatusBadge } from './status-badge';
import { planNlbDelete, useDeleteNetworkLoadBalancer } from '../lib/api';
import {
  nlbReadyStatusToLabel,
  readyStatusToBadgeType,
  type NetworkLoadBalancer,
  type NetworkLoadBalancerRoute,
} from '../schema';
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

export interface NlbRow {
  nlb: NetworkLoadBalancer;
  /** Routes naming this NLB in their parentRefs; undefined until routes load. */
  routes?: NetworkLoadBalancerRoute[];
  backendNames?: string[];
}

export function toNlbRows(
  nlbs: NetworkLoadBalancer[],
  routes: NetworkLoadBalancerRoute[] | undefined
): NlbRow[] {
  return nlbs.map((nlb) => {
    if (!routes) return { nlb };
    const attached = routes.filter((r) => r.parents.some((p) => p.name === nlb.name));
    return {
      nlb,
      routes: attached,
      backendNames: [...new Set(attached.flatMap((r) => r.backendNames))].sort(),
    };
  });
}

type NlbColumn = ColumnDef<DataTableFeatures, NlbRow, unknown>;

const READY_SORT_RANK: Record<NetworkLoadBalancer['readyStatus'], number> = {
  False: 0,
  Unknown: 1,
  True: 2,
};

function matchesSearch(row: NlbRow, search: string): boolean {
  const needle = search.trim().toLowerCase();
  if (!needle) return true;
  return [
    row.nlb.name,
    ...row.nlb.listeners.map((l) => `${l.protocol} ${l.port}`),
    ...(row.backendNames ?? []),
  ]
    .join(' ')
    .toLowerCase()
    .includes(needle);
}

function ConditionalPagination() {
  const { pageCount } = useDataTablePagination();
  if (pageCount <= 1) return null;
  return <DataTable.Pagination className="datum-ui-data-table__pagination" />;
}

function deleteDescription(row: NlbRow | undefined): string {
  if (!row?.routes) return '';
  const { deleteRoutes, detachRoutes } = planNlbDelete(row.nlb.name, row.routes);
  const parts = [`This permanently deletes ${row.nlb.name} and releases its public addresses.`];
  if (deleteRoutes.length > 0) {
    const names = deleteRoutes.map((r) => r.name).join(', ');
    parts.push(
      `It also deletes ${deleteRoutes.length === 1 ? 'its route' : 'its routes'} ${names}.`
    );
  }
  if (detachRoutes.length > 0) {
    const names = detachRoutes.map((d) => d.route.name).join(', ');
    parts.push(
      `${names} also ${detachRoutes.length === 1 ? 'serves' : 'serve'} another load balancer, so ${detachRoutes.length === 1 ? 'it stays' : 'they stay'} for that one.`
    );
  }
  parts.push("This can't be undone.");
  return parts.join(' ');
}

// Rows don't link anywhere yet: the NLB overview arrives with #802.
export function NlbTable({
  nlbs,
  routes,
  projectId,
}: {
  nlbs: NetworkLoadBalancer[];
  routes: NetworkLoadBalancerRoute[] | undefined;
  projectId?: string;
}) {
  const [deleteTarget, setDeleteTarget] = useState<NlbRow>();
  const deleteMutation = useDeleteNetworkLoadBalancer(projectId);
  const rows = useMemo(() => toNlbRows(nlbs, routes), [nlbs, routes]);

  const columns = useMemo<NlbColumn[]>(() => {
    const actions: ActionItem<NlbRow>[] = [
      {
        key: 'delete',
        label: 'Delete',
        variant: 'destructive',
        // Deleting needs the routes, or it would leave them behind.
        hidden: (row) => !row.routes,
        onClick: setDeleteTarget,
      },
    ];

    return [
      {
        id: 'name',
        accessorFn: (row) => row.nlb.name,
        header: ({ column }) => <SortableHeader column={column} title="Name" />,
        cell: ({ row }) => (
          <span className="truncate font-medium" title={row.original.nlb.name}>
            {row.original.nlb.name}
          </span>
        ),
      },
      {
        id: 'listeners',
        header: 'Listeners',
        enableSorting: false,
        cell: ({ row }) => (
          <div className="flex flex-wrap gap-1">
            {row.original.nlb.listeners.map((l) => (
              <ProtocolBadge key={l.name} protocol={l.protocol} port={l.port} />
            ))}
          </div>
        ),
      },
      {
        id: 'backends',
        header: 'Backends',
        enableSorting: false,
        cell: ({ row }) =>
          !row.original.backendNames ? (
            <span className="text-muted-foreground text-sm">—</span>
          ) : row.original.backendNames.length > 0 ? (
            <span className="font-mono text-xs">{row.original.backendNames.join(', ')}</span>
          ) : (
            <span className="text-muted-foreground text-sm">No route</span>
          ),
      },
      {
        id: 'status',
        accessorFn: (row) => READY_SORT_RANK[row.nlb.readyStatus],
        sortDescFirst: false,
        header: ({ column }) => <SortableHeader column={column} title="Status" />,
        cell: ({ row }) => (
          <span title={row.original.nlb.readyMessage}>
            <StatusBadge type={readyStatusToBadgeType(row.original.nlb.readyStatus)}>
              {nlbReadyStatusToLabel(row.original.nlb.readyStatus, row.original.nlb.readyReason)}
            </StatusBadge>
          </span>
        ),
      },
      {
        id: 'createdAt',
        accessorFn: (row) => row.nlb.createdAt.getTime(),
        header: ({ column }) => <SortableHeader column={column} title="Created" />,
        cell: ({ row }) => (
          <DateTime
            date={row.original.nlb.createdAt}
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
        cell: ({ row }) => (
          <div data-slot="dt-row-actions" className="flex justify-end">
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
  }, []);

  return (
    <>
      <DataTable.Client
        data={rows}
        columns={columns}
        getRowId={(row) => row.nlb.uid || row.nlb.name}
        defaultSort={[{ id: 'name', desc: false }]}
        searchFn={matchesSearch}
        className="space-y-6">
        <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <DataTable.Search placeholder="Search load balancers" className="w-full sm:max-w-xs" />
        </div>
        <div
          className="overflow-hidden rounded-lg border"
          data-testid="networking-plugin-nlb-table">
          <DataTable.Content
            emptyMessage={
              <p className="text-muted-foreground py-8 text-center text-sm">
                No load balancers match your search.
              </p>
            }
          />
        </div>
        <ConditionalPagination />
      </DataTable.Client>

      <ConfirmDeleteDialog
        open={deleteTarget !== undefined}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title={`Delete load balancer ${deleteTarget?.nlb.name ?? ''}?`}
        description={deleteDescription(deleteTarget)}
        onConfirm={async () => {
          if (!deleteTarget) return;
          try {
            await deleteMutation.mutateAsync({
              name: deleteTarget.nlb.name,
              routes: deleteTarget.routes ?? [],
            });
            toast.success(`Deleted load balancer ${deleteTarget.nlb.name}`);
          } catch (err) {
            toast.error((err as Error).message);
            throw err;
          }
        }}
      />
    </>
  );
}

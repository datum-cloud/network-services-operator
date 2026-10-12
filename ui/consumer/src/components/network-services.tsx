import { ConfirmDeleteDialog } from './confirm-delete-dialog';
import { CreateNetworkServiceDialog } from './create-network-service-dialog';
import { SortableHeader } from './sortable-header';
import { StatTile, StatTileGrid } from './stat-tiles';
import { ErrorOrRestrictedState, LoadingSkeleton } from './states';
import { StatusBadge } from './status-badge';
import { useDeleteNetworkService, useNetworkServices } from '../lib/api';
import {
  networkServiceMembersResolvedStatusToLabel,
  networkServiceReadyStatusToLabel,
  readyStatusToBadgeType,
  type NetworkService,
} from '../schema';
import {
  DataTable,
  useDataTablePagination,
  type DataTableFeatures,
} from '@datum-cloud/datum-ui/data-table';
import { DateTime } from '@datum-cloud/datum-ui/date-time';
import { EmptyContent } from '@datum-cloud/datum-ui/empty-content';
import { MoreActions, type ActionItem } from '@datum-cloud/datum-ui/more-actions';
import { toast } from '@datum-cloud/datum-ui/toast';
import type { ColumnDef } from '@tanstack/react-table';
import { CircleCheckIcon, ShareIcon, TriangleAlertIcon, UsersIcon } from 'lucide-react';
import { useMemo, useState } from 'react';

type ServiceColumn = ColumnDef<DataTableFeatures, NetworkService, unknown>;

function formatPorts(service: NetworkService): string {
  return service.ports.length > 0 ? service.ports.map((p) => `${p.name}:${p.port}`).join(', ') : '—';
}

function matchesSearch(service: NetworkService, search: string): boolean {
  const needle = search.trim().toLowerCase();
  if (!needle) return true;
  return `${service.name} ${formatPorts(service)}`.toLowerCase().includes(needle);
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

function ServiceTable({
  services,
  projectId,
  networkName,
}: {
  services: NetworkService[];
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const [deleteTarget, setDeleteTarget] = useState<NetworkService>();
  const deleteMutation = useDeleteNetworkService(projectId);

  const columns = useMemo<ServiceColumn[]>(() => {
    const actions: ActionItem<NetworkService>[] = [
      { key: 'delete', label: 'Delete', variant: 'destructive', onClick: setDeleteTarget },
    ];
    return [
      {
        id: 'name',
        accessorKey: 'name',
        header: ({ column }) => <SortableHeader column={column} title="Name" />,
        cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
      },
      {
        id: 'ports',
        header: 'Ports',
        enableSorting: false,
        cell: ({ row }) => <Muted mono>{formatPorts(row.original)}</Muted>,
      },
      {
        id: 'regions',
        accessorFn: (service) => service.summary.locations,
        header: ({ column }) => <SortableHeader column={column} title="Regions" />,
        cell: ({ row }) => <Muted>{row.original.summary.locations}</Muted>,
      },
      {
        id: 'members',
        accessorFn: (service) => service.summary.members,
        header: ({ column }) => <SortableHeader column={column} title="Members" />,
        cell: ({ row }) => <Muted>{row.original.summary.members}</Muted>,
      },
      {
        id: 'healthy',
        accessorFn: (service) => service.summary.healthy,
        header: ({ column }) => <SortableHeader column={column} title="Healthy" />,
        cell: ({ row }) => <Muted>{row.original.summary.healthy}</Muted>,
      },
      {
        id: 'resolved',
        header: 'Resolved',
        enableSorting: false,
        cell: ({ row }) => (
          <StatusBadge type={readyStatusToBadgeType(row.original.membersResolvedStatus)}>
            {networkServiceMembersResolvedStatusToLabel(
              row.original.membersResolvedStatus,
              row.original.membersResolvedReason
            )}
          </StatusBadge>
        ),
      },
      {
        id: 'ready',
        header: 'Ready',
        enableSorting: false,
        cell: ({ row }) => (
          <StatusBadge type={readyStatusToBadgeType(row.original.readyStatus)}>
            {networkServiceReadyStatusToLabel(row.original.readyStatus, row.original.readyReason)}
          </StatusBadge>
        ),
      },
      {
        id: 'createdAt',
        accessorFn: (service) => service.createdAt.getTime(),
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
        cell: ({ row }) => (
          <div data-slot="dt-row-actions" className="inline-flex">
            <MoreActions
              row={row.original}
              actions={actions}
              sheetTitle={`Actions for ${row.original.name}`}
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
        data={services}
        columns={columns}
        getRowId={(service) => service.uid || service.name}
        defaultSort={[{ id: 'name', desc: false }]}
        searchFn={matchesSearch}
        className="space-y-4">
        <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <DataTable.Search placeholder="Search services" className="w-full sm:max-w-xs" />
          <CreateNetworkServiceDialog projectId={projectId} networkName={networkName} />
        </div>
        <p className="text-muted-foreground text-xs">
          Services in this project — not limited to this network. A service's resolved network
          isn't recorded anywhere queryable, so this list can't be filtered to just this one.
        </p>
        <div className="overflow-hidden rounded-lg border" data-testid="networking-plugin-service-table">
          <DataTable.Content
            emptyMessage={
              <EmptyContent title="Try adjusting your search" className="w-full rounded-none border-0" />
            }
          />
        </div>
        <ConditionalPagination />
      </DataTable.Client>

      <ConfirmDeleteDialog
        open={deleteTarget !== undefined}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title={`Delete service ${deleteTarget?.name ?? ''}?`}
        description="This stops load-balancing traffic to its members. This can't be undone."
        onConfirm={async () => {
          if (!deleteTarget) return;
          try {
            await deleteMutation.mutateAsync(deleteTarget.name);
            toast.success(`Deleted service ${deleteTarget.name}`);
          } catch (err) {
            toast.error((err as Error).message);
            throw err;
          }
        }}
      />
    </>
  );
}

export function NetworkServices({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const { data: services, isLoading, error, refetch } = useNetworkServices(projectId);

  if (isLoading) return <LoadingSkeleton />;

  if (error) {
    return (
      <ErrorOrRestrictedState
        error={error}
        restrictedMessage="You don't have permission to view this project's services."
        onRetry={() => void refetch()}
      />
    );
  }

  const total = services?.length ?? 0;
  const ready = services?.filter((s) => s.readyStatus === 'True').length ?? 0;
  const notReady = total - ready;
  const totalMembers = services?.reduce((sum, s) => sum + s.summary.members, 0) ?? 0;

  if (total === 0) {
    return (
      <div className="flex flex-col items-center gap-4" data-testid="networking-plugin-network-services">
        <EmptyContent
          title="you don't have any services yet"
          subtitle="A NetworkService names a set of interfaces by label and load-balances traffic across every region they run in. Declare one to see it here."
        />
        <CreateNetworkServiceDialog projectId={projectId} networkName={networkName} />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-network-services">
      <StatTileGrid testId="networking-plugin-service-stats">
        <StatTile icon={ShareIcon} label="Total services" value={total} caption="in project" />
        <StatTile icon={CircleCheckIcon} label="Ready" value={ready} caption={`of ${total}`} />
        <StatTile icon={TriangleAlertIcon} label="Not ready" value={notReady} caption="needs attention" />
        <StatTile icon={UsersIcon} label="Members" value={totalMembers} caption="across all services" />
      </StatTileGrid>
      <ServiceTable services={services ?? []} projectId={projectId} networkName={networkName} />
    </div>
  );
}

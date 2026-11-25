import { ConfirmDeleteDialog } from './confirm-delete-dialog';
import {
  CreateNetworkServiceDialog,
  EditNetworkServiceDialog,
  editableWorkloadName,
} from './create-network-service-dialog';
import { SortableHeader } from './sortable-header';
import { StatTile, StatTileGrid } from './stat-tiles';
import { ServicesSkeleton } from './skeletons';
import { ErrorOrRestrictedState, SectionEmpty } from './states';
import { StatusBadge } from './status-badge';
import { TableSearch } from './table-search';
import {
  useDeleteNetworkService,
  useNetworkInterfaces,
  useNetworkPermissions,
  useNetworkServices,
  type NetworkPermissions,
} from '../lib/api';
import {
  matchesLabelSelector,
  networkServiceMembersResolvedStatusToLabel,
  networkServiceReadyStatusToLabel,
  readyStatusToBadgeType,
  type NetworkInterface,
  type NetworkService,
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
import { CircleCheckIcon, ShareIcon, TriangleAlertIcon, UsersIcon } from 'lucide-react';
import { useMemo, useState } from 'react';

type ServiceColumn = ColumnDef<DataTableFeatures, NetworkService, unknown>;

function formatPorts(service: NetworkService): string {
  return service.ports.length > 0 ? service.ports.map((p) => `${p.name}:${p.port}/${p.protocol ?? 'TCP'}`).join(', ') : '—';
}

function matchesSearch(service: NetworkService, search: string): boolean {
  const needle = search.trim().toLowerCase();
  if (!needle) return true;
  return `${service.name} ${formatPorts(service)}`.toLowerCase().includes(needle);
}

function partitionByNetwork(services: NetworkService[], interfaces: NetworkInterface[]) {
  const onNetwork: NetworkService[] = [];
  const elsewhere: NetworkService[] = [];
  for (const service of services) {
    const matches = interfaces.some((iface) => matchesLabelSelector(iface.labels, service.networkInterfaceSelector));
    (matches ? onNetwork : elsewhere).push(service);
  }
  return { onNetwork, elsewhere };
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

interface ServiceListProps {
  projectId: string | undefined;
  networkName: string | undefined;
  permissions: NetworkPermissions;
  onEdit: (service: NetworkService) => void;
  onDelete: (service: NetworkService) => void;
}

function ServiceTable({
  services,
  projectId,
  networkName,
  permissions,
  onEdit,
  onDelete,
  showCreate = true,
  testId = 'networking-plugin-service-table',
}: ServiceListProps & {
  services: NetworkService[];
  showCreate?: boolean;
  testId?: string;
}) {
  const { canCreateService, canUpdateService, canDeleteService } = permissions;

  const columns = useMemo<ServiceColumn[]>(() => {
    const actions: ActionItem<NetworkService>[] = [
      ...(canUpdateService
        ? [
            {
              key: 'edit',
              label: 'Edit',
              onClick: onEdit,
              disabled: (service: NetworkService) => editableWorkloadName(service) === undefined,
              tooltip: (service: NetworkService) =>
                editableWorkloadName(service) === undefined
                  ? 'This service uses a selector or port range the portal can\'t edit. Use kubectl or datumctl.'
                  : '',
            },
          ]
        : []),
      ...(canDeleteService
        ? [{ key: 'delete', label: 'Delete', variant: 'destructive' as const, onClick: onDelete }]
        : []),
    ];
    const actionsColumn: ServiceColumn = {
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
    };
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
      ...(canUpdateService || canDeleteService ? [actionsColumn] : []),
    ];
  }, [canUpdateService, canDeleteService, onEdit, onDelete]);

  return (
    <DataTable.Client
      data={services}
      columns={columns}
      getRowId={(service) => service.uid || service.name}
      defaultSort={[{ id: 'name', desc: false }]}
      searchFn={matchesSearch}
      className="space-y-4">
      <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <TableSearch placeholder="Search services" />
        {canCreateService && showCreate && (
          <CreateNetworkServiceDialog projectId={projectId} networkName={networkName} />
        )}
      </div>
      <div className="overflow-hidden rounded-lg border" data-testid={testId}>
        <DataTable.Content
          emptyMessage={
            <p className="text-muted-foreground py-8 text-center text-sm">
              No services match your search.
            </p>
          }
        />
      </div>
      <ConditionalPagination />
    </DataTable.Client>
  );
}

function ElsewhereServices({ services, ...props }: ServiceListProps & { services: NetworkService[] }) {
  if (services.length === 0) return null;
  const count = services.length === 1 ? '1 service' : `${services.length} services`;
  return (
    <details data-testid="networking-plugin-elsewhere-services">
      <summary className="text-muted-foreground cursor-pointer text-sm">
        {count} in this project {services.length === 1 ? "isn't" : "aren't"} on this network
      </summary>
      <p className="text-muted-foreground mt-2 mb-4 text-xs">
        Their selectors match no interface here. They run on another network, or match no
        interface yet.
      </p>
      <ServiceTable
        {...props}
        services={services}
        showCreate={false}
        testId="networking-plugin-elsewhere-service-table"
      />
    </details>
  );
}

export function NetworkServices({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const [editTarget, setEditTarget] = useState<NetworkService>();
  const [deleteTarget, setDeleteTarget] = useState<NetworkService>();
  const deleteMutation = useDeleteNetworkService(projectId);

  return (
    <>
      <ServicesContent
        projectId={projectId}
        networkName={networkName}
        onEdit={setEditTarget}
        onDelete={setDeleteTarget}
      />

      {editTarget && (
        <EditNetworkServiceDialog
          key={editTarget.uid || editTarget.name}
          projectId={projectId}
          networkName={networkName}
          service={editTarget}
          onOpenChange={(open) => !open && setEditTarget(undefined)}
        />
      )}

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

function ServicesContent({
  projectId,
  networkName,
  onEdit,
  onDelete,
}: Omit<ServiceListProps, 'permissions'>) {
  const { data: services, isLoading, error, refetch } = useNetworkServices(projectId);
  const interfacesQuery = useNetworkInterfaces(projectId, networkName);
  const permissions = useNetworkPermissions(projectId);
  const listProps = { projectId, networkName, permissions, onEdit, onDelete };

  if (isLoading || interfacesQuery.isLoading) return <ServicesSkeleton />;

  if (error) {
    return (
      <ErrorOrRestrictedState
        error={error}
        restrictedMessage="You don't have permission to view this project's services."
        onRetry={() => void refetch()}
      />
    );
  }

  const interfaces = interfacesQuery.data;
  const { onNetwork, elsewhere } = interfaces
    ? partitionByNetwork(services ?? [], interfaces)
    : { onNetwork: services ?? [], elsewhere: [] };

  const total = onNetwork.length;
  const ready = onNetwork.filter((s) => s.readyStatus === 'True').length;
  const notReady = total - ready;
  const totalMembers = onNetwork.reduce((sum, s) => sum + s.summary.members, 0);

  const elsewhereSection = (
<ElsewhereServices {...listProps} services={elsewhere} />
  );

  if (total === 0) {
    return (
      <div className="flex flex-col gap-6" data-testid="networking-plugin-network-services">
        <SectionEmpty
          title={elsewhere.length > 0 ? 'No services on this network' : 'No services yet'}
          description="A service load-balances traffic across a workload's instances in every region they run in."
          action={
            permissions.canCreateService ? (
              <CreateNetworkServiceDialog projectId={projectId} networkName={networkName} />
            ) : undefined
          }
        />
        {elsewhereSection}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-network-services">
      {!interfaces && (
        <p className="text-muted-foreground text-xs" data-testid="networking-plugin-services-unfiltered">
          This network's interfaces couldn't be loaded, so this lists every service in the project.
        </p>
      )}
      <StatTileGrid testId="networking-plugin-service-stats">
        <StatTile icon={ShareIcon} label="Total services" value={total} caption="on this network" />
        <StatTile icon={CircleCheckIcon} label="Ready" value={ready} caption={`of ${total}`} />
        <StatTile icon={TriangleAlertIcon} label="Not ready" value={notReady} caption="needs attention" />
        <StatTile icon={UsersIcon} label="Members" value={totalMembers} caption="across all services" />
      </StatTileGrid>
      <ServiceTable {...listProps} services={onNetwork} />
      {elsewhereSection}
    </div>
  );
}

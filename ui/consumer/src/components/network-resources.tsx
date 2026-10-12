import { SortableHeader } from './sortable-header';
import { ResourcesSkeleton } from './skeletons';
import { ErrorOrRestrictedState, SectionEmpty } from './states';
import { StatusBadge } from './status-badge';
import {
  computeWorkloadHref,
  useNetworkInterfaces,
  useNetworkServices,
  useSubnets,
} from '../lib/api';
import {
  holderAvailableStatusToLabel,
  isSubnetUsable,
  matchesLabelSelector,
  networkInterfacePrimaryAddress,
  readyStatusToBadgeType,
  subnetCidr,
  subnetReadyReasonToLabel,
  type NetworkInterface,
  type NetworkService,
  type Subnet,
} from '../schema';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import type { DataTableFeatures } from '@datum-cloud/datum-ui/data-table';
import { DateTime } from '@datum-cloud/datum-ui/date-time';
import { GroupedTable } from '@datum-cloud/datum-ui/grouped-table';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { Text } from '@datum-cloud/datum-ui/typography';
import type { ColumnDef } from '@tanstack/react-table';
import { MapPinIcon } from 'lucide-react';
import { Link } from 'react-router';

export interface RegionGroup {
  region: string;
  subnet?: Subnet;
  interfaces: NetworkInterface[];
}

export function groupByRegion(subnets: Subnet[], interfaces: NetworkInterface[]): RegionGroup[] {
  const groups = new Map<string, RegionGroup>();
  for (const subnet of subnets) {
    const region = subnet.location ?? subnet.name;
    groups.set(region, { region, subnet, interfaces: [] });
  }
  for (const iface of interfaces) {
    const region = iface.location ?? '—';
    const group = groups.get(region) ?? { region, interfaces: [] };
    group.interfaces.push(iface);
    groups.set(region, group);
  }
  return [...groups.values()].sort((a, b) => a.region.localeCompare(b.region));
}

export function regionStatus(subnet: Subnet | undefined): { label: string; usable: boolean } {
  if (!subnet) return { label: 'Unknown', usable: false };
  if (isSubnetUsable(subnet)) return { label: 'Ready', usable: true };
  if (subnet.readyStatus === 'False') {
    return { label: subnetReadyReasonToLabel(subnet.readyReason), usable: false };
  }
  return { label: 'Unknown', usable: false };
}

function servicesFor(iface: NetworkInterface, services: NetworkService[]): string[] {
  return services
    .filter((s) => matchesLabelSelector(iface.labels, s.networkInterfaceSelector))
    .map((s) => s.name);
}

function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}

function Summary({ groups }: { groups: RegionGroup[] }) {
  const interfaces = groups.flatMap((g) => g.interfaces);
  const workloads = new Set(interfaces.map((i) => i.workloadName).filter(Boolean)).size;
  const serving = interfaces.filter((i) => i.holderAvailableStatus === 'True').length;
  const regionsNeedingAttention = groups.filter((g) => !regionStatus(g.subnet).usable).length;
  const needsAttention = regionsNeedingAttention + interfaces.length - serving;

  return (
    <Text as="p" size="sm" textColor="muted" data-testid="networking-plugin-resources-summary">
      {[
        plural(groups.length, 'region', 'regions'),
        plural(workloads, 'workload', 'workloads'),
        plural(interfaces.length, 'instance', 'instances'),
        `${serving} serving`,
      ].join(' · ')}
      {' · '}
      <Text
        size="sm"
        textColor={needsAttention > 0 ? 'destructive' : 'muted'}
        weight={needsAttention > 0 ? 'medium' : undefined}>
        {plural(needsAttention, 'needs', 'need')} attention
      </Text>
    </Text>
  );
}

function RegionTitle({ group }: { group: RegionGroup }) {
  return (
    <span className="flex items-center gap-2" data-testid="region-row">
      <Icon icon={MapPinIcon} size={14} className="text-muted-foreground" />
      <Text size="xs" weight="medium">
        {group.region}
      </Text>
      <Text size="xs" textColor="muted" className="font-mono">
        {(group.subnet && subnetCidr(group.subnet)) ?? '—'}
      </Text>
    </span>
  );
}

function RegionMeta({ group }: { group: RegionGroup }) {
  const status = regionStatus(group.subnet);
  return (
    <span className="flex items-center gap-3" data-testid="region-meta">
      <Text size="xs" textColor="muted">
        {plural(group.interfaces.length, 'instance', 'instances')}
      </Text>
      <StatusBadge type={status.usable ? 'success' : group.subnet ? 'danger' : 'muted'}>
        {status.label}
      </StatusBadge>
    </span>
  );
}

function WorkloadCell({
  iface,
  projectId,
  showIndex,
}: {
  iface: NetworkInterface;
  projectId: string | undefined;
  showIndex: boolean;
}) {
  return (
    <span data-testid="network-interface-table-row">
      {projectId && iface.workloadName ? (
        <Link
          to={computeWorkloadHref(projectId, iface.workloadName)}
          className="font-medium hover:underline">
          {iface.workloadName}
        </Link>
      ) : (
        <Text className="font-medium">{iface.workloadName ?? iface.name}</Text>
      )}
      {showIndex && iface.instanceIndex !== undefined && (
        <Text textColor="muted">
          {' '}
          · instance {iface.instanceIndex}
        </Text>
      )}
    </span>
  );
}

type InterfaceColumn = ColumnDef<DataTableFeatures, NetworkInterface, unknown>;

const STATUS_SORT_RANK: Record<NetworkInterface['holderAvailableStatus'], number> = {
  False: 0,
  Unknown: 1,
  True: 2,
};

function resourceColumns(
  projectId: string | undefined,
  servicesByInterface: Map<NetworkInterface, string[]>,
  showServices: boolean,
  hasSiblings: (iface: NetworkInterface) => boolean
): InterfaceColumn[] {
  const servicesColumn: InterfaceColumn = {
    id: 'services',
    header: 'Services',
    enableSorting: false,
    cell: ({ row }) => {
      const names = servicesByInterface.get(row.original) ?? [];
      return (
        <Text size="sm" textColor="muted">
          {names.length > 0 ? names.join(', ') : '—'}
        </Text>
      );
    },
  };
  return [
    {
      id: 'workload',
      accessorFn: (iface) => `${iface.workloadName ?? iface.name}/${iface.instanceIndex ?? 0}`,
      header: ({ column }) => <SortableHeader column={column} title="Workload" />,
      cell: ({ row }) => (
        <WorkloadCell iface={row.original} projectId={projectId} showIndex={hasSiblings(row.original)} />
      ),
    },
    {
      id: 'address',
      header: 'Address',
      enableSorting: false,
      cell: ({ row }) => (
        <Text size="xs" textColor="muted" className="font-mono">
          {networkInterfacePrimaryAddress(row.original) ?? '—'}
        </Text>
      ),
    },
    ...(showServices ? [servicesColumn] : []),
    {
      id: 'status',
      accessorFn: (iface) => STATUS_SORT_RANK[iface.holderAvailableStatus],
      sortDescFirst: false,
      header: ({ column }) => <SortableHeader column={column} title="Status" />,
      size: 140,
      cell: ({ row }) => (
        <StatusBadge type={readyStatusToBadgeType(row.original.holderAvailableStatus)}>
          {holderAvailableStatusToLabel(
            row.original.holderAvailableStatus,
            row.original.holderAvailableReason
          )}
        </StatusBadge>
      ),
    },
    {
      id: 'age',
      accessorFn: (iface) => iface.createdAt.getTime(),
      header: ({ column }) => <SortableHeader column={column} title="Created" />,
      size: 140,
      cell: ({ row }) => (
        <DateTime
          date={row.original.createdAt}
          variant="relative"
          className="text-muted-foreground text-sm"
        />
      ),
    },
  ];
}

function ResourcesBody({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const subnetsQuery = useSubnets(projectId, networkName);
  const interfacesQuery = useNetworkInterfaces(projectId, networkName);
  const { data: services } = useNetworkServices(projectId);

  if (subnetsQuery.isLoading || interfacesQuery.isLoading) return <ResourcesSkeleton />;

  const error = subnetsQuery.error ?? interfacesQuery.error;
  if (error) {
    return (
      <ErrorOrRestrictedState
        error={error}
        restrictedMessage="You don't have permission to view this network's regions and workloads."
        onRetry={() => {
          void subnetsQuery.refetch();
          void interfacesQuery.refetch();
        }}
      />
    );
  }

  const groups = groupByRegion(subnetsQuery.data ?? [], interfacesQuery.data ?? []);

  if (groups.length === 0) {
    return (
      <SectionEmpty
        testId="networking-plugin-resources-empty"
        title="Not in any region yet"
        description="A network appears in a region once a workload on it is deployed there. Each instance then shows up here with its address and whether it's serving."
      />
    );
  }

  const servicesByInterface = new Map(
    groups.flatMap((g) => g.interfaces).map((iface) => [iface, servicesFor(iface, services ?? [])])
  );
  const showServices = [...servicesByInterface.values()].some((names) => names.length > 0);
  const instancesPerWorkload = new Map<string, number>();
  const workloadKey = (iface: NetworkInterface) => `${iface.location}/${iface.workloadName ?? iface.name}`;
  for (const iface of groups.flatMap((g) => g.interfaces)) {
    instancesPerWorkload.set(workloadKey(iface), (instancesPerWorkload.get(workloadKey(iface)) ?? 0) + 1);
  }
  const hasSiblings = (iface: NetworkInterface) => (instancesPerWorkload.get(workloadKey(iface)) ?? 0) > 1;

  return (
    <>
      <Summary groups={groups} />
      <Card
        size="sm"
        sectioned
        className="min-w-0 overflow-hidden"
        data-testid="networking-plugin-resources-table">
        <CardContent padding="none">
          <GroupedTable<NetworkInterface>
            columns={resourceColumns(projectId, servicesByInterface, showServices, hasSiblings)}
            groups={groups.map((group) => ({
              id: group.region,
              title: <RegionTitle group={group} />,
              meta: <RegionMeta group={group} />,
              rows: group.interfaces,
            }))}
            defaultExpanded="all"
            getRowId={(iface) => iface.uid || iface.name}
            empty={
              <div className="flex flex-col items-center gap-1 py-8 text-center">
                <p className="text-sm font-medium">No workloads yet</p>
                <p className="text-muted-foreground max-w-md text-sm">
                  This network has an address range in each region above. Deploy a workload on it to
                  see its instances here.
                </p>
              </div>
            }
            enableSorting
            className="usage-summary-grouped-table min-w-0 [&>div:last-child]:rounded-none [&>div:last-child]:border-0"
            tableClassName="[&_th:not(:last-child)]:border-r [&_th]:border-border [&_td:first-child]:pl-4 [&_td:last-child]:pr-4 [&_th:first-child]:pl-4 [&_th:last-child]:pr-4 sm:[&_td:first-child]:pl-5 sm:[&_td:last-child]:pr-5 sm:[&_th:first-child]:pl-5 sm:[&_th:last-child]:pr-5"
            headerRowClassName="bg-background hover:bg-background border-b border-border"
            headerCellClassName="text-foreground h-10 text-xs font-medium"
            groupHeaderClassName="bg-muted/40 text-foreground h-10 px-4 text-xs font-medium sm:px-5"
          />
        </CardContent>
      </Card>
    </>
  );
}

export function NetworkResources({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  return (
    <div className="flex flex-col gap-4" data-testid="networking-plugin-network-resources">
      <ResourcesBody projectId={projectId} networkName={networkName} />
    </div>
  );
}

import { ErrorOrRestrictedState, LoadingSkeleton } from './states';
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
import { Badge } from '@datum-cloud/datum-ui/badge';
import type { DataTableFeatures } from '@datum-cloud/datum-ui/data-table';
import { DateTime } from '@datum-cloud/datum-ui/date-time';
import { EmptyContent } from '@datum-cloud/datum-ui/empty-content';
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

function regionStatus(subnet: Subnet | undefined): { label: string; usable: boolean } {
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
      <Icon icon={MapPinIcon} size={14} />
      <Text size="sm" weight="medium">
        {group.region}
      </Text>
      <Text size="sm" textColor="muted" code>
        {(group.subnet && subnetCidr(group.subnet)) ?? '—'}
      </Text>
    </span>
  );
}

function RegionMeta({ group }: { group: RegionGroup }) {
  const status = regionStatus(group.subnet);
  return (
    <span className="flex items-center gap-3" data-testid="region-meta">
      <Text size="sm" textColor="muted">
        {plural(group.interfaces.length, 'instance', 'instances')}
      </Text>
      <Badge type={status.usable ? 'success' : group.subnet ? 'danger' : 'muted'} theme="light">
        {status.label}
      </Badge>
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
          className="text-sm underline underline-offset-2">
          {iface.workloadName}
        </Link>
      ) : (
        <Text size="sm">{iface.workloadName ?? iface.name}</Text>
      )}
      {showIndex && iface.instanceIndex !== undefined && (
        <Text size="sm" textColor="muted">
          {' '}
          · instance {iface.instanceIndex}
        </Text>
      )}
    </span>
  );
}

type InterfaceColumn = ColumnDef<DataTableFeatures, NetworkInterface, unknown>;

function resourceColumns(
  projectId: string | undefined,
  servicesByInterface: Map<NetworkInterface, string[]>,
  showServices: boolean,
  hasSiblings: (iface: NetworkInterface) => boolean
): InterfaceColumn[] {
  const servicesColumn: InterfaceColumn = {
    id: 'services',
    header: 'Services',
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
      header: 'Workload',
      cell: ({ row }) => (
        <WorkloadCell iface={row.original} projectId={projectId} showIndex={hasSiblings(row.original)} />
      ),
    },
    {
      id: 'address',
      header: 'Address',
      cell: ({ row }) => (
        <Text size="sm" textColor="muted" code>
          {networkInterfacePrimaryAddress(row.original) ?? '—'}
        </Text>
      ),
    },
    ...(showServices ? [servicesColumn] : []),
    {
      id: 'status',
      header: 'Status',
      size: 140,
      cell: ({ row }) => (
        <Badge type={readyStatusToBadgeType(row.original.holderAvailableStatus)} theme="light">
          {holderAvailableStatusToLabel(
            row.original.holderAvailableStatus,
            row.original.holderAvailableReason
          )}
        </Badge>
      ),
    },
    {
      id: 'age',
      header: 'Age',
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

  if (subnetsQuery.isLoading || interfacesQuery.isLoading) return <LoadingSkeleton />;

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
      <EmptyContent
        title="this network isn't present anywhere yet"
        subtitle="A network shows up in a region once something is deployed there. Deploy a workload on this network to see it here, with its address and whether it's serving traffic."
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
      <div data-testid="networking-plugin-resources-table">
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
            <EmptyContent
              title="no workloads on this network yet"
              subtitle="This network has an address range in each region above. Deploy a workload on it to see each instance here, with its address and whether it's serving traffic."
            />
          }
          groupHeaderClassName="bg-muted/40"
        />
      </div>
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

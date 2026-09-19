import { StatTile, StatTileGrid } from './stat-tiles';
import { ErrorOrRestrictedState, LoadingSkeleton } from './states';
import { useNetworkInterfaces } from '../lib/api';
import {
  holderAvailableStatusToLabel,
  networkInterfacePrimaryAddress,
  readyStatusToBadgeType,
  type NetworkInterface,
} from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { EmptyContent } from '@datum-cloud/datum-ui/empty-content';
import { Icon } from '@datum-cloud/datum-ui/icons';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@datum-cloud/datum-ui/table';
import { formatDistanceToNowStrict } from 'date-fns';
import { BoxesIcon, CircleCheckIcon, PlugZapIcon, TriangleAlertIcon } from 'lucide-react';

function InterfaceRow({ iface }: { iface: NetworkInterface }) {
  return (
    <TableRow data-testid="network-interface-table-row">
      <TableCell>
        <span className="text-sm font-medium">{iface.workloadName ?? iface.name}</span>
      </TableCell>
      <TableCell className="text-muted-foreground">{iface.location ?? '—'}</TableCell>
      <TableCell className="text-muted-foreground font-mono text-sm">
        {networkInterfacePrimaryAddress(iface) ?? '—'}
      </TableCell>
      <TableCell>
        <Badge type={readyStatusToBadgeType(iface.holderAvailableStatus)} theme="light">
          {holderAvailableStatusToLabel(iface.holderAvailableStatus, iface.holderAvailableReason)}
        </Badge>
      </TableCell>
      <TableCell className="text-muted-foreground">
        {formatDistanceToNowStrict(iface.createdAt, { addSuffix: true })}
      </TableCell>
    </TableRow>
  );
}

export function NetworkInterfaces({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const { data: interfaces, isLoading, error, refetch } = useNetworkInterfaces(projectId, networkName);

  if (isLoading) return <LoadingSkeleton />;

  if (error) {
    return (
      <ErrorOrRestrictedState
        error={error}
        restrictedMessage="You don't have permission to view this network's interfaces."
        onRetry={() => void refetch()}
      />
    );
  }

  const total = interfaces?.length ?? 0;
  const serving = interfaces?.filter((i) => i.holderAvailableStatus === 'True').length ?? 0;
  const needsAttention = total - serving;
  const workloads = new Set(interfaces?.map((i) => i.workloadName).filter(Boolean)).size;

  if (total === 0) {
    return (
      <EmptyContent
        title="you don't have any workloads on this network yet"
        subtitle="Deploy a workload on this network and it shows up here automatically, with its address and whether it's currently serving traffic."
      />
    );
  }

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-network-interfaces">
      <StatTileGrid testId="networking-plugin-interface-stats">
        <StatTile icon={BoxesIcon} label="Workloads" value={workloads} caption="connected" />
        <StatTile icon={PlugZapIcon} label="Instances" value={total} caption="across all workloads" />
        <StatTile icon={CircleCheckIcon} label="Serving" value={serving} caption={`of ${total}`} />
        <StatTile
          icon={TriangleAlertIcon}
          label="Needs attention"
          value={needsAttention}
          caption="not serving"
        />
      </StatTileGrid>

      <div
        className="border-border overflow-hidden rounded-lg border"
        data-testid="networking-plugin-interface-table">
        <div className="flex items-center gap-2 border-b p-4">
          <Icon icon={PlugZapIcon} size={16} />
          <span className="text-sm font-medium">Connected workloads</span>
          <Badge type="muted" theme="light">
            {total}
          </Badge>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Workload
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Region
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Address
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Status
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Age
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {interfaces?.map((iface) => <InterfaceRow key={iface.uid || iface.name} iface={iface} />)}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

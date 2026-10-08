import { StatTile, StatTileGrid } from './stat-tiles';
import { ErrorOrRestrictedState, LoadingSkeleton } from './states';
import { useSubnets } from '../lib/api';
import { readyStatusToBadgeType, subnetCidr, subnetReadyReasonToLabel, type Subnet } from '../schema';
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
import { CircleCheckIcon, MapPinIcon, TriangleAlertIcon } from 'lucide-react';

function readyBadgeLabel(subnet: Subnet): string {
  if (subnet.readyStatus === 'True') return 'Ready';
  if (subnet.readyStatus === 'False') return subnetReadyReasonToLabel(subnet.readyReason);
  return 'Unknown';
}

function LocationRow({ subnet }: { subnet: Subnet }) {
  return (
    <TableRow data-testid="subnet-table-row">
      <TableCell>
        <span className="text-sm font-medium">{subnet.location ?? '—'}</span>
      </TableCell>
      <TableCell className="text-muted-foreground font-mono text-sm">
        {subnetCidr(subnet) ?? '—'}
      </TableCell>
      <TableCell>
        <Badge type={readyStatusToBadgeType(subnet.readyStatus)} theme="light">
          {readyBadgeLabel(subnet)}
        </Badge>
      </TableCell>
      <TableCell className="text-muted-foreground">
        {formatDistanceToNowStrict(subnet.createdAt, { addSuffix: true })}
      </TableCell>
    </TableRow>
  );
}

export function NetworkSubnets({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const { data: subnets, isLoading, error, refetch } = useSubnets(projectId, networkName);

  if (isLoading) return <LoadingSkeleton />;

  if (error) {
    return (
      <ErrorOrRestrictedState
        error={error}
        restrictedMessage="You don't have permission to view this network's regions."
        onRetry={() => void refetch()}
      />
    );
  }

  const total = subnets?.length ?? 0;
  const ready = subnets?.filter((s) => s.readyStatus === 'True').length ?? 0;
  const notReady = total - ready;

  if (total === 0) {
    return (
      <EmptyContent
        title="this network isn't present anywhere yet"
        subtitle="A network shows up in a region once something is deployed there. Deploy a workload on this network to see it here."
      />
    );
  }

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-network-subnets">
      <StatTileGrid testId="networking-plugin-subnet-stats">
        <StatTile icon={MapPinIcon} label="Regions" value={total} caption="this network reaches" />
        <StatTile icon={CircleCheckIcon} label="Ready" value={ready} caption={`of ${total}`} />
        <StatTile
          icon={TriangleAlertIcon}
          label="Needs attention"
          value={notReady}
          caption="not ready"
        />
      </StatTileGrid>

      <div
        className="border-border overflow-hidden rounded-lg border"
        data-testid="networking-plugin-subnet-table">
        <div className="flex items-center gap-2 border-b p-4">
          <Icon icon={MapPinIcon} size={16} />
          <span className="text-sm font-medium">Regions</span>
          <Badge type="muted" theme="light">
            {total}
          </Badge>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Region
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Address range
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
            {subnets?.map((subnet) => <LocationRow key={subnet.uid || subnet.name} subnet={subnet} />)}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

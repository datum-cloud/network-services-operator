import { ConfirmDeleteDialog } from './confirm-delete-dialog';
import { CreateNetworkServiceDialog } from './create-network-service-dialog';
import { StatTile, StatTileGrid } from './stat-tiles';
import { ErrorOrRestrictedState, LoadingSkeleton } from './states';
import { useDeleteNetworkService, useNetworkServices } from '../lib/api';
import {
  networkServiceMembersResolvedStatusToLabel,
  networkServiceReadyStatusToLabel,
  readyStatusToBadgeType,
  type NetworkService,
} from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { Button } from '@datum-cloud/datum-ui/button';
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
import { toast } from '@datum-cloud/datum-ui/toast';
import { formatDistanceToNowStrict } from 'date-fns';
import { CircleCheckIcon, ShareIcon, Trash2Icon, TriangleAlertIcon, UsersIcon } from 'lucide-react';

function ServiceRow({
  service,
  projectId,
}: {
  service: NetworkService;
  projectId: string | undefined;
}) {
  const deleteMutation = useDeleteNetworkService(projectId);

  return (
    <TableRow data-testid="network-service-table-row">
      <TableCell>
        <span className="font-mono text-sm">{service.name}</span>
      </TableCell>
      <TableCell className="text-muted-foreground font-mono text-sm">
        {service.ports.length > 0
          ? service.ports.map((p) => `${p.name}:${p.port}`).join(', ')
          : '—'}
      </TableCell>
      <TableCell className="text-muted-foreground">{service.summary.locations}</TableCell>
      <TableCell className="text-muted-foreground">{service.summary.members}</TableCell>
      <TableCell className="text-muted-foreground">{service.summary.healthy}</TableCell>
      <TableCell>
        <Badge type={readyStatusToBadgeType(service.membersResolvedStatus)} theme="light">
          {networkServiceMembersResolvedStatusToLabel(
            service.membersResolvedStatus,
            service.membersResolvedReason
          )}
        </Badge>
      </TableCell>
      <TableCell>
        <Badge type={readyStatusToBadgeType(service.readyStatus)} theme="light">
          {networkServiceReadyStatusToLabel(service.readyStatus, service.readyReason)}
        </Badge>
      </TableCell>
      <TableCell className="text-muted-foreground">
        {formatDistanceToNowStrict(service.createdAt, { addSuffix: true })}
      </TableCell>
      <TableCell>
        <ConfirmDeleteDialog
          trigger={
            <Button
              type="secondary"
              theme="outline"
              size="icon"
              aria-label={`Delete ${service.name}`}
              icon={<Trash2Icon size={14} />}
            />
          }
          title={`Delete ${service.name}?`}
          description="This stops load-balancing traffic to its members. This can't be undone."
          onConfirm={async () => {
            try {
              await deleteMutation.mutateAsync(service.name);
              toast.success(`Deleted service ${service.name}`);
            } catch (err) {
              toast.error((err as Error).message);
              throw err;
            }
          }}
        />
      </TableCell>
    </TableRow>
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
      <div className="flex justify-end">
        <CreateNetworkServiceDialog projectId={projectId} networkName={networkName} />
      </div>
      <StatTileGrid testId="networking-plugin-service-stats">
        <StatTile icon={ShareIcon} label="Total services" value={total} caption="in project" />
        <StatTile icon={CircleCheckIcon} label="Ready" value={ready} caption={`of ${total}`} />
        <StatTile icon={TriangleAlertIcon} label="Not ready" value={notReady} caption="needs attention" />
        <StatTile icon={UsersIcon} label="Members" value={totalMembers} caption="across all services" />
      </StatTileGrid>

      <div
        className="border-border overflow-hidden rounded-lg border"
        data-testid="networking-plugin-service-table">
        <div className="flex flex-col gap-1 border-b p-4">
          <div className="flex items-center gap-2">
            <Icon icon={ShareIcon} size={16} />
            <span className="text-sm font-medium">Services</span>
            <Badge type="muted" theme="light">
              {total}
            </Badge>
          </div>
          <p className="text-muted-foreground text-xs">
            Services in this project — not limited to this network. A service's resolved network
            isn't recorded anywhere queryable, so this list can't be filtered to just this one.
          </p>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Name
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Ports
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Regions
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Members
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Healthy
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Resolved
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Ready
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                Age
              </TableHead>
              <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {services?.map((service) => (
              <ServiceRow key={service.uid || service.name} service={service} projectId={projectId} />
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

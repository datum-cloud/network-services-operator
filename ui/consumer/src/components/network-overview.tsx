import { DetailCard, DetailList } from './detail-card';
import { groupByRegion, regionStatus, type RegionGroup } from './network-resources';
import { StatusBadge } from './status-badge';
import { useNetworkInterfaces, useSubnets } from '../lib/api';
import {
  readyReasonToLabel,
  readyStatusToBadgeType,
  readyStatusToLabel,
  subnetCidr,
  type Network,
} from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { DateTime } from '@datum-cloud/datum-ui/date-time';
import { EmptyContent } from '@datum-cloud/datum-ui/empty-content';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import {
  CircleCheckIcon,
  ClockIcon,
  MapPinIcon,
  Share2Icon,
  SquareLibraryIcon,
  TriangleAlertIcon,
  type LucideIcon,
} from 'lucide-react';
import { Link } from 'react-router';

type Tone = 'success' | 'warning' | 'danger' | 'muted';

const TONE_COLOR: Record<Tone, string> = {
  success: 'var(--color-badge-success)',
  warning: 'var(--color-badge-warning)',
  danger: 'var(--color-badge-danger)',
  muted: 'var(--color-muted-foreground)',
};

function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}

function Chip({ tone, icon, children }: { tone: Tone; icon: LucideIcon; children: React.ReactNode }) {
  return (
    <Badge
      type={tone}
      theme={tone === 'muted' ? 'solid' : 'light'}
      className="h-6 gap-1.5 rounded-md px-2 text-xs font-medium whitespace-nowrap">
      <Icon icon={icon} size={12} className="shrink-0" />
      {children}
    </Badge>
  );
}

function headline(network: Network): { tone: Tone; icon: LucideIcon; title: string; detail: string } {
  if (network.readyStatus === 'True') {
    return {
      tone: 'success',
      icon: CircleCheckIcon,
      title: 'Network is ready',
      detail: 'Workloads on this network get addresses in every region it reaches.',
    };
  }
  if (network.readyStatus === 'False') {
    return {
      tone: 'danger',
      icon: TriangleAlertIcon,
      title: readyReasonToLabel(network.readyReason),
      detail: network.readyMessage ?? "This network can't hand out addresses until this is resolved.",
    };
  }
  return {
    tone: 'muted',
    icon: ClockIcon,
    title: 'Waiting for status',
    detail: "This network hasn't reported whether it's ready yet.",
  };
}

function HealthStrip({
  network,
  groups,
  loading,
}: {
  network: Network;
  groups: RegionGroup[];
  loading: boolean;
}) {
  const { tone, icon, title, detail } = headline(network);
  const instances = groups.reduce((sum, g) => sum + g.interfaces.length, 0);
  const regionsDown = groups.filter((g) => !regionStatus(g.subnet).usable).length;

  return (
    <Card size="sm" data-testid="networking-plugin-network-health">
      <CardContent className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 items-center gap-3">
          <span
            className="flex size-9 shrink-0 items-center justify-center rounded-full"
            style={{ background: `color-mix(in oklab, ${TONE_COLOR[tone]} 10%, transparent)` }}>
            <Icon icon={icon} size={18} style={{ color: TONE_COLOR[tone] }} />
          </span>
          <div className="flex min-w-0 flex-col">
            <span className="truncate text-sm font-semibold">{title}</span>
            <span className="text-muted-foreground text-xs">{detail}</span>
          </div>
        </div>
        {!loading && (
          <div className="flex flex-wrap items-center gap-2 sm:justify-end">
            <Chip tone={regionsDown > 0 ? 'warning' : 'muted'} icon={MapPinIcon}>
              {regionsDown > 0
                ? `${regionsDown} of ${plural(groups.length, 'region', 'regions')} need attention`
                : plural(groups.length, 'region', 'regions')}
            </Chip>
            <Chip tone="muted" icon={Share2Icon}>
              {plural(instances, 'instance', 'instances')}
            </Chip>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function GeneralCard({ network }: { network: Network }) {
  return (
    <DetailCard icon={SquareLibraryIcon} title="General" testId="networking-plugin-general-card">
      <DetailList
        items={[
          {
            label: 'Status',
            content: (
              <StatusBadge type={readyStatusToBadgeType(network.readyStatus)}>
                {readyStatusToLabel(network.readyStatus, network.readyReason)}
              </StatusBadge>
            ),
          },
          { label: 'Resource name', content: <span className="font-mono text-sm">{network.name}</span> },
          { label: 'Created', content: <DateTime date={network.createdAt} variant="both" /> },
        ]}
      />
    </DetailCard>
  );
}

function IPConfigurationCard({ network }: { network: Network }) {
  return (
    <DetailCard icon={Share2Icon} title="IP Configuration" testId="networking-plugin-ip-card">
      <DetailList
        items={[
          {
            label: 'IPv6 prefix',
            content: <span className="font-mono text-sm">{network.ipv6Prefix ?? '—'}</span>,
          },
          {
            label: 'IP families',
            content:
              network.ipFamilies.length > 0 ? (
                <span className="flex gap-2">
                  {network.ipFamilies.map((family) => (
                    <Badge key={family} type="muted" theme="light">
                      {family}
                    </Badge>
                  ))}
                </span>
              ) : (
                '—'
              ),
          },
          { label: 'IPAM mode', content: network.ipamMode ?? '—' },
          { label: 'MTU', content: network.mtu ?? '—' },
        ]}
      />
    </DetailCard>
  );
}

function RegionsCard({
  groups,
  loading,
  resourcesHref,
}: {
  groups: RegionGroup[];
  loading: boolean;
  resourcesHref: string;
}) {
  return (
    <DetailCard
      icon={MapPinIcon}
      title="Regions"
      testId="networking-plugin-regions-card"
      action={
        <Link to={resourcesHref} className="text-xs font-medium hover:underline">
          View resources
        </Link>
      }>
      {loading ? (
        <div className="flex flex-col gap-3 p-4">
          <Skeleton className="h-5 w-full" />
          <Skeleton className="h-5 w-full" />
        </div>
      ) : groups.length === 0 ? (
        <EmptyContent
          size="sm"
          variant="minimal"
          title="this network isn't present anywhere yet"
          subtitle="A network shows up in a region once something is deployed there."
        />
      ) : (
        <DetailList
          items={groups.map((group) => {
            const status = regionStatus(group.subnet);
            return {
              label: (
                <span className="flex items-center gap-2">
                  <Icon icon={MapPinIcon} size={14} />
                  {group.region}
                </span>
              ),
              content: (
                <span className="flex flex-wrap items-center gap-3">
                  <StatusBadge type={status.usable ? 'success' : group.subnet ? 'danger' : 'muted'}>
                    {status.label}
                  </StatusBadge>
                  <span className="text-muted-foreground font-mono text-xs">
                    {(group.subnet && subnetCidr(group.subnet)) ?? '—'}
                  </span>
                  <span className="text-muted-foreground text-xs">
                    {plural(group.interfaces.length, 'instance', 'instances')}
                  </span>
                </span>
              ),
            };
          })}
        />
      )}
    </DetailCard>
  );
}

export function NetworkOverview({
  network,
  projectId,
  resourcesHref,
}: {
  network: Network;
  projectId: string | undefined;
  resourcesHref: string;
}) {
  const subnets = useSubnets(projectId, network.name);
  const interfaces = useNetworkInterfaces(projectId, network.name);
  const loading = subnets.isLoading || interfaces.isLoading;
  const groups = groupByRegion(subnets.data ?? [], interfaces.data ?? []);

  return (
    <div className="flex flex-col gap-6" data-testid="networking-plugin-network-overview">
      <HealthStrip network={network} groups={groups} loading={loading} />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <GeneralCard network={network} />
        <IPConfigurationCard network={network} />
      </div>
      <RegionsCard groups={groups} loading={loading} resourcesHref={resourcesHref} />
    </div>
  );
}

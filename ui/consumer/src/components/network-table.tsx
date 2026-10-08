import { EnableIPv6Dialog } from './enable-ipv6-dialog';
import { readyReasonToLabel, readyStatusToBadgeType, type Network } from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { InputGroup, InputGroupAddon, InputGroupInput } from '@datum-cloud/datum-ui/input-group';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@datum-cloud/datum-ui/table';
import { cn } from '@datum-cloud/datum-ui/utils';
import { formatDistanceToNowStrict } from 'date-fns';
import { ArrowDownIcon, ArrowUpIcon, ChevronsUpDownIcon, NetworkIcon, SearchIcon } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useLocation, useNavigate } from 'react-router';

export type NetworkSortKey = 'name' | 'ready';
export type SortDirection = 'asc' | 'desc';

const READY_SORT_RANK: Record<Network['readyStatus'], number> = {
  False: 0,
  Unknown: 1,
  True: 2,
};

function compareNetworks(a: Network, b: Network, key: NetworkSortKey): number {
  if (key === 'name') return a.name.localeCompare(b.name);
  return READY_SORT_RANK[a.readyStatus] - READY_SORT_RANK[b.readyStatus];
}

function readyBadgeLabel(network: Network): string {
  if (network.readyStatus === 'True') return 'Ready';
  if (network.readyStatus === 'False') return readyReasonToLabel(network.readyReason);
  return 'Unknown';
}

function SortableHead({
  label,
  sortKey,
  activeKey,
  direction,
  onSort,
}: {
  label: string;
  sortKey: NetworkSortKey;
  activeKey: NetworkSortKey;
  direction: SortDirection;
  onSort: (key: NetworkSortKey) => void;
}) {
  const isActive = activeKey === sortKey;
  const icon = isActive ? (direction === 'asc' ? ArrowUpIcon : ArrowDownIcon) : ChevronsUpDownIcon;

  return (
    <TableHead className="text-muted-foreground text-xs font-medium tracking-wide uppercase">
      <button
        type="button"
        onClick={() => onSort(sortKey)}
        className={cn(
          'flex items-center gap-1 transition-colors',
          isActive ? 'text-foreground' : 'hover:text-foreground'
        )}
        aria-label={`Sort by ${label}`}
        aria-sort={isActive ? (direction === 'asc' ? 'ascending' : 'descending') : 'none'}>
        {label}
        <Icon icon={icon} size={14} />
      </button>
    </TableHead>
  );
}

function PlainHead({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <TableHead
      className={cn('text-muted-foreground text-xs font-medium tracking-wide uppercase', className)}>
      {children}
    </TableHead>
  );
}

function NetworkRow({
  network,
  projectId,
  onOpen,
}: {
  network: Network;
  projectId: string | undefined;
  onOpen: (name: string) => void;
}) {
  const needsIPv6 = !network.ipFamilies.includes('IPv6');

  return (
    <TableRow
      data-testid="network-table-row"
      className="hover:bg-muted/50 cursor-pointer"
      onClick={() => onOpen(network.name)}>
      <TableCell>
        <div className="flex items-center gap-3">
          <span className="bg-muted flex size-8 shrink-0 items-center justify-center rounded-md">
            <Icon icon={NetworkIcon} size={16} className="text-muted-foreground" />
          </span>
          <span className="font-mono text-sm">{network.name}</span>
        </div>
      </TableCell>
      <TableCell className="text-muted-foreground font-mono text-sm">
        {network.ipv6Prefix ?? '—'}
      </TableCell>
      <TableCell>
        <Badge type={readyStatusToBadgeType(network.readyStatus)} theme="light">
          {readyBadgeLabel(network)}
        </Badge>
      </TableCell>
      <TableCell className="text-muted-foreground">
        {formatDistanceToNowStrict(network.createdAt, { addSuffix: true })}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {network.ipFamilies.length > 0 ? network.ipFamilies.join(', ') : '—'}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {network.ipamMode ?? '—'}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {network.mtu ?? '—'}
      </TableCell>
      <TableCell onClick={(e) => e.stopPropagation()}>
        {needsIPv6 ? <EnableIPv6Dialog projectId={projectId} network={network} /> : null}
      </TableCell>
    </TableRow>
  );
}

export function NetworkTable({
  networks,
  projectId,
}: {
  networks: Network[];
  projectId?: string;
}) {
  const [sortKey, setSortKey] = useState<NetworkSortKey>('name');
  const [direction, setDirection] = useState<SortDirection>('asc');
  const [search, setSearch] = useState('');
  const navigate = useNavigate();
  const location = useLocation();
  const basePath = location.pathname.replace(/\/$/, '');
  const handleOpen = (name: string) => navigate(`${basePath}/${name}`);

  const handleSort = (key: NetworkSortKey) => {
    if (key === sortKey) {
      setDirection((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      setDirection('asc');
    }
  };

  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return networks;
    return networks.filter((n) => n.name.toLowerCase().includes(query));
  }, [networks, search]);

  const sorted = useMemo(() => {
    const copy = [...filtered];
    copy.sort((a, b) => {
      const cmp = compareNetworks(a, b, sortKey);
      return direction === 'asc' ? cmp : -cmp;
    });
    return copy;
  }, [filtered, sortKey, direction]);

  return (
    <div className="border-border overflow-hidden rounded-lg border" data-testid="networking-plugin-network-table">
      <div className="flex items-center justify-between gap-4 border-b p-4">
        <div className="flex items-center gap-2">
          <Icon icon={NetworkIcon} size={16} />
          <span className="text-sm font-medium">Networks</span>
          <Badge type="muted" theme="light">
            {networks.length}
          </Badge>
        </div>
        <InputGroup className="w-full max-w-xs">
          <InputGroupAddon align="inline-start">
            <Icon icon={SearchIcon} size={14} className="text-muted-foreground" />
          </InputGroupAddon>
          <InputGroupInput
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search networks..."
            aria-label="Search networks"
          />
        </InputGroup>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <SortableHead
              label="Name"
              sortKey="name"
              activeKey={sortKey}
              direction={direction}
              onSort={handleSort}
            />
            <PlainHead>IPv6 Prefix</PlainHead>
            <SortableHead
              label="Ready"
              sortKey="ready"
              activeKey={sortKey}
              direction={direction}
              onSort={handleSort}
            />
            <PlainHead>Age</PlainHead>
            <PlainHead>IP Families</PlainHead>
            <PlainHead>IPAM Mode</PlainHead>
            <PlainHead>MTU</PlainHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {sorted.map((network) => (
            <NetworkRow
              key={network.uid || network.name}
              network={network}
              projectId={projectId}
              onOpen={handleOpen}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

import { StatTile, StatTileGrid } from './stat-tiles';
import type { Network } from '../schema';
import { CircleCheckIcon, LayersIcon, NetworkIcon, TriangleAlertIcon } from 'lucide-react';

export function NetworkStats({ networks }: { networks: Network[] }) {
  const total = networks.length;
  const ready = networks.filter((n) => n.readyStatus === 'True').length;
  const needsAttention = total - ready;
  const dualStack = networks.filter(
    (n) => n.ipFamilies.includes('IPv4') && n.ipFamilies.includes('IPv6')
  ).length;

  return (
    <StatTileGrid testId="networking-plugin-network-stats">
      <StatTile icon={NetworkIcon} label="Networks" value={total} caption="in project" />
      <StatTile icon={CircleCheckIcon} label="Ready" value={ready} caption={`of ${total}`} />
      <StatTile
        icon={TriangleAlertIcon}
        label="Needs attention"
        value={needsAttention}
        caption="not ready"
      />
      <StatTile icon={LayersIcon} label="Dual-stack" value={dualStack} caption="IPv4 + IPv6" />
    </StatTileGrid>
  );
}

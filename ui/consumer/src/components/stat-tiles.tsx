import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { Icon } from '@datum-cloud/datum-ui/icons';
import type { LucideIcon } from 'lucide-react';

export function StatTile({
  icon,
  label,
  value,
  caption,
}: {
  icon: LucideIcon;
  label: string;
  value: number;
  caption: string;
}) {
  return (
    <Card>
      <CardContent className="flex flex-col gap-2">
        <div className="text-muted-foreground flex items-center gap-1.5 text-sm">
          <Icon icon={icon} size={16} />
          {label}
        </div>
        <span className="text-3xl font-semibold tabular-nums">{value}</span>
        <span className="text-muted-foreground text-xs">{caption}</span>
      </CardContent>
    </Card>
  );
}

export function StatTileGrid({
  children,
  testId,
}: {
  children: React.ReactNode;
  testId?: string;
}) {
  return (
    <div
      className="grid gap-4"
      style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))' }}
      data-testid={testId}>
      {children}
    </div>
  );
}

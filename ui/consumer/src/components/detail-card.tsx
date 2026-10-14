import { Card, CardAction, CardContent, CardField, CardFieldLabel, CardFieldValue, CardHeader, CardTitle } from '@datum-cloud/datum-ui/card';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { cn } from '@datum-cloud/datum-ui/utils';
import type { LucideIcon } from 'lucide-react';

export interface DetailListItem {
  label: React.ReactNode;
  content: React.ReactNode;
  hidden?: boolean;
}

export function DetailList({ items }: { items: DetailListItem[] }) {
  return (
    <div className="flex flex-col">
      {items
        .filter((item) => !item.hidden)
        .map((item, index) => (
          <CardField key={index}>
            <CardFieldLabel>{item.label}</CardFieldLabel>
            <CardFieldValue className="min-w-0 wrap-break-word">{item.content}</CardFieldValue>
          </CardField>
        ))}
    </div>
  );
}

export function DetailCard({
  icon,
  title,
  action,
  className,
  testId,
  children,
}: {
  icon: LucideIcon;
  title: string;
  action?: React.ReactNode;
  className?: string;
  testId?: string;
  children: React.ReactNode;
}) {
  return (
    <Card size="sm" sectioned className={cn('w-full overflow-hidden', className)} data-testid={testId}>
      <CardHeader size="sm" bordered>
        <CardTitle className="flex items-center gap-2 text-sm">
          <Icon icon={icon} size={16} className="text-secondary" />
          {title}
        </CardTitle>
        {action && <CardAction>{action}</CardAction>}
      </CardHeader>
      <CardContent padding="none">{children}</CardContent>
    </Card>
  );
}

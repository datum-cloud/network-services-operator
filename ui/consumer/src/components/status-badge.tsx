import type { BadgeType } from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { SpinnerIcon } from '@datum-cloud/datum-ui/icons';
import { cn } from '@datum-cloud/datum-ui/utils';

export function StatusBadge({
  type,
  pending,
  className,
  children,
}: {
  type: BadgeType;
  pending?: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="w-fit">
      <Badge
        type={type}
        theme="light"
        className={cn(
          'text-5xs flex cursor-default items-center gap-1.5 px-1 py-0.5 font-bold tracking-[0.03em] uppercase',
          className
        )}>
        {pending && <SpinnerIcon size="xs" aria-hidden />}
        {children}
      </Badge>
    </div>
  );
}

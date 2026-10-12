import type { DataTableFeatures } from '@datum-cloud/datum-ui/data-table';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { cn } from '@datum-cloud/datum-ui/utils';
import type { Column, RowData } from '@tanstack/react-table';
import { ChevronDown, ChevronUp } from 'lucide-react';

export function SortableHeader<TData extends RowData>({
  column,
  title,
}: {
  column: Column<DataTableFeatures, TData, unknown>;
  title: string;
}) {
  if (!column.getCanSort()) {
    return <div data-slot="dt-column-header">{title}</div>;
  }

  const sorted = column.getIsSorted();
  return (
    <div
      className="flex h-full cursor-pointer items-center justify-between gap-1 select-none"
      onClick={column.getToggleSortingHandler()}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          column.getToggleSortingHandler()?.(event);
        }
      }}
      role="button"
      tabIndex={0}
      aria-label={`Sort by ${title}${
        sorted === 'asc' ? ', sorted ascending' : sorted === 'desc' ? ', sorted descending' : ''
      }`}
      data-slot="dt-column-header">
      <span>{title}</span>
      <div className="flex flex-col">
        <Icon
          icon={ChevronUp}
          size={10}
          aria-hidden="true"
          className={cn(
            'text-foreground -mb-0.5 stroke-2 opacity-25 transition-all',
            sorted === 'asc' && 'opacity-100'
          )}
        />
        <Icon
          icon={ChevronDown}
          size={10}
          aria-hidden="true"
          className={cn(
            'text-foreground -mt-0.5 stroke-2 opacity-25 transition-all',
            sorted === 'desc' && 'opacity-100'
          )}
        />
      </div>
    </div>
  );
}

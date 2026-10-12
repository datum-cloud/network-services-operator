import type { ActionItem } from '@datum-cloud/datum-ui/more-actions';
import { useState } from 'react';

export type { ActionItem };

export function MoreActions<TData>({
  row,
  actions,
  sheetTitle = 'Actions',
}: {
  row: TData;
  actions: ActionItem<TData>[];
  sheetTitle?: string;
}) {
  const [open, setOpen] = useState(false);
  const visible = actions.filter((a) =>
    typeof a.hidden === 'function' ? !a.hidden(row) : !a.hidden
  );
  return (
    <>
      <button type="button" aria-label={sheetTitle} onClick={() => setOpen(!open)} />
      {open &&
        visible.map((a) => (
          <button
            key={a.key ?? a.label}
            type="button"
            onClick={() => {
              a.onClick(row);
              setOpen(false);
            }}>
            {a.label}
          </button>
        ))}
    </>
  );
}

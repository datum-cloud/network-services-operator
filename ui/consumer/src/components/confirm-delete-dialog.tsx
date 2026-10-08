import { Button } from '@datum-cloud/datum-ui/button';
import { Dialog } from '@datum-cloud/datum-ui/dialog';
import { useState } from 'react';

export function ConfirmDeleteDialog({
  trigger,
  title,
  description,
  confirmLabel = 'Delete',
  onConfirm,
}: {
  trigger: React.ReactNode;
  title: React.ReactNode;
  description: React.ReactNode;
  confirmLabel?: string;
  onConfirm: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [isPending, setIsPending] = useState(false);

  const handleConfirm = async () => {
    setIsPending(true);
    try {
      await onConfirm();
      setOpen(false);
    } catch {
      // onConfirm surfaces its own error; keep the dialog open so the user can retry.
    } finally {
      setIsPending(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>{trigger}</Dialog.Trigger>
      <Dialog.Content>
        <Dialog.Header title={title} description={description} />
        <Dialog.Footer>
          <Button type="secondary" htmlType="button" onClick={() => setOpen(false)} disabled={isPending}>
            Cancel
          </Button>
          <Button type="danger" htmlType="button" loading={isPending} onClick={() => void handleConfirm()}>
            {confirmLabel}
          </Button>
        </Dialog.Footer>
      </Dialog.Content>
    </Dialog>
  );
}

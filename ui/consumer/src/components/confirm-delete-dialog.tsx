import { Button } from '@datum-cloud/datum-ui/button';
import { Dialog } from '@datum-cloud/datum-ui/dialog';
import { Input } from '@datum-cloud/datum-ui/input';
import { Label } from '@datum-cloud/datum-ui/label';
import { useId, useState } from 'react';

const CONFIRM_VALUE = 'DELETE';

export function ConfirmDeleteDialog({
  trigger,
  open: controlledOpen,
  onOpenChange,
  title,
  description,
  confirmLabel = 'Delete',
  onConfirm,
}: {
  trigger?: React.ReactNode;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  title: React.ReactNode;
  description: React.ReactNode;
  confirmLabel?: string;
  onConfirm: () => Promise<void>;
}) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
  const [confirmText, setConfirmText] = useState('');
  const [isPending, setIsPending] = useState(false);
  const inputId = useId();

  const open = controlledOpen ?? uncontrolledOpen;
  const canSubmit = confirmText === CONFIRM_VALUE && !isPending;

  const setOpen = (next: boolean) => {
    if (!next) setConfirmText('');
    setUncontrolledOpen(next);
    onOpenChange?.(next);
  };

  const submit = async () => {
    if (!canSubmit) return;
    setIsPending(true);
    try {
      await onConfirm();
      setOpen(false);
    } catch {
      return;
    } finally {
      setIsPending(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !isPending && setOpen(next)}>
      {trigger && <Dialog.Trigger asChild>{trigger}</Dialog.Trigger>}
      <Dialog.Content>
        <Dialog.Header
          title={title}
          description={description}
          descriptionClassName="break-all"
          onClose={isPending ? undefined : () => setOpen(false)}
        />
        <Dialog.Body className="px-5 py-0">
          <div className="mb-1 flex flex-col gap-3">
            <Label htmlFor={inputId} className="cursor-text select-text">
              Type "{CONFIRM_VALUE}" to confirm.
            </Label>
            <Input
              id={inputId}
              type="text"
              autoComplete="off"
              placeholder="Type in here..."
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') void submit();
              }}
            />
          </div>
        </Dialog.Body>
        <Dialog.Footer>
          <Button
            type="quaternary"
            theme="borderless"
            htmlType="button"
            onClick={() => setOpen(false)}
            disabled={isPending}>
            Cancel
          </Button>
          <Button
            type="danger"
            theme="solid"
            htmlType="button"
            disabled={!canSubmit}
            loading={isPending}
            onClick={() => void submit()}>
            {confirmLabel}
          </Button>
        </Dialog.Footer>
      </Dialog.Content>
    </Dialog>
  );
}

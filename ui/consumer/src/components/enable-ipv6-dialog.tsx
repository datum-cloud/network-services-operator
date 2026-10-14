import { useEnableIPv6 } from '../lib/api';
import type { Network } from '../schema';
import { Button } from '@datum-cloud/datum-ui/button';
import { Dialog } from '@datum-cloud/datum-ui/dialog';
import { toast } from '@datum-cloud/datum-ui/toast';
import { useState } from 'react';

export function EnableIPv6Dialog({
  projectId,
  network,
  trigger,
  open: controlledOpen,
  onOpenChange,
}: {
  projectId: string | undefined;
  network: Network | undefined;
  trigger?: React.ReactNode;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
  const mutation = useEnableIPv6(projectId);

  const open = controlledOpen ?? uncontrolledOpen;
  const setOpen = (next: boolean) => {
    setUncontrolledOpen(next);
    onOpenChange?.(next);
  };

  const handleConfirm = () => {
    if (!network) return;
    mutation.mutate(network, {
      onSuccess: () => {
        toast.success(`IPv6 enabled on ${network.name}`);
        setOpen(false);
      },
      onError: () => {
        toast.error(`Couldn't enable IPv6 on ${network.name}`);
      },
    });
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !mutation.isPending && setOpen(next)}>
      {trigger && <Dialog.Trigger asChild>{trigger}</Dialog.Trigger>}
      <Dialog.Content>
        <Dialog.Header
          title="Enable IPv6 on this network"
          description={`Adds IPv6 to ${network?.name ?? 'this network'}'s address families. The platform addresses workloads over IPv6, so this is required before anything on this network can be given an address.`}
          onClose={mutation.isPending ? undefined : () => setOpen(false)}
        />
        <Dialog.Footer>
          <Button
            type="quaternary"
            theme="borderless"
            htmlType="button"
            onClick={() => setOpen(false)}
            disabled={mutation.isPending}>
            Cancel
          </Button>
          <Button
            type="primary"
            theme="solid"
            htmlType="button"
            onClick={handleConfirm}
            loading={mutation.isPending}>
            Enable IPv6
          </Button>
        </Dialog.Footer>
      </Dialog.Content>
    </Dialog>
  );
}

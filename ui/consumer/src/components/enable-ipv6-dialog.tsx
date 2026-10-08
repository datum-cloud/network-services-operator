import { useEnableIPv6 } from '../lib/api';
import type { Network } from '../schema';
import { Button } from '@datum-cloud/datum-ui/button';
import { Dialog } from '@datum-cloud/datum-ui/dialog';
import { toast } from '@datum-cloud/datum-ui/toast';
import { useState } from 'react';

export function EnableIPv6Dialog({
  projectId,
  network,
}: {
  projectId: string | undefined;
  network: Network;
}) {
  const [open, setOpen] = useState(false);
  const mutation = useEnableIPv6(projectId);

  const handleConfirm = () => {
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
    <Dialog open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <Button type="secondary" size="xs">
          Enable IPv6
        </Button>
      </Dialog.Trigger>
      <Dialog.Content>
        <Dialog.Header
          title="Enable IPv6 on this network"
          description={`Adds IPv6 to ${network.name}'s address families. The platform addresses workloads over IPv6, so this is required before anything on this network can be given an address.`}
        />
        <Dialog.Footer>
          <Button type="secondary" onClick={() => setOpen(false)} disabled={mutation.isPending}>
            Cancel
          </Button>
          <Button type="primary" onClick={handleConfirm} loading={mutation.isPending}>
            Enable IPv6
          </Button>
        </Dialog.Footer>
      </Dialog.Content>
    </Dialog>
  );
}

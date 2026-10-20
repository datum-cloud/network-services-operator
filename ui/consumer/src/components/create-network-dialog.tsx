import { useCreateNetwork } from '../lib/api';
import { Button } from '@datum-cloud/datum-ui/button';
import { Form } from '@datum-cloud/datum-ui/form';
import { ConformAdapter } from '@datum-cloud/datum-ui/form/adapters/conform';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { toast } from '@datum-cloud/datum-ui/toast';
import { PlusIcon } from 'lucide-react';
import { useState } from 'react';
import { z } from 'zod';

const DEFAULT_MTU = 1440;

const NAME_MESSAGE =
  'Lowercase letters, numbers, and hyphens only. Must start and end with a letter or number.';

const schema = z.object({
  name: z
    .string({ error: 'Name is required.' })
    .max(63, 'At most 63 characters.')
    .regex(/^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/, NAME_MESSAGE),
  mtu: z
    .number({ error: 'Enter a whole number.' })
    .int('Enter a whole number.')
    .min(1300, 'Must be between 1300 and 8856.')
    .max(8856, 'Must be between 1300 and 8856.')
    .optional(),
});

export function CreateNetworkDialog({
  projectId,
  open: controlledOpen,
  onOpenChange,
  showTrigger = true,
}: {
  projectId: string | undefined;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  showTrigger?: boolean;
}) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
  const mutation = useCreateNetwork(projectId);

  const open = controlledOpen ?? uncontrolledOpen;
  const setOpen = (next: boolean) => {
    setUncontrolledOpen(next);
    onOpenChange?.(next);
  };

  const handleSubmit = (data: z.infer<typeof schema>) => {
    mutation.mutate(
      {
        name: data.name,
        ipFamilies: ['IPv6'],
        mtu: data.mtu,
      },
      {
        onSuccess: () => {
          toast.success(`Created network ${data.name}`);
          setOpen(false);
        },
        onError: (error) => {
          toast.error(error.message);
        },
      }
    );
  };

  return (
    <>
      {showTrigger && (
        <Button
          type="primary"
          theme="solid"
          size="small"
          className="w-full sm:w-auto"
          onClick={() => setOpen(true)}>
          <Icon icon={PlusIcon} className="size-4" />
          Create network
        </Button>
      )}
      <ConformAdapter>
        <Form.Dialog
          open={open}
          onOpenChange={setOpen}
          title="Create a network"
          schema={schema}
          onSubmit={handleSubmit}
          loading={mutation.isPending}
          submitText="Create"
          submitTextLoading="Creating..."
          className="w-full focus:ring-0 focus:outline-none sm:max-w-xl">
          <div className="divide-border space-y-0 divide-y *:px-5 *:py-5 [&>*:first-child]:pt-0 [&>*:last-child]:pb-0">
            <Form.Field name="name" label="Name" required>
              <Form.Input autoFocus placeholder="e.g. my-network" />
            </Form.Field>
            <Form.Field
              name="mtu"
              label="MTU"
              tooltip="Maximum transmission unit. Traffic between regions is encapsulated, so a larger MTU can stall connections on some paths."
              description={`Between 1300 and 8856. Defaults to ${DEFAULT_MTU}.`}>
              <Form.Input type="number" inputMode="numeric" placeholder={String(DEFAULT_MTU)} />
            </Form.Field>
          </div>
        </Form.Dialog>
      </ConformAdapter>
    </>
  );
}

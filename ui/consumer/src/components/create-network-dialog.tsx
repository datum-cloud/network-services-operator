import { useCreateNetwork } from '../lib/api';
import {
  NetworkMtuField,
  NetworkNameField,
  networkFormSchema,
  toCreateNetworkInput,
  type NetworkFormValues,
} from './network-form';
import { Button } from '@datum-cloud/datum-ui/button';
import { Form } from '@datum-cloud/datum-ui/form';
import { ConformAdapter } from '@datum-cloud/datum-ui/form/adapters/conform';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { toast } from '@datum-cloud/datum-ui/toast';
import { PlusIcon } from 'lucide-react';
import { useState } from 'react';

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

  const handleSubmit = (data: NetworkFormValues) => {
    mutation.mutate(toCreateNetworkInput(data), {
      onSuccess: () => {
        toast.success(`Created network ${data.name}`);
        setOpen(false);
      },
      onError: (error) => {
        toast.error(error.message);
      },
    });
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
          schema={networkFormSchema}
          onSubmit={handleSubmit}
          loading={mutation.isPending}
          submitText="Create"
          submitTextLoading="Creating..."
          className="w-full focus:ring-0 focus:outline-none sm:max-w-xl">
          <div className="divide-border space-y-0 divide-y *:px-5 *:py-5 [&>*:first-child]:pt-0 [&>*:last-child]:pb-0">
            <NetworkNameField autoFocus />
            <NetworkMtuField />
          </div>
        </Form.Dialog>
      </ConformAdapter>
    </>
  );
}

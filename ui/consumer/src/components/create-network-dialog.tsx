import { useCreateNetwork } from '../lib/api';
import { Button } from '@datum-cloud/datum-ui/button';
import { Checkbox } from '@datum-cloud/datum-ui/checkbox';
import { Dialog } from '@datum-cloud/datum-ui/dialog';
import { Input } from '@datum-cloud/datum-ui/input';
import { InputNumber } from '@datum-cloud/datum-ui/input-number';
import { toast } from '@datum-cloud/datum-ui/toast';
import { PlusIcon } from 'lucide-react';
import { useState } from 'react';

const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
const DEFAULT_MTU = 1460;

export function CreateNetworkDialog({ projectId }: { projectId: string | undefined }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [includeIPv4, setIncludeIPv4] = useState(false);
  const [mtu, setMtu] = useState<number | undefined>(undefined);
  const [nameError, setNameError] = useState<string | undefined>(undefined);
  const mutation = useCreateNetwork(projectId);

  const reset = () => {
    setName('');
    setIncludeIPv4(false);
    setMtu(undefined);
    setNameError(undefined);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();

    if (!NAME_PATTERN.test(name)) {
      setNameError('Lowercase letters, numbers, and hyphens only. Must start and end with a letter or number.');
      return;
    }
    setNameError(undefined);

    mutation.mutate(
      {
        name,
        ipFamilies: includeIPv4 ? ['IPv4', 'IPv6'] : ['IPv6'],
        mtu,
      },
      {
        onSuccess: () => {
          toast.success(`Created network ${name}`);
          reset();
          setOpen(false);
        },
        onError: (error) => {
          toast.error(error.message);
        },
      }
    );
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}>
      <Dialog.Trigger asChild>
        <Button type="primary" icon={<PlusIcon size={16} />}>
          Create network
        </Button>
      </Dialog.Trigger>
      <Dialog.Content>
        <Dialog.Header
          title="Create a network"
          description="The platform addresses workloads over IPv6, so every network carries it. Add IPv4 alongside it for dual-stack."
        />
        <form onSubmit={handleSubmit}>
          <Dialog.Body className="flex flex-col gap-4 px-5">
            <label className="flex flex-col gap-1.5">
              <span className="text-sm font-medium">Name</span>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="my-network"
                autoFocus
              />
              {nameError && <span className="text-sm text-red-500">{nameError}</span>}
            </label>

            <label className="flex items-center gap-2">
              <Checkbox checked={includeIPv4} onCheckedChange={(v) => setIncludeIPv4(v === true)} />
              <span className="text-sm">Also carry IPv4 (dual-stack)</span>
            </label>

            <label className="flex flex-col gap-1.5">
              <span className="text-sm font-medium">MTU</span>
              <InputNumber
                value={mtu}
                onValueChange={setMtu}
                placeholder={String(DEFAULT_MTU)}
                min={1300}
                max={8856}
              />
              <span className="text-muted-foreground text-xs">
                Between 1300 and 8856. Defaults to {DEFAULT_MTU}.
              </span>
            </label>
          </Dialog.Body>
          <Dialog.Footer>
            <Button type="secondary" htmlType="button" onClick={() => setOpen(false)} disabled={mutation.isPending}>
              Cancel
            </Button>
            <Button type="primary" htmlType="submit" loading={mutation.isPending}>
              Create network
            </Button>
          </Dialog.Footer>
        </form>
      </Dialog.Content>
    </Dialog>
  );
}

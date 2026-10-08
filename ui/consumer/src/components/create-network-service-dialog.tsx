import { useCreateNetworkService, useNetworkInterfaces } from '../lib/api';
import { Button } from '@datum-cloud/datum-ui/button';
import { Dialog } from '@datum-cloud/datum-ui/dialog';
import { Input } from '@datum-cloud/datum-ui/input';
import { InputNumber } from '@datum-cloud/datum-ui/input-number';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@datum-cloud/datum-ui/select';
import { toast } from '@datum-cloud/datum-ui/toast';
import { PlusIcon, XIcon } from 'lucide-react';
import { useState } from 'react';

const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

const WORKLOAD_NAME_LABEL = 'compute.datumapis.com/workload-name';

interface PortRow {
  name: string;
  port: number | undefined;
}

function emptyPortRow(): PortRow {
  return { name: '', port: undefined };
}

export function CreateNetworkServiceDialog({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [workloadName, setWorkloadName] = useState('');
  const [ports, setPorts] = useState<PortRow[]>([emptyPortRow()]);
  const [error, setError] = useState<string | undefined>(undefined);
  const mutation = useCreateNetworkService(projectId);
  const { data: interfaces } = useNetworkInterfaces(projectId, networkName);

  const workloadOptions = [
    ...new Set((interfaces ?? []).map((iface) => iface.workloadName).filter((w): w is string => !!w)),
  ].sort();

  const reset = () => {
    setName('');
    setWorkloadName('');
    setPorts([emptyPortRow()]);
    setError(undefined);
  };

  const updatePort = (index: number, field: keyof PortRow, value: string | number | undefined) => {
    setPorts((rows) => rows.map((row, i) => (i === index ? { ...row, [field]: value } : row)));
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();

    if (!NAME_PATTERN.test(name)) {
      setError('Name must be lowercase letters, numbers, and hyphens, starting and ending with a letter or number.');
      return;
    }

    if (!workloadName) {
      setError('Select the workload this service should route to.');
      return;
    }

    const filledPorts = ports.filter((p) => p.name.trim() && p.port);
    if (filledPorts.length === 0) {
      setError('Add at least one port this service answers on.');
      return;
    }
    if (!filledPorts.every((p) => NAME_PATTERN.test(p.name.trim()))) {
      setError('Port names must be lowercase letters, numbers, and hyphens.');
      return;
    }
    const portNames = filledPorts.map((p) => p.name.trim());
    const portNumbers = filledPorts.map((p) => p.port);
    if (new Set(portNames).size !== portNames.length) {
      setError('Port names must be unique.');
      return;
    }
    if (new Set(portNumbers).size !== portNumbers.length) {
      setError('Port numbers must be unique.');
      return;
    }

    setError(undefined);

    mutation.mutate(
      {
        name,
        matchLabels: { [WORKLOAD_NAME_LABEL]: workloadName },
        ports: filledPorts.map((p) => ({ name: p.name.trim(), port: p.port as number })),
      },
      {
        onSuccess: () => {
          toast.success(`Created service ${name}`);
          reset();
          setOpen(false);
        },
        onError: (err) => {
          toast.error(err.message);
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
          Create service
        </Button>
      </Dialog.Trigger>
      <Dialog.Content>
        <Dialog.Header
          title="Create a service"
          description="A service selects network interfaces by label and load-balances traffic across every location they run in."
        />
        <form onSubmit={handleSubmit}>
          <Dialog.Body className="flex flex-col gap-5 px-5">
            <label className="flex flex-col gap-1.5">
              <span className="text-sm font-medium">Name</span>
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="storefront"
                autoFocus
              />
            </label>

            <label className="flex flex-col gap-1.5">
              <span className="text-sm font-medium">Workload</span>
              <span className="text-muted-foreground text-xs">
                The service routes to every interface belonging to this workload.
              </span>
              <Select value={workloadName} onValueChange={setWorkloadName}>
                <SelectTrigger>
                  <SelectValue placeholder="Select a workload" />
                </SelectTrigger>
                <SelectContent>
                  {workloadOptions.map((w) => (
                    <SelectItem key={w} value={w}>
                      {w}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </label>

            <div className="flex flex-col gap-1.5">
              <span className="text-sm font-medium">Ports</span>
              <span className="text-muted-foreground text-xs">
                The ports members answer on. A backend referencing this service names one by port
                name.
              </span>
              <div className="flex flex-col gap-2">
                {ports.map((row, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <Input
                      value={row.name}
                      onChange={(e) => updatePort(i, 'name', e.target.value)}
                      placeholder="http"
                      className="flex-1"
                    />
                    <InputNumber
                      value={row.port}
                      onValueChange={(v) => updatePort(i, 'port', v)}
                      placeholder="8080"
                      min={1}
                      max={65535}
                      className="flex-1"
                    />
                    <Button
                      type="secondary"
                      theme="outline"
                      size="icon"
                      htmlType="button"
                      aria-label="Remove port"
                      onClick={() => setPorts((rows) => rows.filter((_, idx) => idx !== i))}
                      disabled={ports.length === 1}
                      icon={<XIcon size={14} />}
                    />
                  </div>
                ))}
              </div>
              <Button
                type="secondary"
                theme="outline"
                size="xs"
                htmlType="button"
                icon={<PlusIcon size={14} />}
                onClick={() => setPorts((rows) => [...rows, emptyPortRow()])}
                className="self-start">
                Add port
              </Button>
            </div>

            {error && <span className="text-sm text-red-500">{error}</span>}
          </Dialog.Body>
          <Dialog.Footer>
            <Button type="secondary" htmlType="button" onClick={() => setOpen(false)} disabled={mutation.isPending}>
              Cancel
            </Button>
            <Button type="primary" htmlType="submit" loading={mutation.isPending}>
              Create service
            </Button>
          </Dialog.Footer>
        </form>
      </Dialog.Content>
    </Dialog>
  );
}

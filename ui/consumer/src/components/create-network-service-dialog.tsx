import { useCreateNetworkService, useNetworkInterfaces } from '../lib/api';
import { useFormMetadata } from '@conform-to/react';
import { Button } from '@datum-cloud/datum-ui/button';
import { Form, useFormContext } from '@datum-cloud/datum-ui/form';
import { ConformAdapter } from '@datum-cloud/datum-ui/form/adapters/conform';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { toast } from '@datum-cloud/datum-ui/toast';
import { PlusIcon, Trash2Icon } from 'lucide-react';
import { useState } from 'react';
import { z } from 'zod';

const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
const NAME_MESSAGE = 'Lowercase letters, numbers, and hyphens, starting and ending with a letter or number.';

const WORKLOAD_NAME_LABEL = 'compute.datumapis.com/workload-name';

const PROTOCOLS = ['TCP', 'UDP'] as const;

const portSchema = z.object({
  name: z.string({ error: 'Name the port.' }).regex(NAME_PATTERN, NAME_MESSAGE),
  port: z
    .number({ error: 'Enter a port number.' })
    .int('Enter a whole number.')
    .min(1, 'Must be between 1 and 65535.')
    .max(65535, 'Must be between 1 and 65535.'),
  protocol: z.enum(PROTOCOLS),
});

const schema = z.object({
  name: z.string({ error: 'Name is required.' }).regex(NAME_PATTERN, NAME_MESSAGE),
  workloadName: z.string({ error: 'Select the workload this service should route to.' }),
  ports: z
    .array(portSchema)
    .min(1, 'Add at least one port this service answers on.')
    .superRefine((ports, ctx) => {
      const seenNames = new Set<string>();
      const seenPorts = new Set<string>();
      for (const p of ports) {
        const portKey = `${p.protocol}/${p.port}`;
        if (seenNames.has(p.name)) {
          ctx.addIssue({ code: 'custom', message: `Port name ${p.name} is already used.` });
        }
        if (seenPorts.has(portKey)) {
          ctx.addIssue({ code: 'custom', message: `${p.protocol} port ${p.port} is already used.` });
        }
        seenNames.add(p.name);
        seenPorts.add(portKey);
      }
    }),
});

type CreateServiceValues = z.infer<typeof schema>;

const NEW_PORT = { name: '', port: undefined, protocol: 'TCP' };

const DEFAULT_VALUES = { ports: [NEW_PORT] } as unknown as Partial<CreateServiceValues>;

function PortsErrors() {
  const { displayTouchedFields } = useFormContext();
  const errors = useFormMetadata().getFieldset().ports?.errors ?? [];
  if (!displayTouchedFields.includes('ports') || errors.length === 0) return null;
  return (
    <ul className="text-destructive space-y-1 text-xs font-medium" role="alert">
      {errors.map((error) => (
        <li key={error}>{error}</li>
      ))}
    </ul>
  );
}

function PortsField() {
  return (
    <Form.FieldArray name="ports">
      {({ fields, append, remove }) => (
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <span className="text-foreground/80 text-xs font-semibold">
              Ports <span className="text-destructive/80">*</span>
            </span>
            <span className="text-muted-foreground text-xs">
              The ports members answer on. A backend referencing this service names one by port name. A UDP port needs a TCP port on the service for its health check.
            </span>
          </div>
          <div className="flex flex-col gap-2">
            {fields.map((field, index) => (
              <div key={field.key} className="flex items-start gap-2">
                <Form.Field name={`ports.${index}.name`} className="flex-1">
                  <Form.Input placeholder="e.g. http" aria-label={`Port ${index + 1} name`} />
                </Form.Field>
                <Form.Field name={`ports.${index}.port`} className="flex-1">
                  <Form.Input
                    type="number"
                    inputMode="numeric"
                    placeholder="e.g. 8080"
                    aria-label={`Port ${index + 1} number`}
                  />
                </Form.Field>
                <Form.Field name={`ports.${index}.protocol`} className="w-24">
                  <Form.Select>
                    {PROTOCOLS.map((protocol) => (
                      <Form.SelectItem key={protocol} value={protocol}>
                        {protocol}
                      </Form.SelectItem>
                    ))}
                  </Form.Select>
                </Form.Field>
                <Button
                  htmlType="button"
                  type="quaternary"
                  theme="borderless"
                  size="icon"
                  aria-label={`Remove port ${index + 1}`}
                  disabled={fields.length === 1}
                  onClick={() => remove(index)}>
                  <Icon icon={Trash2Icon} className="size-4" />
                </Button>
              </div>
            ))}
          </div>
          <Button
            htmlType="button"
            type="quaternary"
            theme="outline"
            size="small"
            className="w-fit"
            onClick={() => append({ ...NEW_PORT })}>
            <Icon icon={PlusIcon} className="size-4" />
            Add port
          </Button>
          <PortsErrors />
        </div>
      )}
    </Form.FieldArray>
  );
}

export function CreateNetworkServiceDialog({
  projectId,
  networkName,
}: {
  projectId: string | undefined;
  networkName: string | undefined;
}) {
  const [open, setOpen] = useState(false);
  const mutation = useCreateNetworkService(projectId);
  const { data: interfaces } = useNetworkInterfaces(projectId, networkName);

  const workloadOptions = [
    ...new Set((interfaces ?? []).map((iface) => iface.workloadName).filter((w): w is string => !!w)),
  ].sort();

  const handleSubmit = (data: CreateServiceValues) => {
    mutation.mutate(
      {
        name: data.name,
        matchLabels: { [WORKLOAD_NAME_LABEL]: data.workloadName },
        ports: data.ports,
      },
      {
        onSuccess: () => {
          toast.success(`Created service ${data.name}`);
          setOpen(false);
        },
        onError: (err) => {
          toast.error(err.message);
        },
      }
    );
  };

  return (
    <>
      <Button
        type="primary"
        theme="solid"
        size="small"
        className="w-full sm:w-auto"
        onClick={() => setOpen(true)}>
        <Icon icon={PlusIcon} className="size-4" />
        Create service
      </Button>
      <ConformAdapter>
        <Form.Dialog
          open={open}
          onOpenChange={setOpen}
          title="Create a service"
          description="A service selects network interfaces by label and load-balances traffic across every location they run in."
          schema={schema}
          defaultValues={DEFAULT_VALUES}
          onSubmit={handleSubmit}
          loading={mutation.isPending}
          submitText="Create"
          submitTextLoading="Creating..."
          className="w-full focus:ring-0 focus:outline-none sm:max-w-xl">
          <div className="divide-border space-y-0 divide-y *:px-5 *:py-5 [&>*:first-child]:pt-0 [&>*:last-child]:pb-0">
            <Form.Field name="name" label="Name" required>
              <Form.Input autoFocus placeholder="e.g. storefront" />
            </Form.Field>
            <Form.Field
              name="workloadName"
              label="Workload"
              required
              description="The service routes to every interface belonging to this workload.">
              <Form.Select placeholder="Select a workload">
                {workloadOptions.map((w) => (
                  <Form.SelectItem key={w} value={w}>
                    {w}
                  </Form.SelectItem>
                ))}
              </Form.Select>
            </Form.Field>
            <PortsField />
          </div>
        </Form.Dialog>
      </ConformAdapter>
    </>
  );
}

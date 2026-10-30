import {
  ApiError,
  createNetworkLoadBalancer,
  createNetworkLoadBalancerRoute,
  PLUGIN_ID,
  useNetworks,
  useNetworkServices,
} from '../lib/api';
import type { NetworkService } from '../schema';
import {
  backendPortOptions,
  healthPortOptions,
  nlbCreateSchema,
  portProtocol,
  SERVING_PORT,
  toNlbCreateErrors,
  toNlbCreateRequests,
  type NlbCreateErrors,
  type NlbCreateValues,
} from './nlb-create-form';
import type { NlbCreatePreset } from './nlb-empty-state';
import { Form, useWatch } from '@datum-cloud/datum-ui/form';
import { ConformAdapter } from '@datum-cloud/datum-ui/form/adapters/conform';
import { toast } from '@datum-cloud/datum-ui/toast';
import { useQueryClient } from '@tanstack/react-query';
import { useRef, useState } from 'react';

const NO_ERRORS: NlbCreateErrors = { fields: {} };

function FieldApiError({ message }: { message: string | undefined }) {
  if (!message) return null;
  return (
    <p role="alert" className="text-destructive mt-1 text-xs font-medium">
      {message}
    </p>
  );
}

function StepHeading({ step, title, hint }: { step: number; title: string; hint: string }) {
  return (
    <p className="flex items-baseline gap-2">
      <span className="bg-foreground text-background flex size-5 shrink-0 items-center justify-center rounded-full text-xs font-semibold">
        {step}
      </span>
      <span className="text-sm font-semibold">{title}</span>
      <span className="text-muted-foreground text-xs">{hint}</span>
    </p>
  );
}

function BackendPortField({ services, error }: { services: NetworkService[]; error?: string }) {
  const backend = useWatch('backend') as string | undefined;
  const protocol = useWatch('protocol') as 'TCP' | 'UDP' | undefined;
  const service = services.find((s) => s.name === backend);
  const options = backendPortOptions(service, protocol);

  return (
    <div>
      <Form.Field
        name="backendPort"
        label="Backend port"
        required
        description={
          service && options.length === 0
            ? `${service.name} has no ${protocol} port. Add one to the service, or pick another backend.`
            : 'The named port on the backend that receives each connection.'
        }>
        <Form.Select placeholder="Select a port" disabled={!service}>
          {options.map((p) => (
            <Form.SelectItem key={p.name} value={p.name}>
              {p.name} · {p.port}/{portProtocol(p)}
            </Form.SelectItem>
          ))}
        </Form.Select>
      </Form.Field>
      <FieldApiError message={error} />
    </div>
  );
}

function HealthPortField({ services, error }: { services: NetworkService[]; error?: string }) {
  const backend = useWatch('backend') as string | undefined;
  const protocol = useWatch('protocol') as 'TCP' | 'UDP' | undefined;
  const service = services.find((s) => s.name === backend);
  const options = healthPortOptions(service);

  return (
    <div className="flex flex-col gap-2">
      <Form.Field
        name="healthPort"
        label="TCP probe port"
        required={protocol === 'UDP'}
        description={
          protocol === 'UDP'
            ? 'UDP can’t be probed directly, so a UDP listener needs a TCP port on the backend to health check.'
            : 'Datum opens a TCP connection to this port to decide whether a backend can take connections.'
        }>
        <Form.Select
          placeholder={protocol === 'UDP' ? 'Select a TCP port' : 'The serving port'}
          disabled={!service}>
          {protocol !== 'UDP' && (
            <Form.SelectItem value={SERVING_PORT}>The serving port</Form.SelectItem>
          )}
          {options.map((p) => (
            <Form.SelectItem key={p.name} value={p.name}>
              {p.name} · {p.port}/TCP
            </Form.SelectItem>
          ))}
        </Form.Select>
      </Form.Field>
      <FieldApiError message={error} />
    </div>
  );
}

/**
 * Checks the form against the backend it names. The schema can't, since it
 * doesn't know the project's services, and the API only reports a mismatch in
 * the route's status after saving.
 */
function checkBackend(
  values: NlbCreateValues,
  services: NetworkService[]
): NlbCreateErrors['fields'] {
  const service = services.find((s) => s.name === values.backend);
  if (!service) return { backend: 'Select a backend.' };
  if (!backendPortOptions(service, values.protocol).some((p) => p.name === values.backendPort)) {
    return { backendPort: `Select one of ${service.name}'s ${values.protocol} ports.` };
  }
  const healthPort = values.healthPort;
  if (
    healthPort &&
    healthPort !== SERVING_PORT &&
    !healthPortOptions(service).some((p) => p.name === healthPort)
  ) {
    return { healthPort: `Select one of ${service.name}'s TCP ports.` };
  }
  return {};
}

// Mockup N2 adapted to creating: the NLB and its first listener, address and
// route in one go. The NLB is created first; if its route then fails, the
// dialog stays open with the listener locked so only the backend is retried.
export function CreateNlbDialog({
  projectId,
  open,
  onOpenChange,
  preset,
}: {
  projectId: string | undefined;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  preset?: NlbCreatePreset;
}) {
  const queryClient = useQueryClient();
  const { data: networks } = useNetworks(projectId);
  const { data: services = [] } = useNetworkServices(projectId);
  const [pending, setPending] = useState(false);
  const [errors, setErrors] = useState<NlbCreateErrors>(NO_ERRORS);
  // Set once the NLB exists but its route doesn't, so a retry only creates the route.
  const [created, setCreated] = useState<CreatedListener>();
  // Form.Dialog closes itself once onSubmit returns, so a failed submit sets
  // this to swallow that one close and keep what the user entered.
  const keepOpen = useRef(false);

  const handleOpenChange = (next: boolean) => {
    if (!next && keepOpen.current) {
      keepOpen.current = false;
      return;
    }
    if (!next) {
      setErrors(NO_ERRORS);
      setCreated(undefined);
    }
    onOpenChange(next);
  };

  const fail = (next: NlbCreateErrors) => {
    setErrors(next);
    keepOpen.current = true;
  };

  const handleSubmit = async (values: NlbCreateValues) => {
    const backendErrors = checkBackend(values, services);
    if (Object.keys(backendErrors).length > 0) return fail({ fields: backendErrors });

    const requests = toNlbCreateRequests(values);
    setPending(true);
    setErrors(NO_ERRORS);
    try {
      if (!created) {
        try {
          await createNetworkLoadBalancer(projectId as string, requests.nlb);
        } catch (err) {
          return fail(toNlbCreateErrors(asApiError(err), 'nlb'));
        }
        setCreated({
          name: values.name,
          network: values.network,
          protocol: values.protocol,
          port: values.port,
        });
        void queryClient.invalidateQueries({
          queryKey: [PLUGIN_ID, 'networkLoadBalancers', projectId],
        });
      }
      try {
        await createNetworkLoadBalancerRoute(projectId as string, requests.route);
      } catch (err) {
        return fail(toNlbCreateErrors(asApiError(err), 'route'));
      }
      toast.success(`Created load balancer ${values.name}`);
      setCreated(undefined);
    } finally {
      setPending(false);
    }
  };

  const networkNames = (networks ?? []).map((n) => n.name);
  const defaultValues: Partial<NlbCreateValues> = {
    protocol: preset?.protocol ?? 'TCP',
    port: preset?.port,
    network: networkNames.length === 1 ? networkNames[0] : undefined,
    healthPort: preset?.protocol === 'UDP' ? undefined : SERVING_PORT,
  };

  return (
    // Keyed so each open, and the networks arriving, start the form from fresh defaults.
    <ConformAdapter
      key={`${open}-${preset?.protocol}-${preset?.port}-${networks ? 'loaded' : 'loading'}`}>
      <Form.Dialog
        open={open}
        onOpenChange={handleOpenChange}
        title="Create load balancer"
        description="Accepts one protocol and port on its own public address, and forwards every connection to one backend."
        schema={nlbCreateSchema}
        defaultValues={defaultValues}
        onSubmit={handleSubmit}
        loading={pending}
        submitText={created ? 'Retry backend' : 'Create'}
        submitTextLoading="Creating..."
        className="w-full focus:ring-0 focus:outline-none sm:max-w-xl">
        <div className="divide-border space-y-0 divide-y *:px-5 *:py-5 [&>*:first-child]:pt-0 [&>*:last-child]:pb-0">
          {(errors.general || created) && (
            <div
              role="alert"
              className="bg-destructive/10 text-destructive rounded-md px-4 py-3 text-xs font-medium">
              {created && (
                <p>
                  Created load balancer {created.name}, but couldn’t attach its backend. Fix the
                  backend and retry.
                </p>
              )}
              {errors.general && <p>{errors.general}</p>}
            </div>
          )}

          {created ? (
            <CreatedListenerSummary listener={created} />
          ) : (
            <ListenerFields networkNames={networkNames} errors={errors} />
          )}

          <div className="flex flex-col gap-4">
            <StepHeading step={2} title="Send to" hint="one backend per listener for now" />
            <div>
              <Form.Field name="backend" label="Backend" required>
                <Form.Select placeholder="Select a backend">
                  {services.map((s) => (
                    <Form.SelectItem key={s.uid} value={s.name}>
                      {s.name}
                    </Form.SelectItem>
                  ))}
                </Form.Select>
              </Form.Field>
              <FieldApiError message={errors.fields.backend} />
            </div>
            <BackendPortField services={services} error={errors.fields.backendPort} />
          </div>

          <div className="flex flex-col gap-4">
            <StepHeading step={3} title="Health check" hint="when a backend can take connections" />
            <HealthPortField services={services} error={errors.fields.healthPort} />
          </div>
        </div>
      </Form.Dialog>
    </ConformAdapter>
  );
}

interface CreatedListener {
  name: string;
  network: string;
  protocol: 'TCP' | 'UDP';
  port: number;
}

function ListenerFields({
  networkNames,
  errors,
}: {
  networkNames: string[];
  errors: NlbCreateErrors;
}) {
  return (
    <>
      <div className="flex flex-col gap-4">
        <div>
          <Form.Field name="name" label="Name" required>
            <Form.Input autoFocus placeholder="e.g. edge-nlb" />
          </Form.Field>
          <FieldApiError message={errors.fields.name} />
        </div>
        <div>
          <Form.Field
            name="network"
            label="Network"
            required
            description="The network the backends run on. It can’t be changed later.">
            <Form.Select placeholder="Select a network">
              {networkNames.map((name) => (
                <Form.SelectItem key={name} value={name}>
                  {name}
                </Form.SelectItem>
              ))}
            </Form.Select>
          </Form.Field>
          <FieldApiError message={errors.fields.network} />
        </div>
      </div>

      <div className="flex flex-col gap-4">
        <StepHeading step={1} title="Listen" hint="which traffic this listener accepts" />
        <div className="flex flex-col gap-4 sm:flex-row">
          <div className="sm:w-1/2">
            <Form.Field name="protocol" label="Protocol" required>
              <Form.Select>
                <Form.SelectItem value="TCP">TCP</Form.SelectItem>
                <Form.SelectItem value="UDP">UDP</Form.SelectItem>
                <Form.SelectItem value="TLS" disabled>
                  TLS · coming soon
                </Form.SelectItem>
              </Form.Select>
            </Form.Field>
            <FieldApiError message={errors.fields.protocol} />
          </div>
          <div className="sm:w-1/2">
            <Form.Field name="port" label="Port" required>
              <Form.Input type="number" inputMode="numeric" placeholder="e.g. 5432" />
            </Form.Field>
            <FieldApiError message={errors.fields.port} />
          </div>
        </div>
        <p className="text-muted-foreground text-xs">
          New IPv6 address · follows backends. One public address per city where the backend runs.
          IPv4 coming soon.
        </p>
      </div>
    </>
  );
}

/**
 * Stands in for the listener fields once the NLB exists. Disabled fields drop
 * out of the submitted form, so the values ride along as hidden inputs.
 */
function CreatedListenerSummary({ listener }: { listener: CreatedListener }) {
  return (
    <div className="flex flex-col gap-1 text-sm">
      <p className="font-medium">{listener.name}</p>
      <p className="text-muted-foreground text-xs">
        {listener.protocol} {listener.port} on network {listener.network} · created
      </p>
      <input type="hidden" name="name" value={listener.name} />
      <input type="hidden" name="network" value={listener.network} />
      <input type="hidden" name="protocol" value={listener.protocol} />
      <input type="hidden" name="port" value={String(listener.port)} />
    </div>
  );
}

function asApiError(err: unknown): ApiError {
  return err instanceof ApiError
    ? err
    : new ApiError(0, err instanceof Error ? err.message : 'Something went wrong.');
}

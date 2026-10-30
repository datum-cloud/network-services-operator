import {
  asApiError,
  BackendPortField,
  checkBackend,
  FieldApiError,
  HealthPortField,
  StepHeading,
} from './create-nlb-dialog';
import {
  nlbListenerName,
  nlbListenerSchema,
  SERVING_PORT,
  toAddListenerRequests,
  toNlbCreateErrors,
  type NlbCreateErrors,
  type NlbListenerValues,
} from './nlb-create-form';
import {
  createNetworkLoadBalancerRoute,
  patchNetworkLoadBalancer,
  PLUGIN_ID,
  useNetworkServices,
} from '../lib/api';
import type { NetworkLoadBalancer } from '../schema';
import { Form } from '@datum-cloud/datum-ui/form';
import { ConformAdapter } from '@datum-cloud/datum-ui/form/adapters/conform';
import { toast } from '@datum-cloud/datum-ui/toast';
import { useQueryClient } from '@tanstack/react-query';
import { useRef, useState } from 'react';

const NO_ERRORS: NlbCreateErrors = { fields: {} };
const MAX_LISTENERS = 16;

/**
 * Checks the new listener against the NLB's others. Listeners on separate
 * addresses could share a protocol and port as far as the API cares, but two
 * TCP 443s on one load balancer are almost always a mistake.
 */
export function checkListener(
  values: Pick<NlbListenerValues, 'protocol' | 'port'>,
  nlb: NetworkLoadBalancer
): NlbCreateErrors {
  if (nlb.listeners.length >= MAX_LISTENERS) {
    return { fields: {}, general: `A load balancer can have at most ${MAX_LISTENERS} listeners.` };
  }
  const name = nlbListenerName(values);
  if (
    nlb.listeners.some(
      (l) => l.name === name || (l.protocol === values.protocol && l.port === values.port)
    )
  ) {
    return {
      fields: {
        port: `This load balancer already has a ${values.protocol} ${values.port} listener.`,
      },
    };
  }
  return NO_ERRORS;
}

// Mockup N2: one more listener on the NLB, on its own new address, with its
// own route. Like create, a failed route keeps the dialog open to retry just that.
export function AddListenerDialog({
  projectId,
  nlb,
  open,
  onOpenChange,
}: {
  projectId: string | undefined;
  nlb: NetworkLoadBalancer;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const { data: services = [] } = useNetworkServices(projectId);
  const [pending, setPending] = useState(false);
  const [errors, setErrors] = useState<NlbCreateErrors>(NO_ERRORS);
  // Set once the listener is on the NLB but its route isn't, so a retry only creates the route.
  const [added, setAdded] = useState<Pick<NlbListenerValues, 'protocol' | 'port'>>();
  const keepOpen = useRef(false);

  const refresh = () => {
    void queryClient.invalidateQueries({
      queryKey: [PLUGIN_ID, 'networkLoadBalancer', projectId, nlb.name],
    });
    void queryClient.invalidateQueries({
      queryKey: [PLUGIN_ID, 'networkLoadBalancers', projectId],
    });
    void queryClient.invalidateQueries({
      queryKey: [PLUGIN_ID, 'networkLoadBalancerRoutes', projectId],
    });
  };

  const handleOpenChange = (next: boolean) => {
    if (!next && keepOpen.current) {
      keepOpen.current = false;
      return;
    }
    if (!next) {
      setErrors(NO_ERRORS);
      setAdded(undefined);
    }
    onOpenChange(next);
  };

  const fail = (next: NlbCreateErrors) => {
    setErrors(next);
    keepOpen.current = true;
  };

  const handleSubmit = async (values: NlbListenerValues) => {
    const listenerErrors = added ? NO_ERRORS : checkListener(values, nlb);
    if (listenerErrors.general || Object.keys(listenerErrors.fields).length > 0)
      return fail(listenerErrors);
    const backendErrors = checkBackend(values, services);
    if (Object.keys(backendErrors).length > 0) return fail({ fields: backendErrors });

    const requests = toAddListenerRequests(nlb, values);
    setPending(true);
    setErrors(NO_ERRORS);
    try {
      if (!added) {
        try {
          await patchNetworkLoadBalancer(projectId as string, nlb.name, requests.patch);
        } catch (err) {
          const error = asApiError(err);
          // The resourceVersion test failed: someone changed the NLB meanwhile.
          if (error.status === 409 || (error.status === 422 && error.causes.length === 0)) {
            refresh();
            return fail({
              fields: {},
              general: `${nlb.name} changed while you were adding this listener. Try again.`,
            });
          }
          return fail(toNlbCreateErrors(error, 'nlb'));
        }
        setAdded({ protocol: values.protocol, port: values.port });
        refresh();
      }
      try {
        await createNetworkLoadBalancerRoute(projectId as string, requests.route);
      } catch (err) {
        return fail(toNlbCreateErrors(asApiError(err), 'route'));
      }
      toast.success(`Added ${values.protocol} ${values.port} to ${nlb.name}`);
      setAdded(undefined);
      refresh();
    } finally {
      setPending(false);
    }
  };

  return (
    <ConformAdapter key={String(open)}>
      <Form.Dialog
        open={open}
        onOpenChange={handleOpenChange}
        title="Add listener"
        description="Accepts one protocol and port on its own public address, and forwards every connection to one backend."
        schema={nlbListenerSchema}
        defaultValues={{ protocol: 'TCP', healthPort: SERVING_PORT }}
        onSubmit={handleSubmit}
        loading={pending}
        submitText={added ? 'Retry backend' : 'Add listener'}
        submitTextLoading="Adding..."
        className="w-full focus:ring-0 focus:outline-none sm:max-w-xl"
        // See create-nlb-dialog.tsx: keeps the buttons in view when the body scrolls.
        formClassName="flex min-h-0 flex-col">
        <div className="divide-border space-y-0 divide-y *:px-5 *:py-5 [&>*:first-child]:pt-0 [&>*:last-child]:pb-0">
          {(errors.general || added) && (
            <div
              role="alert"
              className="bg-destructive/10 text-destructive rounded-md px-4 py-3 text-xs font-medium">
              {added && (
                <p>
                  Added {added.protocol} {added.port}, but couldn’t attach its backend. Fix the
                  backend and retry.
                </p>
              )}
              {errors.general && <p>{errors.general}</p>}
            </div>
          )}

          <div className="flex flex-col gap-4">
            <StepHeading step={1} title="Listen" hint="which traffic this listener accepts" />
            {added ? (
              <p className="text-sm">
                {added.protocol} {added.port} · added
                <input type="hidden" name="protocol" value={added.protocol} />
                <input type="hidden" name="port" value={String(added.port)} />
              </p>
            ) : (
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
                  <Form.Field
                    name="port"
                    label="Port"
                    required
                    description="1 to 65535. TCP and UDP can share a port.">
                    <Form.Input type="number" inputMode="numeric" placeholder="e.g. 7777" />
                  </Form.Field>
                  <FieldApiError message={errors.fields.port} />
                </div>
              </div>
            )}
            <p className="text-muted-foreground text-xs">
              New IPv6 address · follows backends. One public address per city where the backend
              runs. IPv4 coming soon.
            </p>
          </div>

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

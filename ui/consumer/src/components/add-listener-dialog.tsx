import {
  asApiError,
  BackendPortField,
  checkBackend,
  FieldApiError,
  HealthPortField,
  QuotaRefusedNote,
  StepHeading,
} from './create-nlb-dialog';
import { IpFamilyField, isChecked } from './ip-family-field';
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
  ipv4Locations,
  listenerHasIpv4,
  routeOnlyServes,
  toAddressFamiliesPatch,
  toEditListenerPatch,
  toRouteBackendPatch,
} from './nlb-listener-changes';
import type { ListenerRow } from './nlb-overview-model';
import {
  createNetworkLoadBalancerRoute,
  mergePatchNetworkLoadBalancerRoute,
  patchNetworkLoadBalancer,
  PLUGIN_ID,
  useNetworkServices,
} from '../lib/api';
import { releaseIpv4Message, useIpv4Availability, useRequestIpv4 } from '../lib/ipv4-quota';
import type { NetworkLoadBalancer, NetworkService } from '../schema';
import { Button } from '@datum-cloud/datum-ui/button';
import { Form, useField, useWatch } from '@datum-cloud/datum-ui/form';
import { ConformAdapter } from '@datum-cloud/datum-ui/form/adapters/conform';
import { toast } from '@datum-cloud/datum-ui/toast';
import { useQueryClient } from '@tanstack/react-query';
import { useRef, useState } from 'react';

const NO_ERRORS: NlbCreateErrors = { fields: {} };
const MAX_LISTENERS = 16;

/**
 * Checks a new or edited listener against the NLB's others. Listeners on
 * separate addresses could share a protocol and port as far as the API cares,
 * but two TCP 443s on one load balancer are almost always a mistake.
 */
export function checkListener(
  values: Pick<NlbListenerValues, 'protocol' | 'port'>,
  nlb: NetworkLoadBalancer,
  editing?: NetworkLoadBalancer['listeners'][number]
): NlbCreateErrors {
  if (!editing && nlb.listeners.length >= MAX_LISTENERS) {
    return { fields: {}, general: `A load balancer can have at most ${MAX_LISTENERS} listeners.` };
  }
  // An added listener is named after its protocol and port, so that name must be free too.
  const name = editing ? undefined : nlbListenerName(values);
  const others = nlb.listeners.filter((l) => l.name !== editing?.name);
  if (
    others.some(
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
// own route; or, given `editing`, changes to one already there. Like create, a
// failed route keeps the dialog open to retry just that.
export function AddListenerDialog({
  projectId,
  nlb,
  open,
  onOpenChange,
  editing,
}: {
  projectId: string | undefined;
  nlb: NetworkLoadBalancer;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The listener to edit; without it the dialog adds one. */
  editing?: ListenerRow;
}) {
  const queryClient = useQueryClient();
  const { data: services = [] } = useNetworkServices(projectId);
  const [pending, setPending] = useState(false);
  const [errors, setErrors] = useState<NlbCreateErrors>(NO_ERRORS);
  // Set once the listener is saved on the NLB but its route isn't, so a retry only does the route.
  const [added, setAdded] = useState<Pick<NlbListenerValues, 'protocol' | 'port'>>();
  const [confirmRelease, setConfirmRelease] = useState(false);
  const [ipv4ResetKey, setIpv4ResetKey] = useState(0);
  const releaseConfirmed = useRef(false);
  const keepOpen = useRef(false);
  const requestIpv4 = useRequestIpv4();
  const hasIpv4 = editing ? listenerHasIpv4(nlb, editing.listener) : false;

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
      setConfirmRelease(false);
      releaseConfirmed.current = false;
    }
    onOpenChange(next);
  };

  const fail = (next: NlbCreateErrors) => {
    releaseConfirmed.current = false;
    setErrors(next);
    keepOpen.current = true;
  };

  const changedElsewhere = (error: ReturnType<typeof asApiError>) =>
    // A failed test op: someone changed the NLB since the page read it.
    error.status === 409 || (error.status === 422 && error.causes.length === 0);

  const handleEdit = async (row: ListenerRow, values: NlbListenerValues) => {
    if (row.route && !routeOnlyServes(row.route, nlb.name, row.listener)) {
      return fail({
        fields: {},
        general: `Route ${row.route.name} also serves other listeners, so its backend can’t be changed here.`,
      });
    }
    const ipv4 = !!values.ipv4;
    if (!added && hasIpv4 && !ipv4 && !releaseConfirmed.current) {
      setConfirmRelease(true);
      keepOpen.current = true;
      return;
    }
    releaseConfirmed.current = false;
    setConfirmRelease(false);
    setPending(true);
    setErrors(NO_ERRORS);
    try {
      const patch = added
        ? []
        : [
            ...toEditListenerPatch(nlb, row.listener, values),
            ...toAddressFamiliesPatch(nlb, row.listener, ipv4),
          ];
      if (patch.length > 0) {
        try {
          await patchNetworkLoadBalancer(projectId as string, nlb.name, patch);
        } catch (err) {
          const error = asApiError(err);
          if (changedElsewhere(error)) {
            refresh();
            return fail({
              fields: {},
              general: `${nlb.name} changed while you were editing. Try again.`,
            });
          }
          const next = toNlbCreateErrors(error, 'nlb');
          return fail({ ...next, quotaRefused: next.quotaRefused && ipv4 && !hasIpv4 });
        }
        refresh();
      }
      const saved =
        added ?? (patch.length > 0 ? { protocol: values.protocol, port: values.port } : undefined);
      try {
        if (row.route) {
          await mergePatchNetworkLoadBalancerRoute(
            projectId as string,
            row.route.name,
            toRouteBackendPatch(row.route, nlb.name, row.listener, values)
          );
        } else {
          // A listener with no route gets the one it would have had from the start.
          const route = toAddListenerRequests(nlb, values).route;
          route.metadata.name = `${nlb.name}-${row.listener.name}`;
          (route.spec as { parentRefs: object[] }).parentRefs = [
            { name: nlb.name, listener: row.listener.name },
          ];
          await createNetworkLoadBalancerRoute(projectId as string, route);
        }
      } catch (err) {
        setAdded(saved);
        return fail(toNlbCreateErrors(asApiError(err), 'route'));
      }
      toast.success(`Saved ${values.protocol} ${values.port} on ${nlb.name}`);
      setAdded(undefined);
      refresh();
    } finally {
      setPending(false);
    }
  };

  const handleSubmit = async (values: NlbListenerValues) => {
    const listenerErrors = added ? NO_ERRORS : checkListener(values, nlb, editing?.listener);
    if (listenerErrors.general || Object.keys(listenerErrors.fields).length > 0)
      return fail(listenerErrors);
    const backendErrors = checkBackend(values, services);
    if (Object.keys(backendErrors).length > 0) return fail({ fields: backendErrors });
    if (editing) return handleEdit(editing, values);

    const requests = toAddListenerRequests(nlb, values);
    setPending(true);
    setErrors(NO_ERRORS);
    try {
      if (!added) {
        try {
          await patchNetworkLoadBalancer(projectId as string, nlb.name, requests.patch);
        } catch (err) {
          const error = asApiError(err);
          if (changedElsewhere(error)) {
            refresh();
            return fail({
              fields: {},
              general: `${nlb.name} changed while you were adding this listener. Try again.`,
            });
          }
          const next = toNlbCreateErrors(error, 'nlb');
          return fail({ ...next, quotaRefused: next.quotaRefused && !!values.ipv4 });
        }
        refresh();
      }
      try {
        await createNetworkLoadBalancerRoute(projectId as string, requests.route);
      } catch (err) {
        setAdded({ protocol: values.protocol, port: values.port });
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
    <ConformAdapter key={`${open}-${editing?.listener.name}`}>
      <Form.Dialog
        open={open}
        onOpenChange={handleOpenChange}
        title={
          editing ? `Edit ${editing.listener.protocol} ${editing.listener.port}` : 'Add listener'
        }
        description="Accepts one protocol and port on its own public address, and forwards every connection to one backend."
        schema={nlbListenerSchema}
        defaultValues={
          editing
            ? { ...editDefaults(editing), ipv4: hasIpv4 }
            : { protocol: 'TCP', healthPort: SERVING_PORT, ipv4: false }
        }
        onSubmit={handleSubmit}
        loading={pending}
        submitText={added ? 'Retry backend' : editing ? 'Save' : 'Add listener'}
        submitTextLoading={editing ? 'Saving...' : 'Adding...'}
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
                  {editing ? 'Saved' : 'Added'} {added.protocol} {added.port}, but couldn’t{' '}
                  {editing ? 'update' : 'attach'} its backend. Fix the backend and retry.
                </p>
              )}
              {errors.quotaRefused ? (
                <QuotaRefusedNote onRequest={() => requestIpv4(nlb.name)} />
              ) : (
                errors.general && <p>{errors.general}</p>
              )}
            </div>
          )}

          {confirmRelease && editing && (
            <ReleaseIpv4Confirm
              locations={ipv4Locations(nlb, editing.listener)}
              onConfirm={() => {
                releaseConfirmed.current = true;
              }}
              onKeep={() => {
                setConfirmRelease(false);
                setIpv4ResetKey((k) => k + 1);
              }}
            />
          )}

          <div className="flex flex-col gap-4">
            <StepHeading step={1} title="Listen" hint="which traffic this listener accepts" />
            {added ? (
              <p className="text-sm">
                {added.protocol} {added.port} · {editing ? 'saved' : 'added'}
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
            {!added && (
              <ListenerIpFamilyField
                services={services}
                nlbName={nlb.name}
                hasIpv4={hasIpv4}
                resetKey={ipv4ResetKey}
              />
            )}
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

function ListenerIpFamilyField({
  services,
  nlbName,
  hasIpv4,
  resetKey,
}: {
  services: NetworkService[];
  nlbName: string;
  hasIpv4: boolean;
  resetKey: number;
}) {
  const availability = useIpv4Availability();
  const requestIpv4 = useRequestIpv4();
  const backend = useWatch('backend') as string | undefined;
  const service = services.find((s) => s.name === backend);
  return (
    <IpFamilyField
      availability={availability}
      onRequest={() => requestIpv4(nlbName)}
      locations={service?.summary.locations}
      hasIpv4={hasIpv4}
      resetKey={resetKey}
    />
  );
}

function ReleaseIpv4Confirm({
  locations,
  onConfirm,
  onKeep,
}: {
  locations: string[];
  onConfirm: () => void;
  onKeep: () => void;
}) {
  const { control } = useField('ipv4');
  const checked = isChecked(useWatch('ipv4'));
  if (checked) return null;
  return (
    <div
      role="alert"
      aria-label="Release IPv4"
      className="flex flex-col gap-3 rounded-md bg-amber-500/10 px-4 py-3 text-xs font-medium text-amber-800 dark:text-amber-300">
      <p>{releaseIpv4Message(locations)}</p>
      <div className="flex gap-2">
        <Button type="danger" theme="solid" size="small" htmlType="submit" onClick={onConfirm}>
          Release IPv4 and save
        </Button>
        <Button
          type="quaternary"
          theme="borderless"
          size="small"
          htmlType="button"
          onClick={() => {
            control.change(true);
            onKeep();
          }}>
          Keep IPv4
        </Button>
      </div>
    </div>
  );
}

function editDefaults(row: ListenerRow): Partial<NlbListenerValues> {
  const protocol = row.listener.protocol === 'UDP' ? 'UDP' : 'TCP';
  return {
    protocol,
    port: row.listener.port,
    backend: row.backend?.name,
    backendPort: row.backend?.port,
    healthPort: row.backend?.healthCheck?.port ?? (protocol === 'UDP' ? undefined : SERVING_PORT),
  };
}

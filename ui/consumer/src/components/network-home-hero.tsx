import { ApiError, useCreateNetwork, useNetwork } from '../lib/api';
import { readyReasonToLabel } from '../schema';
import {
  DEFAULT_MTU,
  DEFAULT_NETWORK_NAME,
  NetworkMtuField,
  NetworkNameField,
  networkFormSchema,
  toCreateNetworkInput,
  type NetworkFormValues,
} from './network-form';
import { Card } from '@datum-cloud/datum-ui/card';
import { Form } from '@datum-cloud/datum-ui/form';
import { ConformAdapter } from '@datum-cloud/datum-ui/form/adapters/conform';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { cn } from '@datum-cloud/datum-ui/utils';
import {
  ChevronDownIcon,
  CircleCheckIcon,
  LoaderCircleIcon,
  NetworkIcon,
  PlugIcon,
  PlusIcon,
  ServerIcon,
  SplitIcon,
  type LucideIcon,
} from 'lucide-react';
import { useId, useState } from 'react';
import { useParams } from 'react-router';

const ATTACHMENTS: { icon: LucideIcon; title: string; description: string; highlighted?: boolean }[] = [
  {
    icon: NetworkIcon,
    title: 'Network',
    description: 'Private IPv6 backbone across every location',
    highlighted: true,
  },
  { icon: ServerIcon, title: 'Workloads', description: 'Containers and VMs get a private address on it' },
  { icon: SplitIcon, title: 'Load balancers', description: 'Public front door that reaches private services' },
  { icon: PlugIcon, title: 'Connectors', description: 'Bring in services from a laptop or your own servers' },
];

// Same colour as the primary button, in both themes. Set inline because the
// portal's CSS has no text or border utility for it (see the note below).
const ACCENT_TEXT = { color: 'var(--btn-primary)' };
const ACCENT_BORDER = { borderColor: 'var(--btn-primary)' };

function createErrorMessage(error: ApiError): string {
  if (error.status === 403) return "You don't have permission to create networks in this project.";
  return error.message;
}

// Rendered by the host in the project home's first-run slot
// (portal.hero/project-home) while the project has no Network. Once the
// network exists the host swaps this out on its own; until then the card
// watches the created network so the user sees it come up.
//
// The plugin ships no stylesheet, so every utility class here has to exist in
// the portal's compiled CSS. That is why the layout splits at md and avoids
// arbitrary values.
export default function NetworkHomeHero() {
  const { projectId } = useParams<{ projectId: string }>();
  const mutation = useCreateNetwork(projectId);
  const [createdName, setCreatedName] = useState<string>();
  const [pendingName, setPendingName] = useState(DEFAULT_NETWORK_NAME);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const advancedId = useId();
  const attachesId = useId();

  const handleSubmit = (data: NetworkFormValues) => {
    setPendingName(data.name);
    mutation.mutate(toCreateNetworkInput(data), {
      onSuccess: () => setCreatedName(data.name),
    });
  };

  return (
    <Card sectioned data-testid="networking-plugin-home-hero" className="overflow-hidden">
      <div className="flex flex-col md:flex-row">
        <div className="flex min-w-0 flex-1 flex-col gap-5 p-6 sm:p-8">
          <p
            className="flex items-center gap-2 text-xs font-semibold tracking-wider uppercase"
            style={ACCENT_TEXT}>
            <Icon icon={NetworkIcon} size={14} />
            Step 1 · your foundation
          </p>
          <h2 className="font-title text-3xl leading-tight">Create your private network</h2>
          <p className="text-muted-foreground max-w-2xl text-sm leading-relaxed">
            Everything you build on Datum connects through it. Workloads, load balancers and connectors
            attach to this network, and it reaches every Datum location the moment something uses it.
            Traffic between your pieces never crosses the public internet.
          </p>

          {createdName ? (
            <NetworkPendingStatus projectId={projectId} name={createdName} />
          ) : (
            <ConformAdapter>
              <Form.Root
                name="network-home-hero"
                schema={networkFormSchema}
                defaultValues={{ name: DEFAULT_NETWORK_NAME }}
                onSubmit={handleSubmit}
                isSubmitting={mutation.isPending}
                className="flex flex-col gap-3">
                <NetworkNameField
                  label="Network name"
                  placeholder={DEFAULT_NETWORK_NAME}
                  disabled={mutation.isPending}
                  className="max-w-xl"
                  trailing={
                    <Form.Submit
                      type="primary"
                      theme="solid"
                      className="shrink-0"
                      loadingText={`Creating ${pendingName}...`}>
                      <Icon icon={PlusIcon} className="size-4" />
                      Create network
                    </Form.Submit>
                  }
                />

                {mutation.error && (
                  <p
                    role="alert"
                    data-testid="networking-plugin-home-hero-error"
                    className="text-destructive text-xs font-medium">
                    {createErrorMessage(mutation.error)}
                  </p>
                )}

                <div className="text-muted-foreground flex flex-wrap items-center gap-x-5 gap-y-1 text-xs">
                  <span>IPv6 · address range assigned for you</span>
                  <span>MTU {DEFAULT_MTU}</span>
                  <button
                    type="button"
                    aria-expanded={advancedOpen}
                    aria-controls={advancedId}
                    onClick={() => setAdvancedOpen((open) => !open)}
                    className="inline-flex items-center gap-1 font-medium hover:underline"
                    style={ACCENT_TEXT}>
                    Advanced
                    <Icon
                      icon={ChevronDownIcon}
                      size={14}
                      className={cn('transition-transform', advancedOpen && 'rotate-180')}
                    />
                  </button>
                </div>

                {advancedOpen && (
                  <div id={advancedId} className="max-w-xs">
                    <NetworkMtuField disabled={mutation.isPending} />
                  </div>
                )}
              </Form.Root>
            </ConformAdapter>
          )}

          <p className="bg-muted text-muted-foreground rounded-md px-4 py-3 text-xs">
            Prefer the CLI?{' '}
            <code className="text-foreground font-mono">datumctl compute deploy</code> creates a
            network named <code className="text-foreground font-mono">default</code> for you.
          </p>
        </div>

        <aside
          aria-labelledby={attachesId}
          className="bg-muted/50 border-border flex flex-col gap-3 border-t p-6 sm:p-8 md:w-2/5 md:border-t-0 md:border-l">
          <h3
            id={attachesId}
            className="text-muted-foreground mb-1 text-xs font-semibold tracking-wider uppercase">
            What attaches to it
          </h3>
          <ul className="flex flex-col gap-3">
            {ATTACHMENTS.map((item) => (
              <li
                key={item.title}
                className={cn(
                  'bg-background flex items-start gap-3 rounded-lg border p-4',
                  !item.highlighted && 'border-border'
                )}
                style={item.highlighted ? ACCENT_BORDER : undefined}>
                <span
                  className={cn(
                    'bg-muted flex size-9 shrink-0 items-center justify-center rounded-md',
                    !item.highlighted && 'text-muted-foreground'
                  )}
                  style={item.highlighted ? ACCENT_TEXT : undefined}>
                  <Icon icon={item.icon} size={18} />
                </span>
                <div className="min-w-0">
                  <p className={cn('text-sm font-medium', !item.highlighted && 'text-muted-foreground')}>
                    {item.title}
                  </p>
                  <p className="text-muted-foreground text-xs">{item.description}</p>
                </div>
              </li>
            ))}
          </ul>
        </aside>
      </div>
    </Card>
  );
}

function NetworkPendingStatus({ projectId, name }: { projectId: string | undefined; name: string }) {
  // A 404 right after create just means the read hasn't caught up yet; the
  // query keeps polling, so only the Ready condition decides what to show.
  const { data: network } = useNetwork(projectId, name);
  const ready = network?.readyStatus === 'True';
  const detail =
    network?.readyStatus === 'False' && network.readyReason ? readyReasonToLabel(network.readyReason) : undefined;

  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="networking-plugin-home-hero-pending"
      className="bg-muted flex items-start gap-3 rounded-lg p-4">
      <Icon
        icon={ready ? CircleCheckIcon : LoaderCircleIcon}
        size={18}
        className={cn('mt-0.5 shrink-0', !ready && 'text-muted-foreground animate-spin')}
        style={ready ? ACCENT_TEXT : undefined}
      />
      <div className="min-w-0">
        <p className="text-sm font-medium">
          {ready ? `Network ${name} is ready` : `Waiting for ${name} to be ready...`}
        </p>
        <p className="text-muted-foreground text-xs">
          {ready
            ? 'Taking you to the next step.'
            : (detail ?? 'Datum is setting up the address range. This usually takes a few seconds.')}
        </p>
      </div>
    </div>
  );
}

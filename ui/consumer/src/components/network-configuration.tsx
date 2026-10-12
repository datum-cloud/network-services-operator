import { BadgeCopy } from './badge-copy';
import { ConfirmDeleteDialog } from './confirm-delete-dialog';
import { DangerCard } from './danger-card';
import { EnableIPv6Dialog } from './enable-ipv6-dialog';
import { FieldLabel } from './field-label';
import { StatusBadge } from './status-badge';
import { useDeleteNetwork, useUpdateNetwork } from '../lib/api';
import { readyStatusToBadgeType, readyStatusToLabel, type Network } from '../schema';
import { Button } from '@datum-cloud/datum-ui/button';
import {
  Card,
  CardAction,
  CardContent,
  CardField,
  CardFieldValue,
  CardHeader,
  CardSaveBar,
  CardTitle,
} from '@datum-cloud/datum-ui/card';
import { DateTime } from '@datum-cloud/datum-ui/date-time';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { Input } from '@datum-cloud/datum-ui/input';
import { SettingsNav, SettingsNavItem, SettingsNavLabel } from '@datum-cloud/datum-ui/settings-nav';
import { toast } from '@datum-cloud/datum-ui/toast';
import { Text, Title } from '@datum-cloud/datum-ui/typography';
import { PencilIcon, Share2Icon, SquareLibraryIcon, Trash2Icon, type LucideIcon } from 'lucide-react';
import { useEffect, useState } from 'react';

const SECTIONS: { id: string; label: string; icon: LucideIcon }[] = [
  { id: 'general', label: 'General', icon: SquareLibraryIcon },
  { id: 'ip-configuration', label: 'IP Configuration', icon: Share2Icon },
  { id: 'danger', label: 'Danger Zone', icon: Trash2Icon },
];

const SECTION_IDS = SECTIONS.map((s) => s.id);

const MTU_MIN = 1300;
const MTU_MAX = 8856;

function useSectionScrollSpy(sectionIds: readonly string[], fallback: string) {
  const [activeId, setActiveId] = useState(fallback);

  useEffect(() => {
    const readHash = () => {
      const hash = window.location.hash.replace(/^#/, '');
      return sectionIds.includes(hash) ? hash : undefined;
    };

    let pinned = false;
    let settleTimer: ReturnType<typeof setTimeout> | undefined;
    let frame: number | undefined;

    const compute = () => {
      frame = undefined;
      if (pinned) return;
      const line = window.innerHeight / 3;
      let current: string | undefined;
      for (const id of sectionIds) {
        const el = document.getElementById(id);
        if (!el) continue;
        if (el.getBoundingClientRect().top <= line) current = id;
        else break;
      }
      setActiveId(current ?? sectionIds[0] ?? fallback);
    };

    const unpinSoon = () => {
      if (settleTimer) clearTimeout(settleTimer);
      settleTimer = setTimeout(() => {
        pinned = false;
      }, 200);
    };

    const onScroll = () => {
      if (pinned) return unpinSoon();
      if (frame === undefined) frame = requestAnimationFrame(compute);
    };

    const pin = (target: string) => {
      pinned = true;
      setActiveId(target);
      unpinSoon();
    };

    const onHashChange = () => {
      const target = readHash();
      if (target) pin(target);
    };

    const onClick = (event: MouseEvent) => {
      const anchor = (event.target as Element | null)?.closest<HTMLAnchorElement>('a[href^="#"]');
      if (!anchor || anchor.getAttribute('aria-disabled') === 'true') return;
      const target = anchor.getAttribute('href')?.slice(1);
      if (target && sectionIds.includes(target)) pin(target);
    };

    const initial = readHash();
    if (initial) pin(initial);
    else compute();

    document.addEventListener('scroll', onScroll, { capture: true, passive: true });
    document.addEventListener('click', onClick);
    window.addEventListener('hashchange', onHashChange);
    window.addEventListener('resize', onScroll);
    return () => {
      document.removeEventListener('scroll', onScroll, { capture: true });
      document.removeEventListener('click', onClick);
      window.removeEventListener('hashchange', onHashChange);
      window.removeEventListener('resize', onScroll);
      if (settleTimer) clearTimeout(settleTimer);
      if (frame !== undefined) cancelAnimationFrame(frame);
    };
  }, [sectionIds, fallback]);

  return activeId;
}

function SectionCard({
  icon,
  title,
  action,
  footer,
  testId,
  children,
}: {
  icon: LucideIcon;
  title: string;
  action?: React.ReactNode;
  footer?: React.ReactNode;
  testId?: string;
  children: React.ReactNode;
}) {
  return (
    <Card size="sm" sectioned className="w-full overflow-hidden" data-testid={testId}>
      <CardHeader size="sm" bordered>
        <CardTitle className="flex items-center gap-2 text-sm">
          <Icon icon={icon} size={16} className="text-secondary" />
          {title}
        </CardTitle>
        {action && <CardAction>{action}</CardAction>}
      </CardHeader>
      <CardContent padding="none">{children}</CardContent>
      {footer}
    </Card>
  );
}

function Empty() {
  return <span className="text-muted-foreground">&mdash;</span>;
}

function GeneralCard({ network }: { network: Network }) {
  return (
    <SectionCard icon={SquareLibraryIcon} title="General" testId="networking-plugin-config-general">
      <CardField>
        <FieldLabel hint="Immutable identifier used by the API and CLI.">Resource name</FieldLabel>
        <CardFieldValue>
          <BadgeCopy value={network.name} badgeType="muted" badgeTheme="solid" />
        </CardFieldValue>
      </CardField>
      <CardField>
        <FieldLabel>Status</FieldLabel>
        <CardFieldValue>
          <StatusBadge type={readyStatusToBadgeType(network.readyStatus)}>
            {readyStatusToLabel(network.readyStatus, network.readyReason)}
          </StatusBadge>
        </CardFieldValue>
      </CardField>
      <CardField>
        <FieldLabel>Created</FieldLabel>
        <CardFieldValue>
          <DateTime date={network.createdAt} variant="detailed" className="text-sm" />
        </CardFieldValue>
      </CardField>
    </SectionCard>
  );
}

function mtuError(draft: string): string | undefined {
  const value = Number(draft);
  if (draft.trim() === '' || !Number.isInteger(value)) return 'Enter a whole number.';
  if (value < MTU_MIN || value > MTU_MAX) return `Must be between ${MTU_MIN} and ${MTU_MAX}.`;
  return undefined;
}

function IPConfigurationCard({
  network,
  projectId,
}: {
  network: Network;
  projectId: string | undefined;
}) {
  const [editing, setEditing] = useState(false);
  const [draftMtu, setDraftMtu] = useState('');
  const [enableOpen, setEnableOpen] = useState(false);
  const update = useUpdateNetwork(projectId);
  const hasIPv6 = network.ipFamilies.includes('IPv6');

  const startEdit = () => {
    setDraftMtu(network.mtu !== undefined ? String(network.mtu) : '');
    setEditing(true);
  };

  const error = editing ? mtuError(draftMtu) : undefined;
  const changeCount = editing && !error && Number(draftMtu) !== network.mtu ? 1 : 0;

  const save = () => {
    if (error || changeCount === 0) return;
    update.mutate(
      { network, spec: { mtu: Number(draftMtu) } },
      {
        onSuccess: () => {
          toast.success('Network', { description: 'IP configuration saved' });
          setEditing(false);
        },
        onError: (err) => {
          toast.error('Network', { description: err.message || 'Failed to save IP configuration' });
        },
      }
    );
  };

  return (
    <SectionCard
      icon={Share2Icon}
      title="IP Configuration"
      testId="networking-plugin-config-ip"
      action={
        <Button
          type="secondary"
          theme="outline"
          size="xs"
          className={editing ? 'invisible shrink-0' : 'shrink-0'}
          disabled={editing}
          aria-hidden={editing}
          tabIndex={editing ? -1 : undefined}
          onClick={startEdit}>
          <Icon icon={PencilIcon} size={12} />
          Edit
        </Button>
      }
      footer={
        editing ? (
          <CardSaveBar
            changeCount={changeCount}
            errorCount={error ? 1 : 0}
            saving={update.isPending}
            onCancel={() => setEditing(false)}
            onSave={save}
          />
        ) : null
      }>
      <CardField>
        <FieldLabel hint="The /48 allocated to this network from the tenant pool.">IPv6 prefix</FieldLabel>
        <CardFieldValue>
          {network.ipv6Prefix ? (
            <BadgeCopy value={network.ipv6Prefix} badgeType="muted" badgeTheme="solid" />
          ) : (
            <Empty />
          )}
        </CardFieldValue>
      </CardField>
      <CardField>
        <FieldLabel hint="Protocol versions permitted on this network.">IP families</FieldLabel>
        <CardFieldValue>
          <span className="flex flex-wrap items-center gap-2">
            {network.ipFamilies.length > 0 ? (
              network.ipFamilies.map((family) => (
                <StatusBadge key={family} type="muted">
                  {family}
                </StatusBadge>
              ))
            ) : (
              <Empty />
            )}
            {!hasIPv6 && (
              <Button type="secondary" theme="outline" size="xs" onClick={() => setEnableOpen(true)}>
                Enable IPv6
              </Button>
            )}
          </span>
        </CardFieldValue>
      </CardField>
      <CardField>
        <FieldLabel hint="How addresses are allocated for this network. Set when the network is created.">
          IPAM mode
        </FieldLabel>
        <CardFieldValue>{network.ipamMode ?? <Empty />}</CardFieldValue>
      </CardField>
      <CardField>
        <FieldLabel
          hint={`Maximum transmission unit, between ${MTU_MIN} and ${MTU_MAX}. Traffic between regions is encapsulated, so a larger MTU can stall connections on some paths.`}>
          MTU
        </FieldLabel>
        <CardFieldValue>
          {editing ? (
            <div className="flex w-full flex-col gap-1">
              <Input
                type="number"
                inputMode="numeric"
                min={MTU_MIN}
                max={MTU_MAX}
                value={draftMtu}
                onChange={(e) => setDraftMtu(e.target.value)}
                aria-invalid={!!error}
                aria-label="MTU"
                autoFocus
              />
              {error && (
                <Text as="p" size="xs" textColor="destructive">
                  {error}
                </Text>
              )}
            </div>
          ) : (
            (network.mtu ?? <Empty />)
          )}
        </CardFieldValue>
      </CardField>
      <EnableIPv6Dialog
        projectId={projectId}
        network={network}
        open={enableOpen}
        onOpenChange={setEnableOpen}
      />
    </SectionCard>
  );
}

function DangerZone({
  network,
  projectId,
  onDeleted,
}: {
  network: Network;
  projectId: string | undefined;
  onDeleted: () => void;
}) {
  const [confirmOpen, setConfirmOpen] = useState(false);
  const deleteMutation = useDeleteNetwork(projectId);

  return (
    <>
      <Title as="h3" level={6} weight="medium" className="mb-4">
        Delete network
      </Title>
      <DangerCard
        deleteText="Delete network"
        description="This permanently deletes the network and everything provisioned on it. This can't be undone."
        onDelete={() => setConfirmOpen(true)}
      />
      <ConfirmDeleteDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={`Delete network ${network.name}?`}
        description="This permanently deletes the network and everything provisioned on it. This can't be undone."
        onConfirm={async () => {
          try {
            await deleteMutation.mutateAsync(network.name);
            toast.success(`Deleted network ${network.name}`);
            onDeleted();
          } catch (err) {
            toast.error((err as Error).message);
            throw err;
          }
        }}
      />
    </>
  );
}

export function NetworkConfiguration({
  network,
  projectId,
  onDeleted,
}: {
  network: Network;
  projectId: string | undefined;
  onDeleted: () => void;
}) {
  const activeSection = useSectionScrollSpy(SECTION_IDS, 'general');

  return (
    <div className="flex flex-col gap-8 lg:flex-row lg:items-start lg:gap-10">
      <aside className="lg:sticky lg:top-0 lg:w-56 lg:shrink-0">
        <SettingsNav>
          <SettingsNavLabel>Settings</SettingsNavLabel>
          {SECTIONS.map((section) => (
            <SettingsNavItem
              key={section.id}
              href={`#${section.id}`}
              icon={<Icon icon={section.icon} size={16} />}
              active={activeSection === section.id}>
              {section.label}
            </SettingsNavItem>
          ))}
        </SettingsNav>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col gap-6">
        <section id="general" className="scroll-mt-24">
          <GeneralCard network={network} />
        </section>
        <section id="ip-configuration" className="scroll-mt-24">
          <IPConfigurationCard network={network} projectId={projectId} />
        </section>
        <section id="danger" className="scroll-mt-24">
          <DangerZone network={network} projectId={projectId} onDeleted={onDeleted} />
        </section>
      </div>
    </div>
  );
}

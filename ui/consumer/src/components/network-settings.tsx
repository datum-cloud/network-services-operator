import { ConfirmDeleteDialog } from './confirm-delete-dialog';
import { useDeleteNetwork } from '../lib/api';
import type { Network } from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import { Button } from '@datum-cloud/datum-ui/button';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { toast } from '@datum-cloud/datum-ui/toast';
import { cn } from '@datum-cloud/datum-ui/utils';
import { formatDistanceToNowStrict } from 'date-fns';
import {
  PencilIcon,
  Share2Icon,
  SlidersHorizontalIcon,
  Trash2Icon,
  TriangleAlertIcon,
  type LucideIcon,
} from 'lucide-react';
import { useRef, useState } from 'react';

type SectionId = 'general' | 'ipConfiguration' | 'dangerZone';

const SECTIONS: { id: SectionId; label: string; icon: LucideIcon; danger?: boolean }[] = [
  { id: 'general', label: 'General', icon: SlidersHorizontalIcon },
  { id: 'ipConfiguration', label: 'IP Configuration', icon: Share2Icon },
  { id: 'dangerZone', label: 'Danger Zone', icon: TriangleAlertIcon, danger: true },
];

function SettingsRow({
  label,
  description,
  children,
}: {
  label: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-1 gap-1 border-t py-4 first:border-t-0 first:pt-0 sm:grid-cols-2 sm:gap-4">
      <div>
        <p className="text-sm font-medium">{label}</p>
        {description && <p className="text-muted-foreground text-xs">{description}</p>}
      </div>
      <div className="text-sm sm:pt-0.5">{children}</div>
    </div>
  );
}

function SettingsCard({
  icon,
  title,
  description,
  children,
}: {
  icon: LucideIcon;
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <Card>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-start justify-between gap-4">
          <div className="flex items-start gap-3">
            <span className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-md">
              <Icon icon={icon} size={16} />
            </span>
            <div>
              <p className="text-sm font-semibold">{title}</p>
              <p className="text-muted-foreground text-sm">{description}</p>
            </div>
          </div>
          <Button type="secondary" theme="outline" size="xs" icon={<PencilIcon size={14} />} disabled>
            Edit
          </Button>
        </div>
        <div className="flex flex-col">{children}</div>
      </CardContent>
    </Card>
  );
}

function GeneralSection({ network }: { network: Network }) {
  return (
    <SettingsCard
      icon={SlidersHorizontalIcon}
      title="General"
      description="Basic identity and metadata for this network">
      <SettingsRow label="Resource name" description="Immutable identifier used by the API and CLI.">
        <span className="font-mono">{network.name}</span>
      </SettingsRow>
      <SettingsRow label="Created" description="When this network was created.">
        {formatDistanceToNowStrict(network.createdAt, { addSuffix: true })}
      </SettingsRow>
    </SettingsCard>
  );
}

function IPConfigurationSection({ network }: { network: Network }) {
  return (
    <SettingsCard
      icon={Share2Icon}
      title="IP Configuration"
      description="Address space and IP family for this network">
      <SettingsRow label="IPv6 prefix" description="The /48 allocated to this network from the tenant pool.">
        <span className="font-mono">{network.ipv6Prefix ?? '—'}</span>
      </SettingsRow>
      <SettingsRow label="IP families" description="Protocol versions permitted on this network.">
        <div className="flex gap-2">
          {network.ipFamilies.length > 0 ? (
            network.ipFamilies.map((family) => (
              <Badge key={family} type="muted" theme="light">
                {family}
              </Badge>
            ))
          ) : (
            <span>—</span>
          )}
        </div>
      </SettingsRow>
      <SettingsRow label="IPAM mode" description="How addresses are allocated for this network.">
        {network.ipamMode ?? '—'}
      </SettingsRow>
      <SettingsRow label="MTU" description="Maximum transmission unit for this network.">
        {network.mtu ?? '—'}
      </SettingsRow>
    </SettingsCard>
  );
}

function DangerZoneSection({
  network,
  projectId,
  onDeleted,
}: {
  network: Network;
  projectId: string | undefined;
  onDeleted: () => void;
}) {
  const deleteMutation = useDeleteNetwork(projectId);

  return (
    <Card className="border-red-200">
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-start gap-3">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-red-50">
            <Icon icon={TriangleAlertIcon} size={16} className="text-red-500" />
          </span>
          <div>
            <p className="text-sm font-semibold">Danger Zone</p>
            <p className="text-muted-foreground text-sm">Irreversible actions for this network</p>
          </div>
        </div>
        <SettingsRow
          label="Delete this network"
          description="Permanently deletes the network and everything provisioned on it. This can't be undone.">
          <ConfirmDeleteDialog
            trigger={
              <Button type="danger" theme="outline" icon={<Trash2Icon size={16} />}>
                Delete network
              </Button>
            }
            title={`Delete ${network.name}?`}
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
        </SettingsRow>
      </CardContent>
    </Card>
  );
}

export function NetworkSettings({
  network,
  projectId,
  onDeleted,
}: {
  network: Network;
  projectId: string | undefined;
  onDeleted: () => void;
}) {
  const [activeSection, setActiveSection] = useState<SectionId>('general');
  const sectionRefs = useRef<Partial<Record<SectionId, HTMLDivElement>>>({});

  const handleNavClick = (id: SectionId) => {
    setActiveSection(id);
    sectionRefs.current[id]?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  return (
    <div className="flex flex-col gap-6 sm:flex-row">
      <div
        className="flex shrink-0 flex-col gap-1 self-start sm:sticky sm:top-4"
        style={{ width: '16rem' }}>
        <p className="text-muted-foreground px-3 text-xs font-medium tracking-wide uppercase">
          Settings
        </p>
        {SECTIONS.map((section) => (
          <button
            key={section.id}
            type="button"
            onClick={() => handleNavClick(section.id)}
            className={cn(
              'flex items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors',
              activeSection === section.id ? 'bg-muted font-medium' : 'hover:bg-muted/50',
              section.danger && 'text-red-500'
            )}>
            <Icon icon={section.icon} size={16} />
            {section.label}
          </button>
        ))}
      </div>

      <div className="flex min-w-0 flex-1 flex-col gap-6">
        {SECTIONS.map((section) => (
          <div
            key={section.id}
            ref={(el) => {
              sectionRefs.current[section.id] = el ?? undefined;
            }}
            className="scroll-mt-4">
            {section.id === 'general' && <GeneralSection network={network} />}
            {section.id === 'ipConfiguration' && <IPConfigurationSection network={network} />}
            {section.id === 'dangerZone' && (
              <DangerZoneSection network={network} projectId={projectId} onDeleted={onDeleted} />
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

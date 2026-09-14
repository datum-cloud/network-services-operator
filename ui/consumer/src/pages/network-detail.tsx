import { NetworkResources } from '../components/network-resources';
import { NetworkServices } from '../components/network-services';
import { NetworkSettings } from '../components/network-settings';
import { ErrorOrRestrictedState, LoadingSkeleton } from '../components/states';
import { useNetwork } from '../lib/api';
import { readyStatusToBadgeType, readyStatusToLabel } from '../schema';
import { Badge } from '@datum-cloud/datum-ui/badge';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@datum-cloud/datum-ui/breadcrumb';
import { Button } from '@datum-cloud/datum-ui/button';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@datum-cloud/datum-ui/tabs';
import { HomeIcon } from 'lucide-react';
import { useState } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router';

const TABS = [
  { value: 'resources', label: 'Resources' },
  { value: 'services', label: 'Services' },
  { value: 'settings', label: 'Settings' },
];

export default function NetworkDetail() {
  const { projectId, networkName } = useParams<{
    projectId: string;
    networkName: string;
  }>();
  const { data: network, isLoading, error, refetch } = useNetwork(projectId, networkName);
  const [activeTab, setActiveTab] = useState('resources');
  const navigate = useNavigate();
  const location = useLocation();

  const projectHref = projectId ? `/project/${projectId}` : '/';
  // Relative to the current route rather than reconstructed from `serviceSlug`
  // (which the host route may not even carry) — mirrors network-table.tsx's
  // use of `location.pathname` for the forward link to this same page.
  const networksHref = location.pathname.replace(/\/[^/]+$/, '') || '/';

  return (
    <div data-testid="networking-plugin-network-detail" className="flex min-w-0 flex-col gap-6">
      <Breadcrumb className="min-w-0 overflow-x-auto">
        <BreadcrumbList className="flex-nowrap">
          <BreadcrumbItem>
            <BreadcrumbLink href={projectHref}>
              <Icon icon={HomeIcon} size={16} />
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbLink href={networksHref}>Networks</BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage>{networkName}</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      {isLoading && <LoadingSkeleton />}

      {!isLoading && error && (
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to view this network."
          onRetry={() => void refetch()}
        />
      )}

      {!isLoading && !error && network && (
        <>
          <div className="flex items-start justify-between gap-4">
            <div className="flex flex-col gap-1">
              <div className="flex items-center gap-3">
                <h1 className="font-serif text-2xl">{network.name}</h1>
                <Badge type={readyStatusToBadgeType(network.readyStatus)} theme="light">
                  {readyStatusToLabel(network.readyStatus, network.readyReason)}
                </Badge>
              </div>
              <p className="text-muted-foreground font-mono text-sm">
                {network.ipv6Prefix ?? '—'}
                {network.ipFamilies.length > 0 ? ` · ${network.ipFamilies.join(', ')}` : ''}
                {network.ipamMode ? ` · ${network.ipamMode}` : ''}
              </p>
            </div>
            {activeTab === 'settings' && (
              <div className="flex shrink-0 gap-2">
                <Button type="primary" disabled>
                  Save changes
                </Button>
              </div>
            )}
          </div>

          <Tabs value={activeTab} onValueChange={setActiveTab}>
            <div className="border-b pb-3">
              <TabsList>
                {TABS.map((tab) => (
                  <TabsTrigger key={tab.value} value={tab.value}>
                    {tab.label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </div>
            {TABS.map((tab) => (
              <TabsContent key={tab.value} value={tab.value}>
                {tab.value === 'resources' && (
                  <NetworkResources projectId={projectId} networkName={networkName} />
                )}
                {tab.value === 'services' && (
                  <NetworkServices projectId={projectId} networkName={networkName} />
                )}
                {tab.value === 'settings' && (
                  <NetworkSettings
                    network={network}
                    projectId={projectId}
                    onDeleted={() => navigate(networksHref)}
                  />
                )}
              </TabsContent>
            ))}
          </Tabs>
        </>
      )}
    </div>
  );
}

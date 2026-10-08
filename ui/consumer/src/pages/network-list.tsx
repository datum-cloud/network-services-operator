import { NetworkCliSections } from '../components/cli-section';
import { CreateNetworkDialog } from '../components/create-network-dialog';
import { NetworkStats } from '../components/network-stats';
import { NetworkTable } from '../components/network-table';
import { ErrorOrRestrictedState, LoadingSkeleton } from '../components/states';
import { useNetworks } from '../lib/api';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@datum-cloud/datum-ui/breadcrumb';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { PageTitle } from '@datum-cloud/datum-ui/page-title';
import { HomeIcon } from 'lucide-react';
import { useParams } from 'react-router';

export default function NetworkList() {
  const { projectId } = useParams<{ projectId: string; serviceSlug: string }>();
  const { data: networks, isLoading, error, refetch } = useNetworks(projectId);

  const projectHref = projectId ? `/project/${projectId}` : '/';

  return (
    <div data-testid="networking-plugin-network-list" className="flex min-w-0 flex-col gap-6">
      <Breadcrumb className="min-w-0 overflow-x-auto">
        <BreadcrumbList className="flex-nowrap">
          <BreadcrumbItem>
            <BreadcrumbLink href={projectHref}>
              <Icon icon={HomeIcon} size={16} />
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage>Networks</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <PageTitle
        title="Networks"
        description="The networks your workloads and load balancers run on"
        actions={<CreateNetworkDialog projectId={projectId} />}
      />

      {isLoading && <LoadingSkeleton />}

      {!isLoading && error && (
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to view this project's networks."
          onRetry={() => void refetch()}
        />
      )}

      {!isLoading && !error && (networks?.length ?? 0) === 0 && (
        <div className="flex flex-col gap-6" data-testid="networking-plugin-network-empty">
          <NetworkCliSections projectId={projectId} />
        </div>
      )}

      {!isLoading && !error && networks && networks.length > 0 && (
        <>
          <NetworkStats networks={networks} />
          <NetworkTable networks={networks} projectId={projectId} />
        </>
      )}
    </div>
  );
}

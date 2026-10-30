import { CreateNlbDialog } from '../components/create-nlb-dialog';
import { NlbEmptyState, type NlbCreatePreset } from '../components/nlb-empty-state';
import { NlbTable } from '../components/nlb-table';
import { NetworkListSkeleton } from '../components/skeletons';
import { ErrorOrRestrictedState } from '../components/states';
import { useNetworkLoadBalancerRoutes, useNetworkLoadBalancers } from '../lib/api';
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
import { PageTitle } from '@datum-cloud/datum-ui/page-title';
import { HomeIcon, PlusIcon } from 'lucide-react';
import { Link, useParams, useSearchParams } from 'react-router';

export default function NlbList() {
  const { projectId } = useParams<{ projectId: string; serviceSlug: string }>();
  const { data: nlbs, isLoading, error, refetch } = useNetworkLoadBalancers(projectId);
  // Only the table's backends column and delete use routes, so a failed read
  // leaves those unknown instead of failing the page.
  const { data: routes } = useNetworkLoadBalancerRoutes(projectId);
  const [searchParams, setSearchParams] = useSearchParams();
  const isEmpty = !isLoading && !error && (nlbs?.length ?? 0) === 0;

  const projectHref = projectId ? `/project/${projectId}` : '/';

  // Create opens from ?create, with a starter's protocol and port in the same
  // query, so a starter link can be shared or reloaded.
  const openCreate = (preset?: NlbCreatePreset) => {
    const params = new URLSearchParams({ create: '1' });
    if (preset) {
      params.set('protocol', preset.protocol);
      params.set('port', String(preset.port));
    }
    setSearchParams(params);
  };
  const closeCreate = () => setSearchParams({});

  return (
    <div data-testid="networking-plugin-nlb-list" className="flex min-w-0 flex-col gap-6">
      <Breadcrumb className="min-w-0 overflow-x-auto">
        <BreadcrumbList className="flex-nowrap">
          <BreadcrumbItem>
            <BreadcrumbLink asChild>
              <Link to={projectHref}>
                <Icon icon={HomeIcon} size={16} />
              </Link>
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage>NLB</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <PageTitle
        title="Network load balancers"
        actions={
          !isEmpty && (
            <Button
              type="primary"
              theme="solid"
              size="small"
              className="w-full sm:w-auto"
              onClick={() => openCreate()}>
              <Icon icon={PlusIcon} className="size-4" />
              Create load balancer
            </Button>
          )
        }
      />

      <CreateNlbDialog
        projectId={projectId}
        open={searchParams.get('create') === '1'}
        onOpenChange={(open) => (open ? openCreate() : closeCreate())}
        preset={createPresetFromQuery(searchParams)}
      />

      {isLoading && <NetworkListSkeleton />}

      {!isLoading && error && (
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to view this project's network load balancers."
          onRetry={() => void refetch()}
        />
      )}

      {isEmpty && <NlbEmptyState projectId={projectId} onCreate={openCreate} />}

      {!isLoading && !error && nlbs && nlbs.length > 0 && (
        <NlbTable nlbs={nlbs} routes={routes} projectId={projectId} />
      )}
    </div>
  );
}

function createPresetFromQuery(params: URLSearchParams): NlbCreatePreset | undefined {
  const protocol = params.get('protocol');
  const port = Number(params.get('port'));
  if (
    (protocol !== 'TCP' && protocol !== 'UDP') ||
    !Number.isInteger(port) ||
    port < 1 ||
    port > 65535
  ) {
    return undefined;
  }
  return { protocol, port };
}

import { AddListenerDialog } from '../components/add-listener-dialog';
import { NlbOverview } from '../components/nlb-overview';
import { NetworkOverviewSkeleton } from '../components/skeletons';
import { ErrorOrRestrictedState } from '../components/states';
import {
  useNetworkLoadBalancer,
  useNetworkLoadBalancerRoutes,
  useNetworkServices,
} from '../lib/api';
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
import { useState } from 'react';
import { Link, useLocation, useParams } from 'react-router';

/** The plugin's mount, the NLB list and the NLB itself, from wherever under the NLB we are. */
function useHrefs(nlbName: string | undefined) {
  const { pathname } = useLocation();
  const marker = `/nlb/${encodeURIComponent(nlbName ?? '')}`;
  const at = pathname.lastIndexOf(marker);
  const mount = at >= 0 ? pathname.slice(0, at) : pathname.replace(/\/[^/]*\/[^/]*\/?$/, '');
  return { mount, list: `${mount}/nlb` };
}

export default function NlbDetail() {
  const { projectId, nlbName } = useParams<{ projectId: string; nlbName: string }>();
  const { data: nlb, isLoading, error, refetch } = useNetworkLoadBalancer(projectId, nlbName);
  // Routes and services fill in backends and health. Wait for routes so a
  // listener doesn't flash "No route"; if they fail, show the listeners anyway.
  const { data: routes = [], isLoading: routesLoading } = useNetworkLoadBalancerRoutes(projectId);
  const { data: services = [] } = useNetworkServices(projectId);
  const hrefs = useHrefs(nlbName);
  const [addOpen, setAddOpen] = useState(false);

  const projectHref = projectId ? `/project/${projectId}` : '/';
  const servicesHref = nlb?.networkName
    ? `${hrefs.mount}/networks/${encodeURIComponent(nlb.networkName)}/services`
    : undefined;

  return (
    <div data-testid="networking-plugin-nlb-detail" className="flex min-w-0 flex-col gap-6">
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
            <BreadcrumbLink asChild>
              <Link to={hrefs.list}>NLB</Link>
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage>{nlbName}</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <PageTitle title={nlbName ?? ''} />

      {(isLoading || routesLoading) && <NetworkOverviewSkeleton />}

      {!isLoading && error && (
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to view this network load balancer."
          onRetry={() => void refetch()}
        />
      )}

      {nlb && !routesLoading && (
        <>
          <NlbOverview
            nlb={nlb}
            routes={routes}
            services={services}
            servicesHref={servicesHref}
            onAddListener={() => setAddOpen(true)}
          />
          <AddListenerDialog
            projectId={projectId}
            nlb={nlb}
            open={addOpen}
            onOpenChange={setAddOpen}
          />
        </>
      )}
    </div>
  );
}

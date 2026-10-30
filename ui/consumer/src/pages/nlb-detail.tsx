import { AddListenerDialog } from '../components/add-listener-dialog';
import { DeleteListenerDialog } from '../components/delete-listener-dialog';
import { ConfirmDeleteDialog } from '../components/confirm-delete-dialog';
import { NlbOverview, NlbStatusPill } from '../components/nlb-overview';
import { toListenerRows, type ListenerRow } from '../components/nlb-overview-model';
import { deleteDescription } from '../components/nlb-table';
import { NetworkOverviewSkeleton } from '../components/skeletons';
import { ErrorOrRestrictedState } from '../components/states';
import {
  useDeleteNetworkLoadBalancer,
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
import { Button } from '@datum-cloud/datum-ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@datum-cloud/datum-ui/dropdown';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { PageTitle } from '@datum-cloud/datum-ui/page-title';
import { toast } from '@datum-cloud/datum-ui/toast';
import { ChevronDownIcon, HomeIcon } from 'lucide-react';
import { useState } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router';

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
  // Open to add when there's no row to edit.
  const [listenerDialog, setListenerDialog] = useState<{ editing?: ListenerRow }>();
  const [deleting, setDeleting] = useState<ListenerRow>();
  const [deletingNlb, setDeletingNlb] = useState(false);
  const deleteNlb = useDeleteNetworkLoadBalancer(projectId);
  const navigate = useNavigate();

  const projectHref = projectId ? `/project/${projectId}` : '/';
  const networkHref = nlb?.networkName
    ? `${hrefs.mount}/networks/${encodeURIComponent(nlb.networkName)}`
    : undefined;
  const servicesHref = networkHref && `${networkHref}/services`;
  const ready = nlb && !routesLoading;
  const rows = ready ? toListenerRows(nlb, routes, services) : [];
  const attachedRoutes = routes.filter((r) => r.parents.some((p) => p.name === nlbName));

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

      <PageTitle
        title={nlbName ?? ''}
        actions={
          ready && (
            <div className="flex items-center gap-2">
              <NlbStatusPill rows={rows} />
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button type="quaternary" theme="outline" size="small" htmlType="button">
                    Actions
                    <Icon icon={ChevronDownIcon} className="size-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onSelect={() => setListenerDialog({})}>
                    Add listener
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    className="text-destructive"
                    onSelect={() => setDeletingNlb(true)}>
                    Delete load balancer
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          )
        }
      />

      {(isLoading || routesLoading) && <NetworkOverviewSkeleton />}

      {!isLoading && error && (
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to view this network load balancer."
          onRetry={() => void refetch()}
        />
      )}

      {ready && (
        <>
          <NlbOverview
            nlb={nlb}
            rows={rows}
            networkHref={networkHref}
            servicesHref={servicesHref}
            listenerActions={{
              onAdd: () => setListenerDialog({}),
              onEdit: (row) => setListenerDialog({ editing: row }),
              onDelete: setDeleting,
            }}
          />
          <AddListenerDialog
            projectId={projectId}
            nlb={nlb}
            open={listenerDialog !== undefined}
            onOpenChange={(open) => !open && setListenerDialog(undefined)}
            editing={listenerDialog?.editing}
          />
          <DeleteListenerDialog
            projectId={projectId}
            nlb={nlb}
            routes={routes}
            row={deleting}
            onOpenChange={(open) => !open && setDeleting(undefined)}
          />
          <ConfirmDeleteDialog
            open={deletingNlb}
            onOpenChange={setDeletingNlb}
            title={`Delete load balancer ${nlb.name}?`}
            description={deleteDescription({ nlb, routes: attachedRoutes })}
            onConfirm={async () => {
              try {
                await deleteNlb.mutateAsync({ name: nlb.name, routes: attachedRoutes });
                toast.success(`Deleted load balancer ${nlb.name}`);
                navigate(hrefs.list);
              } catch (err) {
                toast.error((err as Error).message);
                throw err;
              }
            }}
          />
        </>
      )}
    </div>
  );
}

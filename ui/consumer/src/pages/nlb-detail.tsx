import { AddListenerDialog } from '../components/add-listener-dialog';
import { DeleteListenerDialog } from '../components/delete-listener-dialog';
import { ConfirmDeleteDialog } from '../components/confirm-delete-dialog';
import { NlbOverview } from '../components/nlb-overview';
import { toListenerRows, type ListenerRow } from '../components/nlb-overview-model';
import { deleteDescription } from '../components/nlb-table';
import { PluginTabs } from '../components/plugin-tabs';
import { NetworkMetricsSkeleton, NetworkOverviewSkeleton } from '../components/skeletons';
import { ErrorOrRestrictedState } from '../components/states';
import {
  useDeleteNetworkLoadBalancer,
  useNetworkLoadBalancer,
  useNetworkLoadBalancerRoutes,
  useNetworkServices,
} from '../lib/api';
import type { NetworkLoadBalancer, NetworkLoadBalancerRoute, NetworkService } from '../schema';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@datum-cloud/datum-ui/breadcrumb';
import { Button } from '@datum-cloud/datum-ui/button';
import { DateTime } from '@datum-cloud/datum-ui/date-time';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { PageTitle } from '@datum-cloud/datum-ui/page-title';
import { toast } from '@datum-cloud/datum-ui/toast';
import { HomeIcon, Trash2Icon } from 'lucide-react';
import { Suspense, lazy, useState } from 'react';
import {
  Link,
  Navigate,
  Outlet,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useOutletContext,
  useParams,
} from 'react-router';

const NlbMetrics = lazy(() => import('../components/nlb-metrics').then((m) => ({ default: m.NlbMetrics })));
const NlbTrafficCard = lazy(() =>
  import('../components/nlb-metrics').then((m) => ({ default: m.NlbTrafficCard }))
);

interface NlbHrefs {
  /** The plugin's mount. */
  mount: string;
  list: string;
  overview: string;
  metrics: string;
}

interface NlbOutletContext {
  nlb: NetworkLoadBalancer;
  routes: NetworkLoadBalancerRoute[];
  services: NetworkService[];
  projectId: string | undefined;
  hrefs: NlbHrefs;
}

function useNlbOutlet() {
  return useOutletContext<NlbOutletContext>();
}

/** The plugin's mount, the NLB list and the NLB's tabs, from wherever under the NLB we are. */
function useHrefs(nlbName: string | undefined): NlbHrefs {
  const { pathname } = useLocation();
  const marker = `/nlb/${encodeURIComponent(nlbName ?? '')}`;
  const at = pathname.lastIndexOf(marker);
  const mount = at >= 0 ? pathname.slice(0, at) : pathname.replace(/\/[^/]*\/[^/]*\/?$/, '');
  const overview = `${mount}${marker}`;
  return { mount, list: `${mount}/nlb`, overview, metrics: `${overview}/metrics` };
}

function TabSkeleton() {
  const { '*': rest = '' } = useParams();
  return rest.split('/')[0] === 'metrics' ? <NetworkMetricsSkeleton /> : <NetworkOverviewSkeleton />;
}

function DetailLayout() {
  const { projectId, nlbName } = useParams<{ projectId: string; nlbName: string }>();
  const { data: nlb, isLoading, error, refetch } = useNetworkLoadBalancer(projectId, nlbName);
  // Routes and services fill in backends and health. Wait for routes so a
  // listener doesn't flash "No route"; if they fail, show the listeners anyway.
  const { data: routes = [], isLoading: routesLoading } = useNetworkLoadBalancerRoutes(projectId);
  const { data: services = [] } = useNetworkServices(projectId);
  const hrefs = useHrefs(nlbName);
  const [deletingNlb, setDeletingNlb] = useState(false);
  const deleteNlb = useDeleteNetworkLoadBalancer(projectId);
  const navigate = useNavigate();

  const projectHref = projectId ? `/project/${projectId}` : '/';
  const ready = nlb && !routesLoading;
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
        description={
          nlb && (
            <span className="text-muted-foreground text-sm">
              Network load balancer · created{' '}
              <DateTime date={nlb.createdAt} variant="relative" addSuffix />
            </span>
          )
        }
        actions={
          ready && (
            <div className="flex items-center gap-2">
              <Button
                type="danger"
                theme="outline"
                size="xs"
                htmlType="button"
                icon={<Icon icon={Trash2Icon} size={12} />}
                onClick={() => setDeletingNlb(true)}>
                Delete
              </Button>
            </div>
          )
        }
      />

      <PluginTabs
        testId="networking-plugin-nlb-tabs"
        tabs={[
          { label: 'Overview', href: hrefs.overview },
          { label: 'Metrics', href: hrefs.metrics },
        ]}
      />

      {(isLoading || routesLoading) && <TabSkeleton />}

      {!isLoading && error && (
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to view this network load balancer."
          onRetry={() => void refetch()}
        />
      )}

      {ready && (
        <>
          <Outlet context={{ nlb, routes, services, projectId, hrefs } satisfies NlbOutletContext} />
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

function OverviewTab() {
  const { nlb, routes, services, projectId, hrefs } = useNlbOutlet();
  // Open to add when there's no row to edit.
  const [listenerDialog, setListenerDialog] = useState<{ editing?: ListenerRow }>();
  const [deleting, setDeleting] = useState<ListenerRow>();
  const networkHref = nlb.networkName
    ? `${hrefs.mount}/networks/${encodeURIComponent(nlb.networkName)}`
    : undefined;
  const servicesHref = networkHref && `${networkHref}/services`;
  const rows = toListenerRows(nlb, routes, services);

  return (
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
        traffic={
          <Suspense fallback={null}>
            <NlbTrafficCard projectId={projectId} nlb={nlb} metricsHref={hrefs.metrics} />
          </Suspense>
        }
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
    </>
  );
}

function MetricsTab() {
  const { nlb, projectId } = useNlbOutlet();
  return (
    <Suspense fallback={<NetworkMetricsSkeleton />}>
      <NlbMetrics projectId={projectId} nlb={nlb} />
    </Suspense>
  );
}

function NotFoundTab() {
  const { hrefs } = useNlbOutlet();
  return <Navigate to={hrefs.overview} replace />;
}

export default function NlbDetail() {
  return (
    <Routes>
      <Route element={<DetailLayout />}>
        <Route index element={<OverviewTab />} />
        <Route path="metrics" element={<MetricsTab />} />
        <Route path="*" element={<NotFoundTab />} />
      </Route>
    </Routes>
  );
}

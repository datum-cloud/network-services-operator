import { NetworkOverview } from '../components/network-overview';
import { NetworkServices } from '../components/network-services';
import { NetworkConfiguration } from '../components/network-configuration';
import { PluginTabs } from '../components/plugin-tabs';
import {
  ConfigurationSkeleton,
  NetworkOverviewSkeleton,
  ServicesSkeleton,
} from '../components/skeletons';
import { ErrorOrRestrictedState } from '../components/states';
import { useNetwork } from '../lib/api';
import type { Network } from '../schema';
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
import { Skeleton } from '@datum-cloud/datum-ui/skeleton';
import { HomeIcon } from 'lucide-react';
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

interface DetailHrefs {
  project: string;
  networks: string;
  overview: string;
  services: string;
  configuration: string;
}

interface NetworkOutletContext {
  network: Network;
  projectId: string | undefined;
  hrefs: DetailHrefs;
}

function useNetworkOutlet() {
  return useOutletContext<NetworkOutletContext>();
}

function useDetailHrefs(projectId: string | undefined): DetailHrefs {
  const { pathname } = useLocation();
  const { '*': rest = '' } = useParams();
  const path = pathname.replace(/\/$/, '');
  const tail = rest.replace(/\/$/, '');
  const overview = tail ? path.slice(0, path.length - tail.length - 1) : path;
  return {
    project: projectId ? `/project/${projectId}` : '/',
    networks: overview.replace(/\/[^/]+$/, '') || '/',
    overview,
    services: `${overview}/services`,
    configuration: `${overview}/configuration`,
  };
}

function TabSkeleton() {
  const { '*': rest = '' } = useParams();
  const tab = rest.split('/')[0];
  if (tab === 'services') return <ServicesSkeleton />;
  if (tab === 'configuration') return <ConfigurationSkeleton />;
  return <NetworkOverviewSkeleton />;
}

function describe(network: Network | undefined): string | undefined {
  if (!network) return undefined;
  return [network.ipv6Prefix, network.ipFamilies.join(', ')].filter(Boolean).join(' · ') || undefined;
}

function DetailLayout() {
  const { projectId, networkName } = useParams<{ projectId: string; networkName: string }>();
  const { data: network, isLoading, error, refetch } = useNetwork(projectId, networkName);
  const hrefs = useDetailHrefs(projectId);

  return (
    <div data-testid="networking-plugin-network-detail" className="flex min-w-0 flex-col gap-6">
      <Breadcrumb className="min-w-0 overflow-x-auto">
        <BreadcrumbList className="flex-nowrap">
          <BreadcrumbItem>
            <BreadcrumbLink asChild>
              <Link to={hrefs.project}>
                <Icon icon={HomeIcon} size={16} />
              </Link>
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbLink asChild>
              <Link to={hrefs.networks}>VPC Networks</Link>
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem className="min-w-0">
            <BreadcrumbPage className="truncate">{networkName}</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <PageTitle
        title={networkName ?? 'Network'}
        description={
          isLoading ? (
            <Skeleton
              aria-hidden
              style={{ display: 'inline-block', width: '14em', maxWidth: '100%', height: '1em' }}
            />
          ) : (
            describe(network)
          )
        }
        descriptionClassName="font-mono break-all"
      />

      <PluginTabs
        testId="networking-plugin-network-tabs"
        tabs={[
          { label: 'Overview', href: hrefs.overview },
          { label: 'Services', href: hrefs.services },
          { label: 'Configuration', href: hrefs.configuration },
        ]}
      />

      {isLoading && <TabSkeleton />}

      {!isLoading && error && (
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to view this network."
          onRetry={() => void refetch()}
        />
      )}

      {!isLoading && !error && network && (
        <Outlet context={{ network, projectId, hrefs } satisfies NetworkOutletContext} />
      )}
    </div>
  );
}

function OverviewTab() {
  const { network, projectId } = useNetworkOutlet();
  return <NetworkOverview network={network} projectId={projectId} />;
}

function ServicesTab() {
  const { network, projectId } = useNetworkOutlet();
  return <NetworkServices projectId={projectId} networkName={network.name} />;
}

function ConfigurationTab() {
  const { network, projectId, hrefs } = useNetworkOutlet();
  const navigate = useNavigate();
  return (
    <NetworkConfiguration
      network={network}
      projectId={projectId}
      onDeleted={() => navigate(hrefs.networks)}
    />
  );
}

function NotFoundTab() {
  const { hrefs } = useNetworkOutlet();
  return <Navigate to={hrefs.overview} replace />;
}

export default function NetworkDetail() {
  return (
    <Routes>
      <Route element={<DetailLayout />}>
        <Route index element={<OverviewTab />} />
        <Route path="services" element={<ServicesTab />} />
        <Route path="configuration" element={<ConfigurationTab />} />
        <Route path="*" element={<NotFoundTab />} />
      </Route>
    </Routes>
  );
}

import { NetworkOverview } from '../components/network-overview';
import { NetworkResources } from '../components/network-resources';
import { NetworkServices } from '../components/network-services';
import { NetworkConfiguration } from '../components/network-configuration';
import { PluginTabs } from '../components/plugin-tabs';
import { ErrorOrRestrictedState, LoadingSkeleton } from '../components/states';
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
  resources: string;
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
    resources: `${overview}/resources`,
    services: `${overview}/services`,
    configuration: `${overview}/configuration`,
  };
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
              <Link to={hrefs.networks}>Networks</Link>
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
        description={describe(network)}
        descriptionClassName="font-mono break-all"
      />

      <PluginTabs
        testId="networking-plugin-network-tabs"
        tabs={[
          { label: 'Overview', href: hrefs.overview },
          { label: 'Resources', href: hrefs.resources },
          { label: 'Services', href: hrefs.services },
          { label: 'Configuration', href: hrefs.configuration },
        ]}
      />

      {isLoading && <LoadingSkeleton />}

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
  const { network, projectId, hrefs } = useNetworkOutlet();
  return <NetworkOverview network={network} projectId={projectId} resourcesHref={hrefs.resources} />;
}

function ResourcesTab() {
  const { network, projectId } = useNetworkOutlet();
  return <NetworkResources projectId={projectId} networkName={network.name} />;
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
        <Route path="resources" element={<ResourcesTab />} />
        <Route path="services" element={<ServicesTab />} />
        <Route path="configuration" element={<ConfigurationTab />} />
        <Route path="*" element={<NotFoundTab />} />
      </Route>
    </Routes>
  );
}

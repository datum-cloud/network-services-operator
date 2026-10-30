import { NlbEmptyState, ProtocolBadge, type NlbCreatePreset } from '../components/nlb-empty-state';
import { NetworkListSkeleton } from '../components/skeletons';
import { ErrorOrRestrictedState } from '../components/states';
import { useNetworkLoadBalancers } from '../lib/api';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@datum-cloud/datum-ui/breadcrumb';
import { Card, CardContent } from '@datum-cloud/datum-ui/card';
import { Icon } from '@datum-cloud/datum-ui/icons';
import { PageTitle } from '@datum-cloud/datum-ui/page-title';
import { HomeIcon } from 'lucide-react';
import { Link, useParams, useSearchParams } from 'react-router';

export default function NlbList() {
  const { projectId } = useParams<{ projectId: string; serviceSlug: string }>();
  const { data: nlbs, isLoading, error, refetch } = useNetworkLoadBalancers(projectId);
  const [, setSearchParams] = useSearchParams();
  const isEmpty = !isLoading && !error && (nlbs?.length ?? 0) === 0;

  const projectHref = projectId ? `/project/${projectId}` : '/';

  // The create flow (#801) opens from ?create and reads a starter's protocol
  // and port from the same query, so a starter link can be shared or reloaded.
  const openCreate = (preset?: NlbCreatePreset) => {
    const params = new URLSearchParams({ create: '1' });
    if (preset) {
      params.set('protocol', preset.protocol);
      params.set('port', String(preset.port));
    }
    setSearchParams(params);
  };

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

      <PageTitle title="Network load balancers" />

      {isLoading && <NetworkListSkeleton />}

      {!isLoading && error && (
        <ErrorOrRestrictedState
          error={error}
          restrictedMessage="You don't have permission to view this project's network load balancers."
          onRetry={() => void refetch()}
        />
      )}

      {isEmpty && <NlbEmptyState projectId={projectId} onCreate={openCreate} />}

      {/* Placeholder until the NLB table lands in #803. */}
      {!isLoading && !error && nlbs && nlbs.length > 0 && (
        <ul className="flex flex-col gap-3">
          {nlbs.map((nlb) => (
            <li key={nlb.uid}>
              <Card size="sm">
                <CardContent className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-sm font-medium">{nlb.name}</span>
                  {nlb.listeners.map((l) => (
                    <ProtocolBadge key={l.name} protocol={l.protocol} port={l.port} />
                  ))}
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

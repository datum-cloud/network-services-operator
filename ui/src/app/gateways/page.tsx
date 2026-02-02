'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { Plus, ExternalLink, Clock } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Button } from '@/components/common/Button';
import { StatusBadge } from '@/components/common/StatusBadge';
import { DataTable, Column } from '@/components/common/DataTable';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { EmptyState } from '@/components/common/EmptyState';
import { apiClient } from '@/api/client';
import type { HTTPProxy } from '@/api/types';

function getProxyStatus(proxy: HTTPProxy): 'success' | 'warning' | 'error' | 'info' {
  const conditions = proxy.status?.conditions || [];
  const programmed = conditions.find((c) => c.type === 'Programmed');
  const accepted = conditions.find((c) => c.type === 'Accepted');

  if (programmed?.status === 'True') return 'success';
  if (accepted?.status === 'True') return 'warning';
  if (accepted?.status === 'False') return 'error';
  return 'info';
}

function getProxyStatusText(proxy: HTTPProxy): string {
  const conditions = proxy.status?.conditions || [];
  const programmed = conditions.find((c) => c.type === 'Programmed');
  const accepted = conditions.find((c) => c.type === 'Accepted');

  if (programmed?.status === 'True') return 'Ready';
  if (accepted?.status === 'True' && programmed?.status === 'False') return programmed?.reason || 'Pending';
  if (accepted?.status === 'False') return accepted?.reason || 'Error';
  return 'Unknown';
}

function formatDate(timestamp?: string): string {
  if (!timestamp) return '-';
  const date = new Date(timestamp);
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  });
}

export default function GatewaysPage() {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['httpproxies'],
    queryFn: async () => {
      // Fetch from all namespaces by getting namespaces first
      const nsResponse = await apiClient.listNamespaces();
      const namespaces = nsResponse.items.map((ns) => ns.name);

      const allProxies: HTTPProxy[] = [];
      for (const ns of namespaces) {
        try {
          const response = await apiClient.listHTTPProxies(ns);
          allProxies.push(...(response.items || []));
        } catch {
          // Skip namespaces with no proxies
        }
      }
      return allProxies;
    },
  });

  const columns: Column<HTTPProxy>[] = [
    {
      key: 'name',
      header: 'Name',
      sortable: true,
      render: (proxy) => (
        <Link
          href={`/gateways/${proxy.metadata.namespace}/${proxy.metadata.name}`}
          className="font-medium text-primary-600 dark:text-primary-400 hover:underline"
        >
          {proxy.metadata.name}
        </Link>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      sortable: true,
      render: (proxy) => (
        <span className="text-sm text-gray-600 dark:text-dark-300">
          {proxy.metadata.namespace}
        </span>
      ),
    },
    {
      key: 'hostnames',
      header: 'Hostnames',
      render: (proxy) => (
        <div className="flex flex-wrap gap-1">
          {proxy.spec.hostnames?.slice(0, 2).map((hostname) => (
            <span
              key={hostname}
              className="inline-flex items-center gap-1 px-2 py-0.5 bg-gray-100 dark:bg-dark-700 rounded text-xs"
            >
              {hostname}
              <ExternalLink className="w-3 h-3 opacity-50" />
            </span>
          ))}
          {(proxy.spec.hostnames?.length || 0) > 2 && (
            <span className="text-xs text-gray-500 dark:text-dark-400">
              +{(proxy.spec.hostnames?.length || 0) - 2} more
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (proxy) => (
        <StatusBadge status={getProxyStatus(proxy)}>
          {getProxyStatusText(proxy)}
        </StatusBadge>
      ),
    },
    {
      key: 'created',
      header: 'Created',
      sortable: true,
      render: (proxy) => (
        <div className="flex items-center gap-1 text-sm text-gray-500 dark:text-dark-400">
          <Clock className="w-3.5 h-3.5" />
          {formatDate(proxy.metadata.creationTimestamp)}
        </div>
      ),
    },
  ];

  if (isLoading) {
    return <LoadingState message="Loading gateways..." />;
  }

  if (error) {
    return <ErrorState message="Failed to load gateways" onRetry={() => refetch()} />;
  }

  return (
    <div className="space-y-6">
      <Breadcrumb items={[{ name: 'Gateways' }]} />

      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Gateways</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Manage your HTTP proxy gateways
          </p>
        </div>
        <Link href="/gateways/create">
          <Button icon={<Plus className="w-4 h-4" />}>Create Gateway</Button>
        </Link>
      </div>

      {data && data.length > 0 ? (
        <DataTable
          data={data}
          columns={columns}
          searchable
          searchPlaceholder="Search gateways..."
          getRowId={(proxy) => `${proxy.metadata.namespace}/${proxy.metadata.name}`}
        />
      ) : (
        <EmptyState
          title="No gateways yet"
          description="Create your first HTTP proxy gateway to get started."
          action={{
            label: 'Create Gateway',
            href: '/gateways/create',
          }}
        />
      )}
    </div>
  );
}

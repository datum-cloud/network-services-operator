'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { Clock, Globe, CheckCircle, AlertCircle } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { StatusBadge } from '@/components/common/StatusBadge';
import { DataTable, Column } from '@/components/common/DataTable';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { EmptyState } from '@/components/common/EmptyState';
import { apiClient } from '@/api/client';
import type { Domain } from '@/api/types';

function getDomainStatus(domain: Domain): 'success' | 'warning' | 'error' | 'info' {
  const conditions = domain.status?.conditions || [];
  const verified = conditions.find((c) => c.type === 'Verified');

  if (verified?.status === 'True') return 'success';
  if (verified?.status === 'False') return 'warning';
  return 'info';
}

function getDomainStatusText(domain: Domain): string {
  const conditions = domain.status?.conditions || [];
  const verified = conditions.find((c) => c.type === 'Verified');

  if (verified?.status === 'True') return 'Verified';
  if (verified?.status === 'False') return 'Pending Verification';
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

export default function DomainsPage() {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['domains'],
    queryFn: async () => {
      const nsResponse = await apiClient.listNamespaces();
      const namespaces = nsResponse.items.map((ns) => ns.name);

      const allDomains: Domain[] = [];
      for (const ns of namespaces) {
        try {
          const response = await apiClient.listDomains(ns);
          allDomains.push(...(response.items || []));
        } catch {
          // Skip namespaces with no domains
        }
      }
      return allDomains;
    },
  });

  const columns: Column<Domain>[] = [
    {
      key: 'name',
      header: 'Domain Name',
      sortable: true,
      render: (domain) => (
        <Link
          href={`/domains/${domain.metadata.namespace}/${domain.metadata.name}`}
          className="flex items-center gap-2 font-medium text-primary-600 dark:text-primary-400 hover:underline"
        >
          <Globe className="w-4 h-4" />
          {domain.spec.domainName}
        </Link>
      ),
    },
    {
      key: 'resource',
      header: 'Resource Name',
      render: (domain) => (
        <span className="text-sm text-gray-600 dark:text-dark-300">
          {domain.metadata.name}
        </span>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      sortable: true,
      render: (domain) => (
        <span className="text-sm text-gray-600 dark:text-dark-300">
          {domain.metadata.namespace}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (domain) => (
        <StatusBadge status={getDomainStatus(domain)}>
          {getDomainStatusText(domain)}
        </StatusBadge>
      ),
    },
    {
      key: 'registrar',
      header: 'Registrar',
      render: (domain) => (
        <span className="text-sm text-gray-600 dark:text-dark-300">
          {domain.status?.registration?.registrar || '-'}
        </span>
      ),
    },
    {
      key: 'created',
      header: 'Created',
      sortable: true,
      render: (domain) => (
        <div className="flex items-center gap-1 text-sm text-gray-500 dark:text-dark-400">
          <Clock className="w-3.5 h-3.5" />
          {formatDate(domain.metadata.creationTimestamp)}
        </div>
      ),
    },
  ];

  if (isLoading) {
    return <LoadingState message="Loading domains..." />;
  }

  if (error) {
    return <ErrorState message="Failed to load domains" onRetry={() => refetch()} />;
  }

  return (
    <div className="space-y-6">
      <Breadcrumb items={[{ name: 'Domains' }]} />

      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Domains</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Manage domain verification and DNS settings
          </p>
        </div>
      </div>

      {data && data.length > 0 ? (
        <DataTable
          data={data}
          columns={columns}
          searchable
          searchPlaceholder="Search domains..."
          getRowId={(domain) => `${domain.metadata.namespace}/${domain.metadata.name}`}
        />
      ) : (
        <EmptyState
          title="No domains yet"
          description="Domains are created automatically when you configure gateways with custom hostnames."
          icon={<Globe className="w-8 h-8 text-gray-400 dark:text-dark-500" />}
        />
      )}
    </div>
  );
}

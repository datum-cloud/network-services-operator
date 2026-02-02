'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { Clock, Shield, Eye, Ban } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { StatusBadge } from '@/components/common/StatusBadge';
import { DataTable, Column } from '@/components/common/DataTable';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { EmptyState } from '@/components/common/EmptyState';
import { apiClient } from '@/api/client';
import type { TrafficProtectionPolicy } from '@/api/types';

function getPolicyModeStatus(mode: string): 'success' | 'warning' | 'error' | 'info' {
  switch (mode) {
    case 'Enforce':
      return 'success';
    case 'Observe':
      return 'warning';
    case 'Disabled':
      return 'error';
    default:
      return 'info';
  }
}

function getPolicyModeIcon(mode: string) {
  switch (mode) {
    case 'Enforce':
      return Shield;
    case 'Observe':
      return Eye;
    case 'Disabled':
      return Ban;
    default:
      return Shield;
  }
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

export default function PoliciesPage() {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['policies'],
    queryFn: async () => {
      const nsResponse = await apiClient.listNamespaces();
      const namespaces = nsResponse.items.map((ns) => ns.name);

      const allPolicies: TrafficProtectionPolicy[] = [];
      for (const ns of namespaces) {
        try {
          const response = await apiClient.listPolicies(ns);
          allPolicies.push(...(response.items || []));
        } catch {
          // Skip namespaces with no policies
        }
      }
      return allPolicies;
    },
  });

  const columns: Column<TrafficProtectionPolicy>[] = [
    {
      key: 'name',
      header: 'Name',
      sortable: true,
      render: (policy) => (
        <Link
          href={`/policies/${policy.metadata.namespace}/${policy.metadata.name}`}
          className="font-medium text-primary-600 dark:text-primary-400 hover:underline"
        >
          {policy.metadata.name}
        </Link>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      sortable: true,
      render: (policy) => (
        <span className="text-sm text-gray-600 dark:text-dark-300">
          {policy.metadata.namespace}
        </span>
      ),
    },
    {
      key: 'mode',
      header: 'Mode',
      render: (policy) => {
        const Icon = getPolicyModeIcon(policy.spec.mode);
        return (
          <StatusBadge status={getPolicyModeStatus(policy.spec.mode)}>
            <Icon className="w-3 h-3 mr-1" />
            {policy.spec.mode}
          </StatusBadge>
        );
      },
    },
    {
      key: 'targets',
      header: 'Targets',
      render: (policy) => (
        <span className="text-sm text-gray-600 dark:text-dark-300">
          {policy.spec.targetRefs?.length || 0} target{(policy.spec.targetRefs?.length || 0) !== 1 ? 's' : ''}
        </span>
      ),
    },
    {
      key: 'ruleSets',
      header: 'Rule Sets',
      render: (policy) => (
        <div className="flex flex-wrap gap-1">
          {policy.spec.ruleSets?.map((rs) => (
            <span
              key={rs.name}
              className="inline-flex items-center px-2 py-0.5 bg-gray-100 dark:bg-dark-700 rounded text-xs"
            >
              {rs.name}
            </span>
          ))}
        </div>
      ),
    },
    {
      key: 'created',
      header: 'Created',
      sortable: true,
      render: (policy) => (
        <div className="flex items-center gap-1 text-sm text-gray-500 dark:text-dark-400">
          <Clock className="w-3.5 h-3.5" />
          {formatDate(policy.metadata.creationTimestamp)}
        </div>
      ),
    },
  ];

  if (isLoading) {
    return <LoadingState message="Loading policies..." />;
  }

  if (error) {
    return <ErrorState message="Failed to load policies" onRetry={() => refetch()} />;
  }

  return (
    <div className="space-y-6">
      <Breadcrumb items={[{ name: 'Security Policies' }]} />

      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Security Policies</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Manage traffic protection and WAF policies
          </p>
        </div>
      </div>

      {data && data.length > 0 ? (
        <DataTable
          data={data}
          columns={columns}
          searchable
          searchPlaceholder="Search policies..."
          getRowId={(policy) => `${policy.metadata.namespace}/${policy.metadata.name}`}
        />
      ) : (
        <EmptyState
          title="No security policies yet"
          description="Create traffic protection policies to secure your gateways."
          icon={<Shield className="w-8 h-8 text-gray-400 dark:text-dark-500" />}
        />
      )}
    </div>
  );
}

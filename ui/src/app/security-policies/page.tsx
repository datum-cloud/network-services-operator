'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { Clock, Shield, Plus, Key, FileKey, Globe, Lock, Scale } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { StatusBadge } from '@/components/common/StatusBadge';
import { DataTable, Column } from '@/components/common/DataTable';
import { Button } from '@/components/common/Button';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { EmptyState } from '@/components/common/EmptyState';
import { apiClient } from '@/api/client';
import type { SecurityPolicy } from '@/api/types';

function getStatusFromConditions(policy: SecurityPolicy): 'success' | 'warning' | 'error' | 'info' {
  const programmed = policy.status?.conditions?.find(c => c.type === 'Programmed');
  const accepted = policy.status?.conditions?.find(c => c.type === 'Accepted');

  if (programmed?.status === 'True') return 'success';
  if (accepted?.status === 'True') return 'warning';
  if (programmed?.status === 'False' || accepted?.status === 'False') return 'error';
  return 'info';
}

function getEnabledAuthTypes(policy: SecurityPolicy): string[] {
  const types: string[] = [];
  if (policy.spec.basicAuth) types.push('Basic');
  if (policy.spec.apiKeyAuth) types.push('API Key');
  if (policy.spec.jwt) types.push('JWT');
  if (policy.spec.oidc) types.push('OIDC');
  if (policy.spec.cors) types.push('CORS');
  if (policy.spec.authorization) types.push('Authorization');
  return types;
}

function getAuthTypeIcon(type: string) {
  switch (type) {
    case 'Basic': return Key;
    case 'API Key': return FileKey;
    case 'JWT': return Shield;
    case 'OIDC': return Globe;
    case 'CORS': return Lock;
    case 'Authorization': return Scale;
    default: return Shield;
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

export default function SecurityPoliciesPage() {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['securityPolicies'],
    queryFn: async () => {
      const nsResponse = await apiClient.listNamespaces();
      const namespaces = nsResponse.items.map((ns) => ns.name);

      const allPolicies: SecurityPolicy[] = [];
      for (const ns of namespaces) {
        try {
          const response = await apiClient.listSecurityPolicies(ns);
          allPolicies.push(...(response.items || []));
        } catch {
          // Skip namespaces with no policies
        }
      }
      return allPolicies;
    },
  });

  const columns: Column<SecurityPolicy>[] = [
    {
      key: 'name',
      header: 'Name',
      sortable: true,
      render: (policy) => (
        <Link
          href={`/security-policies/${policy.metadata.namespace}/${policy.metadata.name}`}
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
      key: 'authTypes',
      header: 'Auth Types',
      render: (policy) => {
        const types = getEnabledAuthTypes(policy);
        if (types.length === 0) {
          return <span className="text-sm text-gray-400">None</span>;
        }
        return (
          <div className="flex flex-wrap gap-1">
            {types.map((type) => {
              const Icon = getAuthTypeIcon(type);
              return (
                <span
                  key={type}
                  className="inline-flex items-center gap-1 px-2 py-0.5 bg-gray-100 dark:bg-dark-700 rounded text-xs"
                  title={type}
                >
                  <Icon className="w-3 h-3" />
                  {type}
                </span>
              );
            })}
          </div>
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
      key: 'status',
      header: 'Status',
      render: (policy) => {
        const status = getStatusFromConditions(policy);
        const programmed = policy.status?.conditions?.find(c => c.type === 'Programmed');
        return (
          <StatusBadge status={status}>
            {programmed?.reason || 'Unknown'}
          </StatusBadge>
        );
      },
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
    return <LoadingState message="Loading security policies..." />;
  }

  if (error) {
    return <ErrorState message="Failed to load security policies" onRetry={() => refetch()} />;
  }

  return (
    <div className="space-y-6">
      <Breadcrumb items={[{ name: 'Security Policies' }]} />

      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Security Policies</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Manage authentication and authorization policies for your gateways
          </p>
        </div>
        <Link href="/security-policies/create">
          <Button icon={<Plus className="w-4 h-4" />}>
            Create Policy
          </Button>
        </Link>
      </div>

      {data && data.length > 0 ? (
        <DataTable
          data={data}
          columns={columns}
          searchable
          searchPlaceholder="Search security policies..."
          getRowId={(policy) => `${policy.metadata.namespace}/${policy.metadata.name}`}
        />
      ) : (
        <EmptyState
          title="No security policies yet"
          description="Create security policies to add authentication and authorization to your gateways."
          icon={<Shield className="w-8 h-8 text-gray-400 dark:text-dark-500" />}
          action={{
            label: 'Create Policy',
            href: '/security-policies/create',
          }}
        />
      )}
    </div>
  );
}

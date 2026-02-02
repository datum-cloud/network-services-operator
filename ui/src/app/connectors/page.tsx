'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { Clock, Network, Wifi, WifiOff, Tag } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { StatusBadge } from '@/components/common/StatusBadge';
import { DataTable, Column } from '@/components/common/DataTable';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { EmptyState } from '@/components/common/EmptyState';
import { apiClient } from '@/api/client';
import type { Connector } from '@/api/types';

function getConnectorStatus(connector: Connector): 'success' | 'warning' | 'error' | 'info' {
  const conditions = connector.status?.conditions || [];
  const ready = conditions.find((c) => c.type === 'Ready');

  if (ready?.status === 'True') return 'success';
  if (ready?.status === 'False') return 'error';
  return 'warning';
}

function getConnectorStatusText(connector: Connector): string {
  const conditions = connector.status?.conditions || [];
  const ready = conditions.find((c) => c.type === 'Ready');

  if (ready?.status === 'True') return 'Connected';
  if (ready?.status === 'False') return ready?.reason || 'Disconnected';
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

export default function ConnectorsPage() {
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['connectors'],
    queryFn: async () => {
      const nsResponse = await apiClient.listNamespaces();
      const namespaces = nsResponse.items.map((ns) => ns.name);

      const allConnectors: Connector[] = [];
      for (const ns of namespaces) {
        try {
          const response = await apiClient.listConnectors(ns);
          allConnectors.push(...(response.items || []));
        } catch {
          // Skip namespaces with no connectors
        }
      }
      return allConnectors;
    },
  });

  const columns: Column<Connector>[] = [
    {
      key: 'name',
      header: 'Name',
      sortable: true,
      render: (connector) => (
        <Link
          href={`/connectors/${connector.metadata.namespace}/${connector.metadata.name}`}
          className="flex items-center gap-2 font-medium text-primary-600 dark:text-primary-400 hover:underline"
        >
          <Network className="w-4 h-4" />
          {connector.metadata.name}
        </Link>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      sortable: true,
      render: (connector) => (
        <span className="text-sm text-gray-600 dark:text-dark-300">
          {connector.metadata.namespace}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      render: (connector) => {
        const status = getConnectorStatus(connector);
        const Icon = status === 'success' ? Wifi : WifiOff;
        return (
          <StatusBadge status={status}>
            <Icon className="w-3 h-3 mr-1" />
            {getConnectorStatusText(connector)}
          </StatusBadge>
        );
      },
    },
    {
      key: 'capabilities',
      header: 'Capabilities',
      render: (connector) => (
        <div className="flex flex-wrap gap-1">
          {connector.status?.capabilities?.map((cap) => (
            <span
              key={cap}
              className="inline-flex items-center px-2 py-0.5 bg-gray-100 dark:bg-dark-700 rounded text-xs"
            >
              {cap}
            </span>
          ))}
        </div>
      ),
    },
    {
      key: 'labels',
      header: 'Labels',
      render: (connector) => {
        const labels = connector.metadata.labels || {};
        const labelEntries = Object.entries(labels).slice(0, 2);
        return (
          <div className="flex flex-wrap gap-1">
            {labelEntries.map(([key, value]) => (
              <span
                key={key}
                className="inline-flex items-center px-2 py-0.5 bg-blue-50 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded text-xs"
              >
                {key}={value}
              </span>
            ))}
            {Object.keys(labels).length > 2 && (
              <span className="text-xs text-gray-500 dark:text-dark-400">
                +{Object.keys(labels).length - 2} more
              </span>
            )}
          </div>
        );
      },
    },
    {
      key: 'lastConnected',
      header: 'Last Connected',
      render: (connector) => (
        <div className="flex items-center gap-1 text-sm text-gray-500 dark:text-dark-400">
          <Clock className="w-3.5 h-3.5" />
          {connector.status?.connectionDetails?.lastConnected
            ? formatDate(connector.status.connectionDetails.lastConnected)
            : '-'}
        </div>
      ),
    },
  ];

  if (isLoading) {
    return <LoadingState message="Loading connectors..." />;
  }

  if (error) {
    return <ErrorState message="Failed to load connectors" onRetry={() => refetch()} />;
  }

  return (
    <div className="space-y-6">
      <Breadcrumb items={[{ name: 'Connectors' }]} />

      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-white">Connectors</h1>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            View edge connectors and their advertised services
          </p>
        </div>
      </div>

      {data && data.length > 0 ? (
        <DataTable
          data={data}
          columns={columns}
          searchable
          searchPlaceholder="Search connectors..."
          getRowId={(connector) => `${connector.metadata.namespace}/${connector.metadata.name}`}
        />
      ) : (
        <EmptyState
          title="No connectors yet"
          description="Connectors are created when edge agents connect to the control plane."
          icon={<Network className="w-8 h-8 text-gray-400 dark:text-dark-500" />}
        />
      )}
    </div>
  );
}

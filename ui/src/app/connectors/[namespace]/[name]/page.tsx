'use client';

import { useParams } from 'next/navigation';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, Network, Wifi, WifiOff, Server, Tag } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/common/Card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Tabs, TabPanel } from '@/components/common/Tabs';
import { YamlViewer } from '@/components/common/YamlViewer';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { apiClient } from '@/api/client';
import type { Connector, ConnectorAdvertisement } from '@/api/types';

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
  return date.toLocaleString();
}

export default function ConnectorDetailPage() {
  const params = useParams();
  const namespace = params.namespace as string;
  const name = params.name as string;

  const { data: connector, isLoading, error, refetch } = useQuery({
    queryKey: ['connector', namespace, name],
    queryFn: () => apiClient.getConnector(name, namespace),
    enabled: !!namespace && !!name,
  });

  const { data: advertisements } = useQuery({
    queryKey: ['connector-advertisements', namespace, name],
    queryFn: () => apiClient.getConnectorAdvertisementsByConnector(name, namespace),
    enabled: !!namespace && !!name,
  });

  if (isLoading) {
    return <LoadingState message="Loading connector..." />;
  }

  if (error || !connector) {
    return (
      <ErrorState
        title="Connector not found"
        message="The connector you're looking for doesn't exist or has been deleted."
        onRetry={() => refetch()}
      />
    );
  }

  const status = getConnectorStatus(connector);
  const StatusIcon = status === 'success' ? Wifi : WifiOff;

  const tabs = [
    { id: 'overview', label: 'Overview' },
    { id: 'advertisements', label: 'Advertised Services' },
    { id: 'yaml', label: 'YAML' },
  ];

  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Connectors', href: '/connectors' },
          { name: connector.metadata.name },
        ]}
      />

      {/* Header */}
      <div className="flex items-start gap-4">
        <Link
          href="/connectors"
          className="mt-1 p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-dark-800 transition-colors"
        >
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <div>
          <div className="flex items-center gap-3">
            <Network className="w-6 h-6 text-gray-400" />
            <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
              {connector.metadata.name}
            </h1>
            <StatusBadge status={status}>
              <StatusIcon className="w-3 h-3 mr-1" />
              {getConnectorStatusText(connector)}
            </StatusBadge>
          </div>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Namespace: {connector.metadata.namespace}
          </p>
        </div>
      </div>

      {/* Tabs */}
      <Tabs tabs={tabs} defaultTab="overview">
        <TabPanel id="overview">
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            {/* Connector Details */}
            <Card>
              <CardHeader>
                <CardTitle>Connector Details</CardTitle>
              </CardHeader>
              <CardContent>
                <dl className="space-y-4">
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Name</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{connector.metadata.name}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Namespace</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{connector.metadata.namespace}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Class</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                      {connector.spec.connectorClassName}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Created</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                      {formatDate(connector.metadata.creationTimestamp)}
                    </dd>
                  </div>
                </dl>
              </CardContent>
            </Card>

            {/* Connection Details */}
            {connector.status?.connectionDetails && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Server className="w-4 h-4" />
                    Connection
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-4">
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Endpoint</dt>
                      <dd className="mt-1 text-sm font-mono text-gray-900 dark:text-white break-all">
                        {connector.status.connectionDetails.endpoint}
                      </dd>
                    </div>
                    {connector.status.connectionDetails.clientId && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Client ID</dt>
                        <dd className="mt-1 text-sm font-mono text-gray-600 dark:text-dark-300">
                          {connector.status.connectionDetails.clientId}
                        </dd>
                      </div>
                    )}
                    {connector.status.connectionDetails.lastConnected && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Last Connected</dt>
                        <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                          {formatDate(connector.status.connectionDetails.lastConnected)}
                        </dd>
                      </div>
                    )}
                  </dl>
                </CardContent>
              </Card>
            )}

            {/* Capabilities */}
            <Card>
              <CardHeader>
                <CardTitle>Capabilities</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="flex flex-wrap gap-2">
                  {connector.status?.capabilities?.map((cap) => (
                    <span
                      key={cap}
                      className="inline-flex items-center px-3 py-1 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded-lg text-sm"
                    >
                      {cap}
                    </span>
                  ))}
                </div>
              </CardContent>
            </Card>

            {/* Labels */}
            {connector.metadata.labels && Object.keys(connector.metadata.labels).length > 0 && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Tag className="w-4 h-4" />
                    Labels
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="flex flex-wrap gap-2">
                    {Object.entries(connector.metadata.labels).map(([key, value]) => (
                      <span
                        key={key}
                        className="inline-flex items-center px-2 py-1 bg-gray-100 dark:bg-dark-700 rounded text-xs"
                      >
                        <span className="font-medium text-gray-700 dark:text-dark-200">{key}</span>
                        <span className="mx-1 text-gray-400">=</span>
                        <span className="text-gray-600 dark:text-dark-300">{value}</span>
                      </span>
                    ))}
                  </div>
                </CardContent>
              </Card>
            )}

            {/* Conditions */}
            <Card className="lg:col-span-2">
              <CardHeader>
                <CardTitle>Conditions</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="space-y-3">
                  {connector.status?.conditions?.map((condition, idx) => (
                    <div
                      key={idx}
                      className={`p-4 rounded-lg ${
                        condition.status === 'True'
                          ? 'bg-green-50 dark:bg-green-900/20'
                          : 'bg-red-50 dark:bg-red-900/20'
                      }`}
                    >
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                          <StatusBadge status={condition.status === 'True' ? 'success' : 'error'}>
                            {condition.type}
                          </StatusBadge>
                          <span className="text-sm text-gray-600 dark:text-dark-300">{condition.reason}</span>
                        </div>
                        <span className="text-xs text-gray-500 dark:text-dark-400">
                          {formatDate(condition.lastTransitionTime)}
                        </span>
                      </div>
                      {condition.message && (
                        <p className="mt-2 text-sm text-gray-600 dark:text-dark-300">{condition.message}</p>
                      )}
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>
          </div>
        </TabPanel>

        <TabPanel id="advertisements">
          <div className="space-y-6">
            {advertisements && advertisements.length > 0 ? (
              advertisements.map((adv) => (
                <Card key={adv.metadata.uid}>
                  <CardHeader>
                    <CardTitle>{adv.metadata.name}</CardTitle>
                  </CardHeader>
                  <CardContent>
                    {adv.spec.layer4 && adv.spec.layer4.length > 0 && (
                      <div className="space-y-4">
                        <h4 className="text-sm font-medium text-gray-700 dark:text-dark-300">Layer 4 Services</h4>
                        <div className="space-y-3">
                          {adv.spec.layer4.map((service, idx) => (
                            <div
                              key={idx}
                              className="p-4 bg-gray-50 dark:bg-dark-800 rounded-lg"
                            >
                              <div className="flex items-center justify-between mb-2">
                                <span className="font-medium text-gray-900 dark:text-white">{service.name}</span>
                                <span className="text-sm font-mono text-gray-600 dark:text-dark-300">
                                  {service.address}
                                </span>
                              </div>
                              <div className="flex flex-wrap gap-2">
                                {service.ports?.map((port, pidx) => (
                                  <span
                                    key={pidx}
                                    className="inline-flex items-center px-2 py-1 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded text-xs font-mono"
                                  >
                                    {port.protocol}/{port.port}
                                    {port.targetPort && port.targetPort !== port.port && ` -> ${port.targetPort}`}
                                  </span>
                                ))}
                              </div>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </CardContent>
                </Card>
              ))
            ) : (
              <Card>
                <CardContent className="py-8">
                  <div className="text-center text-gray-500 dark:text-dark-400">
                    <Server className="w-8 h-8 mx-auto mb-2 opacity-50" />
                    <p className="text-sm">No advertised services</p>
                  </div>
                </CardContent>
              </Card>
            )}
          </div>
        </TabPanel>

        <TabPanel id="yaml">
          <YamlViewer data={connector} title={`${connector.metadata.name}.yaml`} />
        </TabPanel>
      </Tabs>
    </div>
  );
}

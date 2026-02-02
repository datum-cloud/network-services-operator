'use client';

import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Trash2, Edit, ExternalLink, Clock, Tag, Network, Server, Route, Globe, Layers, PlayCircle } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Button } from '@/components/common/Button';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/common/Card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Tabs, TabPanel } from '@/components/common/Tabs';
import { YamlViewer } from '@/components/common/YamlViewer';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
import { Modal } from '@/components/common/Modal';
import { apiClient } from '@/api/client';
import type { HTTPProxy, HTTPProxyRelatedResources, Condition } from '@/api/types';
import { TestProxyModal } from '@/components/httpproxy/TestProxyModal';
import { useState } from 'react';

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
  return date.toLocaleString();
}

function getConditionStatus(conditions: Condition[] | undefined, type: string): 'success' | 'warning' | 'error' | 'info' {
  const condition = conditions?.find(c => c.type === type);
  if (!condition) return 'info';
  return condition.status === 'True' ? 'success' : condition.status === 'False' ? 'error' : 'warning';
}

function RelatedResourcesPanel({
  relatedResources,
  isLoading,
  namespace,
}: {
  relatedResources: HTTPProxyRelatedResources | undefined;
  isLoading: boolean;
  namespace: string;
}) {
  if (isLoading) {
    return (
      <div className="flex items-center justify-center p-8">
        <div className="text-gray-500 dark:text-dark-400">Loading related resources...</div>
      </div>
    );
  }

  if (!relatedResources) {
    return (
      <div className="text-gray-500 dark:text-dark-400 p-4">
        No related resources found.
      </div>
    );
  }

  const { gateway, httpRoute, endpointSlices, domains } = relatedResources;

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
      {/* Gateway */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Server className="w-4 h-4" />
            Gateway
          </CardTitle>
        </CardHeader>
        <CardContent>
          {gateway ? (
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium text-gray-900 dark:text-white">
                  {gateway.metadata.name}
                </span>
                <StatusBadge status={getConditionStatus(gateway.status?.conditions, 'Programmed')}>
                  {gateway.status?.conditions?.find(c => c.type === 'Programmed')?.reason || 'Unknown'}
                </StatusBadge>
              </div>
              <div>
                <dt className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-1">Gateway Class</dt>
                <dd className="text-sm text-gray-900 dark:text-white">{gateway.spec.gatewayClassName}</dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-1">Listeners</dt>
                <dd className="space-y-2">
                  {gateway.spec.listeners.map((listener, idx) => (
                    <div key={idx} className="flex items-center justify-between p-2 bg-gray-50 dark:bg-dark-800 rounded text-sm">
                      <span className="font-mono">{listener.name}</span>
                      <span className="text-gray-500 dark:text-dark-400">
                        {listener.protocol}:{listener.port}
                        {listener.hostname && ` (${listener.hostname})`}
                      </span>
                    </div>
                  ))}
                </dd>
              </div>
              {gateway.status?.addresses && gateway.status.addresses.length > 0 && (
                <div>
                  <dt className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-1">Addresses</dt>
                  <dd className="space-y-1">
                    {gateway.status.addresses.map((addr, idx) => (
                      <div key={idx} className="text-sm font-mono text-gray-700 dark:text-dark-200">
                        {addr.value} ({addr.type})
                      </div>
                    ))}
                  </dd>
                </div>
              )}
            </div>
          ) : (
            <div className="text-sm text-gray-500 dark:text-dark-400">No Gateway found</div>
          )}
        </CardContent>
      </Card>

      {/* HTTPRoute */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Route className="w-4 h-4" />
            HTTPRoute
          </CardTitle>
        </CardHeader>
        <CardContent>
          {httpRoute ? (
            <div className="space-y-4">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium text-gray-900 dark:text-white">
                  {httpRoute.metadata.name}
                </span>
                <StatusBadge
                  status={httpRoute.status?.parents?.[0]?.conditions?.find(c => c.type === 'Accepted')?.status === 'True' ? 'success' : 'warning'}
                >
                  {httpRoute.status?.parents?.[0]?.conditions?.find(c => c.type === 'Accepted')?.reason || 'Unknown'}
                </StatusBadge>
              </div>
              {httpRoute.spec.parentRefs && httpRoute.spec.parentRefs.length > 0 && (
                <div>
                  <dt className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-1">Parent Refs</dt>
                  <dd className="space-y-1">
                    {httpRoute.spec.parentRefs.map((ref, idx) => (
                      <div key={idx} className="text-sm font-mono text-gray-700 dark:text-dark-200">
                        {ref.kind || 'Gateway'}/{ref.name}
                        {ref.sectionName && ` (${ref.sectionName})`}
                      </div>
                    ))}
                  </dd>
                </div>
              )}
              <div>
                <dt className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-1">Rules</dt>
                <dd className="text-sm text-gray-700 dark:text-dark-200">
                  {httpRoute.spec.rules?.length || 0} rule(s)
                </dd>
              </div>
              {httpRoute.spec.rules?.map((rule, ruleIdx) => (
                <div key={ruleIdx} className="p-2 bg-gray-50 dark:bg-dark-800 rounded">
                  <div className="text-xs text-gray-500 dark:text-dark-400 mb-1">Rule {ruleIdx + 1} Backends:</div>
                  {rule.backendRefs?.map((backend, bIdx) => (
                    <div key={bIdx} className="text-sm font-mono text-gray-700 dark:text-dark-200">
                      {backend.kind || 'Service'}/{backend.name}:{backend.port}
                      {backend.weight !== undefined && ` (weight: ${backend.weight})`}
                    </div>
                  ))}
                </div>
              ))}
            </div>
          ) : (
            <div className="text-sm text-gray-500 dark:text-dark-400">No HTTPRoute found</div>
          )}
        </CardContent>
      </Card>

      {/* EndpointSlices */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Layers className="w-4 h-4" />
            EndpointSlices ({endpointSlices.length})
          </CardTitle>
        </CardHeader>
        <CardContent>
          {endpointSlices.length > 0 ? (
            <div className="space-y-3">
              {endpointSlices.map((slice, idx) => (
                <div key={idx} className="p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
                  <div className="flex items-center justify-between mb-2">
                    <span className="text-sm font-medium text-gray-900 dark:text-white">
                      {slice.metadata.name}
                    </span>
                    <span className="text-xs px-2 py-0.5 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded">
                      {slice.addressType}
                    </span>
                  </div>
                  <div className="space-y-1">
                    {slice.endpoints.map((endpoint, eIdx) => (
                      <div key={eIdx} className="flex items-center justify-between text-sm">
                        <span className="font-mono text-gray-700 dark:text-dark-200">
                          {endpoint.addresses.join(', ')}
                        </span>
                        <StatusBadge status={endpoint.conditions?.ready ? 'success' : 'warning'} size="sm">
                          {endpoint.conditions?.ready ? 'Ready' : 'Not Ready'}
                        </StatusBadge>
                      </div>
                    ))}
                  </div>
                  {slice.ports && slice.ports.length > 0 && (
                    <div className="mt-2 flex flex-wrap gap-1">
                      {slice.ports.map((port, pIdx) => (
                        <span key={pIdx} className="text-xs px-2 py-0.5 bg-gray-200 dark:bg-dark-700 rounded">
                          {port.name || 'port'}:{port.port}/{port.protocol}
                          {port.appProtocol && ` (${port.appProtocol})`}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <div className="text-sm text-gray-500 dark:text-dark-400">No EndpointSlices found</div>
          )}
        </CardContent>
      </Card>

      {/* Domains */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Globe className="w-4 h-4" />
            Domains ({domains.length})
          </CardTitle>
        </CardHeader>
        <CardContent>
          {domains.length > 0 ? (
            <div className="space-y-3">
              {domains.map((domain, idx) => {
                const verified = domain.status?.conditions?.find(c => c.type === 'Verified');
                return (
                  <div key={idx} className="p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
                    <div className="flex items-center justify-between">
                      <Link
                        href={`/domains/${domain.metadata.namespace}/${domain.metadata.name}`}
                        className="text-sm font-medium text-blue-600 dark:text-blue-400 hover:underline"
                      >
                        {domain.spec.domainName}
                      </Link>
                      <StatusBadge status={verified?.status === 'True' ? 'success' : 'warning'}>
                        {verified?.status === 'True' ? 'Verified' : 'Pending'}
                      </StatusBadge>
                    </div>
                    {verified?.message && (
                      <p className="mt-1 text-xs text-gray-500 dark:text-dark-400">{verified.message}</p>
                    )}
                  </div>
                );
              })}
            </div>
          ) : (
            <div className="text-sm text-gray-500 dark:text-dark-400">No Domains found</div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export default function GatewayDetailPage() {
  const params = useParams();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [showDeleteModal, setShowDeleteModal] = useState(false);
  const [showTestModal, setShowTestModal] = useState(false);

  const namespace = params.namespace as string;
  const name = params.name as string;

  const { data: proxy, isLoading, error, refetch } = useQuery({
    queryKey: ['httpproxy', namespace, name],
    queryFn: () => apiClient.getHTTPProxy(name, namespace),
    enabled: !!namespace && !!name,
  });

  const { data: relatedResources, isLoading: relatedLoading } = useQuery({
    queryKey: ['httpproxy-related', namespace, name],
    queryFn: () => apiClient.getHTTPProxyRelatedResources(name, namespace),
    enabled: !!namespace && !!name && !!proxy,
  });

  const deleteMutation = useMutation({
    mutationFn: () => apiClient.deleteHTTPProxy(name, namespace),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['httpproxies'] });
      router.push('/gateways');
    },
  });

  if (isLoading) {
    return <LoadingState message="Loading gateway..." />;
  }

  if (error || !proxy) {
    return (
      <ErrorState
        title="Gateway not found"
        message="The gateway you're looking for doesn't exist or has been deleted."
        onRetry={() => refetch()}
      />
    );
  }

  const tabs = [
    { id: 'overview', label: 'Overview' },
    { id: 'routing', label: 'Routing Rules' },
    { id: 'related', label: 'Related Resources' },
    { id: 'yaml', label: 'YAML' },
  ];

  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Gateways', href: '/gateways' },
          { name: proxy.metadata.name },
        ]}
      />

      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-start gap-4">
          <Link
            href="/gateways"
            className="mt-1 p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-dark-800 transition-colors"
          >
            <ArrowLeft className="w-5 h-5" />
          </Link>
          <div>
            <div className="flex items-center gap-3">
              <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
                {proxy.metadata.name}
              </h1>
              <StatusBadge status={getProxyStatus(proxy)}>
                {getProxyStatusText(proxy)}
              </StatusBadge>
            </div>
            <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
              Namespace: {proxy.metadata.namespace}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="primary"
            icon={<PlayCircle className="w-4 h-4" />}
            onClick={() => setShowTestModal(true)}
          >
            Test
          </Button>
          <Button
            type="button"
            variant="outline"
            icon={<Edit className="w-4 h-4" />}
            onClick={() => router.push(`/gateways/${namespace}/${name}/edit`)}
          >
            Edit
          </Button>
          <Button
            variant="danger"
            icon={<Trash2 className="w-4 h-4" />}
            onClick={() => setShowDeleteModal(true)}
          >
            Delete
          </Button>
        </div>
      </div>

      {/* Tabs */}
      <Tabs tabs={tabs} defaultTab="overview">
        <TabPanel id="overview">
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            {/* Details */}
            <Card>
              <CardHeader>
                <CardTitle>Details</CardTitle>
              </CardHeader>
              <CardContent>
                <dl className="space-y-4">
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Name</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{proxy.metadata.name}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Namespace</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{proxy.metadata.namespace}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">UID</dt>
                    <dd className="mt-1 text-xs font-mono text-gray-600 dark:text-dark-300">{proxy.metadata.uid}</dd>
                  </div>
                  <div className="flex items-center gap-1">
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Created</dt>
                    <Clock className="w-3.5 h-3.5 text-gray-400" />
                    <dd className="text-sm text-gray-900 dark:text-white">
                      {formatDate(proxy.metadata.creationTimestamp)}
                    </dd>
                  </div>
                </dl>
              </CardContent>
            </Card>

            {/* Hostnames */}
            <Card>
              <CardHeader>
                <CardTitle>Hostnames</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="space-y-2">
                  {proxy.spec.hostnames?.map((hostname) => (
                    <div
                      key={hostname}
                      className="flex items-center justify-between p-3 bg-gray-50 dark:bg-dark-800 rounded-lg"
                    >
                      <span className="text-sm font-medium text-gray-900 dark:text-white">{hostname}</span>
                      <a
                        href={`https://${hostname}`}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="p-1.5 text-gray-400 hover:text-gray-600 dark:hover:text-dark-200"
                      >
                        <ExternalLink className="w-4 h-4" />
                      </a>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>

            {/* Labels */}
            {proxy.metadata.labels && Object.keys(proxy.metadata.labels).length > 0 && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Tag className="w-4 h-4" />
                    Labels
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="flex flex-wrap gap-2">
                    {Object.entries(proxy.metadata.labels).map(([key, value]) => (
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

            {/* Addresses */}
            {proxy.status?.addresses && proxy.status.addresses.length > 0 && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Network className="w-4 h-4" />
                    Addresses
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="space-y-2">
                    {proxy.status.addresses.map((addr, idx) => (
                      <div
                        key={idx}
                        className="flex items-center justify-between p-3 bg-gray-50 dark:bg-dark-800 rounded-lg"
                      >
                        <div>
                          <span className="text-xs text-gray-500 dark:text-dark-400 uppercase">{addr.type}</span>
                          <p className="text-sm font-mono text-gray-900 dark:text-white">{addr.value}</p>
                        </div>
                      </div>
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
                  {proxy.status?.conditions?.map((condition, idx) => (
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

        <TabPanel id="routing">
          <Card>
            <CardHeader>
              <CardTitle>Routing Rules</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="space-y-4">
                {proxy.spec.rules?.map((rule, idx) => (
                  <div
                    key={idx}
                    className="p-4 border border-gray-200 dark:border-dark-700 rounded-lg"
                  >
                    <div className="flex items-center justify-between mb-4">
                      <h4 className="text-sm font-medium text-gray-900 dark:text-white">Rule {idx + 1}</h4>
                    </div>

                    {/* Matches */}
                    {rule.matches && rule.matches.length > 0 && (
                      <div className="mb-4">
                        <h5 className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-2">Matches</h5>
                        <div className="space-y-2">
                          {rule.matches.map((match, midx) => (
                            <div key={midx} className="flex flex-wrap gap-2">
                              {match.path && (
                                <span className="inline-flex items-center px-2 py-1 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded text-xs">
                                  {match.path.type}: {match.path.value}
                                </span>
                              )}
                              {match.method && (
                                <span className="inline-flex items-center px-2 py-1 bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-300 rounded text-xs">
                                  {match.method}
                                </span>
                              )}
                            </div>
                          ))}
                        </div>
                      </div>
                    )}

                    {/* Backends */}
                    {rule.backends && rule.backends.length > 0 && (
                      <div>
                        <h5 className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-2">Backends</h5>
                        <div className="space-y-2">
                          {rule.backends.map((backend, bidx) => (
                            <div
                              key={bidx}
                              className="flex items-center justify-between p-2 bg-gray-50 dark:bg-dark-800 rounded"
                            >
                              <div className="flex items-center gap-2">
                                <span className="text-sm font-mono text-gray-700 dark:text-dark-200">
                                  {backend.endpoint}
                                </span>
                                {backend.connectorRef && (
                                  <span className="text-xs text-gray-500 dark:text-dark-400">
                                    via {backend.connectorRef.name}
                                  </span>
                                )}
                              </div>
                              {backend.weight !== undefined && (
                                <span className="text-xs text-gray-500 dark:text-dark-400">
                                  weight: {backend.weight}
                                </span>
                              )}
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        </TabPanel>

        <TabPanel id="related">
          <RelatedResourcesPanel
            relatedResources={relatedResources}
            isLoading={relatedLoading}
            namespace={namespace}
          />
        </TabPanel>

        <TabPanel id="yaml">
          <YamlViewer data={proxy} title={`${proxy.metadata.name}.yaml`} />
        </TabPanel>
      </Tabs>

      {/* Delete Modal */}
      <Modal
        isOpen={showDeleteModal}
        onClose={() => setShowDeleteModal(false)}
        title="Delete Gateway"
        size="sm"
      >
        <div className="space-y-4">
          <p className="text-sm text-gray-600 dark:text-dark-300">
            Are you sure you want to delete the gateway{' '}
            <span className="font-medium text-gray-900 dark:text-white">{proxy.metadata.name}</span>?
            This action cannot be undone.
          </p>
          <div className="flex justify-end gap-3">
            <Button variant="ghost" onClick={() => setShowDeleteModal(false)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={deleteMutation.isPending}
              onClick={() => deleteMutation.mutate()}
            >
              Delete
            </Button>
          </div>
        </div>
      </Modal>

      {/* Test Modal */}
      <TestProxyModal
        isOpen={showTestModal}
        onClose={() => setShowTestModal(false)}
        proxy={proxy}
      />
    </div>
  );
}

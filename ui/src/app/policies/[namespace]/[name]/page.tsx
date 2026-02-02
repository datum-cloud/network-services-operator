'use client';

import { useParams } from 'next/navigation';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, Shield, Eye, Ban, Target, FileCode } from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/common/Card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Tabs, TabPanel } from '@/components/common/Tabs';
import { YamlViewer } from '@/components/common/YamlViewer';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
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
  return date.toLocaleString();
}

export default function PolicyDetailPage() {
  const params = useParams();
  const namespace = params.namespace as string;
  const name = params.name as string;

  const { data: policy, isLoading, error, refetch } = useQuery({
    queryKey: ['policy', namespace, name],
    queryFn: () => apiClient.getPolicy(name, namespace),
    enabled: !!namespace && !!name,
  });

  if (isLoading) {
    return <LoadingState message="Loading policy..." />;
  }

  if (error || !policy) {
    return (
      <ErrorState
        title="Policy not found"
        message="The policy you're looking for doesn't exist or has been deleted."
        onRetry={() => refetch()}
      />
    );
  }

  const ModeIcon = getPolicyModeIcon(policy.spec.mode);
  const tabs = [
    { id: 'overview', label: 'Overview' },
    { id: 'rules', label: 'Rule Sets' },
    { id: 'yaml', label: 'YAML' },
  ];

  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Security Policies', href: '/policies' },
          { name: policy.metadata.name },
        ]}
      />

      {/* Header */}
      <div className="flex items-start gap-4">
        <Link
          href="/policies"
          className="mt-1 p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-dark-800 transition-colors"
        >
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <div>
          <div className="flex items-center gap-3">
            <Shield className="w-6 h-6 text-gray-400" />
            <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
              {policy.metadata.name}
            </h1>
            <StatusBadge status={getPolicyModeStatus(policy.spec.mode)}>
              <ModeIcon className="w-3 h-3 mr-1" />
              {policy.spec.mode}
            </StatusBadge>
          </div>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Namespace: {policy.metadata.namespace}
          </p>
        </div>
      </div>

      {/* Tabs */}
      <Tabs tabs={tabs} defaultTab="overview">
        <TabPanel id="overview">
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            {/* Policy Details */}
            <Card>
              <CardHeader>
                <CardTitle>Policy Details</CardTitle>
              </CardHeader>
              <CardContent>
                <dl className="space-y-4">
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Name</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{policy.metadata.name}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Namespace</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{policy.metadata.namespace}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Mode</dt>
                    <dd className="mt-1">
                      <StatusBadge status={getPolicyModeStatus(policy.spec.mode)}>
                        <ModeIcon className="w-3 h-3 mr-1" />
                        {policy.spec.mode}
                      </StatusBadge>
                    </dd>
                  </div>
                  {policy.spec.samplingPercentage !== undefined && (
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Sampling</dt>
                      <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                        {policy.spec.samplingPercentage}%
                      </dd>
                    </div>
                  )}
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Created</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                      {formatDate(policy.metadata.creationTimestamp)}
                    </dd>
                  </div>
                </dl>
              </CardContent>
            </Card>

            {/* Target Refs */}
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <Target className="w-4 h-4" />
                  Targets
                </CardTitle>
              </CardHeader>
              <CardContent>
                {policy.spec.targetRefs && policy.spec.targetRefs.length > 0 ? (
                  <div className="space-y-2">
                    {policy.spec.targetRefs.map((ref, idx) => (
                      <div
                        key={idx}
                        className="flex items-center justify-between p-3 bg-gray-50 dark:bg-dark-800 rounded-lg"
                      >
                        <div>
                          <span className="text-xs text-gray-500 dark:text-dark-400 uppercase">{ref.kind}</span>
                          <p className="text-sm font-medium text-gray-900 dark:text-white">{ref.name}</p>
                        </div>
                        <Link
                          href={`/gateways/${policy.metadata.namespace}/${ref.name}`}
                          className="text-sm text-primary-600 dark:text-primary-400 hover:underline"
                        >
                          View
                        </Link>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="text-sm text-gray-500 dark:text-dark-400">No targets configured</p>
                )}
              </CardContent>
            </Card>

            {/* Conditions */}
            <Card className="lg:col-span-2">
              <CardHeader>
                <CardTitle>Conditions</CardTitle>
              </CardHeader>
              <CardContent>
                <div className="space-y-3">
                  {policy.status?.conditions?.map((condition, idx) => (
                    <div
                      key={idx}
                      className={`p-4 rounded-lg ${
                        condition.status === 'True'
                          ? 'bg-green-50 dark:bg-green-900/20'
                          : 'bg-yellow-50 dark:bg-yellow-900/20'
                      }`}
                    >
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                          <StatusBadge status={condition.status === 'True' ? 'success' : 'warning'}>
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

        <TabPanel id="rules">
          <div className="space-y-6">
            {policy.spec.ruleSets?.map((ruleSet, idx) => (
              <Card key={idx}>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <FileCode className="w-4 h-4" />
                    {ruleSet.name}
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-4">
                    {ruleSet.paranoiaLevel !== undefined && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Paranoia Level</dt>
                        <dd className="mt-1">
                          <span className="inline-flex items-center px-2 py-1 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded text-sm">
                            Level {ruleSet.paranoiaLevel}
                          </span>
                        </dd>
                      </div>
                    )}
                    {ruleSet.scoreThreshold !== undefined && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Score Threshold</dt>
                        <dd className="mt-1 text-sm text-gray-900 dark:text-white">{ruleSet.scoreThreshold}</dd>
                      </div>
                    )}
                    {ruleSet.ruleExclusions && ruleSet.ruleExclusions.length > 0 && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400 mb-2">Rule Exclusions</dt>
                        <dd>
                          <div className="space-y-2">
                            {ruleSet.ruleExclusions.map((exclusion, eidx) => (
                              <div
                                key={eidx}
                                className="p-3 bg-yellow-50 dark:bg-yellow-900/20 rounded-lg"
                              >
                                <div className="flex items-center gap-2">
                                  <span className="font-mono text-sm text-yellow-700 dark:text-yellow-300">
                                    {exclusion.ruleId}
                                  </span>
                                </div>
                                {exclusion.reason && (
                                  <p className="mt-1 text-sm text-gray-600 dark:text-dark-300">{exclusion.reason}</p>
                                )}
                              </div>
                            ))}
                          </div>
                        </dd>
                      </div>
                    )}
                  </dl>
                </CardContent>
              </Card>
            ))}
          </div>
        </TabPanel>

        <TabPanel id="yaml">
          <YamlViewer data={policy} title={`${policy.metadata.name}.yaml`} />
        </TabPanel>
      </Tabs>
    </div>
  );
}

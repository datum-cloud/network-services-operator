'use client';

import { useParams } from 'next/navigation';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, Globe, Copy, Check, Shield, Server } from 'lucide-react';
import { useState } from 'react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/common/Card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Tabs, TabPanel } from '@/components/common/Tabs';
import { YamlViewer } from '@/components/common/YamlViewer';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
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
  return date.toLocaleString();
}

function CopyableValue({ value, label }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="flex items-center gap-2">
      <code className="flex-1 px-3 py-2 bg-gray-100 dark:bg-dark-800 rounded text-sm font-mono text-gray-800 dark:text-dark-200 break-all">
        {value}
      </code>
      <button
        onClick={handleCopy}
        className="p-2 text-gray-400 hover:text-gray-600 dark:hover:text-dark-200 transition-colors"
        title="Copy to clipboard"
      >
        {copied ? <Check className="w-4 h-4 text-green-500" /> : <Copy className="w-4 h-4" />}
      </button>
    </div>
  );
}

export default function DomainDetailPage() {
  const params = useParams();
  const namespace = params.namespace as string;
  const name = params.name as string;

  const { data: domain, isLoading, error, refetch } = useQuery({
    queryKey: ['domain', namespace, name],
    queryFn: () => apiClient.getDomain(name, namespace),
    enabled: !!namespace && !!name,
  });

  if (isLoading) {
    return <LoadingState message="Loading domain..." />;
  }

  if (error || !domain) {
    return (
      <ErrorState
        title="Domain not found"
        message="The domain you're looking for doesn't exist or has been deleted."
        onRetry={() => refetch()}
      />
    );
  }

  const tabs = [
    { id: 'overview', label: 'Overview' },
    { id: 'verification', label: 'Verification' },
    { id: 'yaml', label: 'YAML' },
  ];

  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Domains', href: '/domains' },
          { name: domain.spec.domainName },
        ]}
      />

      {/* Header */}
      <div className="flex items-start gap-4">
        <Link
          href="/domains"
          className="mt-1 p-2 rounded-lg hover:bg-gray-100 dark:hover:bg-dark-800 transition-colors"
        >
          <ArrowLeft className="w-5 h-5" />
        </Link>
        <div>
          <div className="flex items-center gap-3">
            <Globe className="w-6 h-6 text-gray-400" />
            <h1 className="text-2xl font-bold text-gray-900 dark:text-white">
              {domain.spec.domainName}
            </h1>
            <StatusBadge status={getDomainStatus(domain)}>
              {getDomainStatusText(domain)}
            </StatusBadge>
          </div>
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
            Resource: {domain.metadata.name} | Namespace: {domain.metadata.namespace}
          </p>
        </div>
      </div>

      {/* Tabs */}
      <Tabs tabs={tabs} defaultTab="overview">
        <TabPanel id="overview">
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            {/* Domain Details */}
            <Card>
              <CardHeader>
                <CardTitle>Domain Details</CardTitle>
              </CardHeader>
              <CardContent>
                <dl className="space-y-4">
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Domain Name</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{domain.spec.domainName}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Resource Name</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{domain.metadata.name}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Namespace</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">{domain.metadata.namespace}</dd>
                  </div>
                  <div>
                    <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Created</dt>
                    <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                      {formatDate(domain.metadata.creationTimestamp)}
                    </dd>
                  </div>
                </dl>
              </CardContent>
            </Card>

            {/* Registration Info */}
            {domain.status?.registration && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Server className="w-4 h-4" />
                    Registration
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-4">
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Registrar</dt>
                      <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                        {domain.status.registration.registrar}
                      </dd>
                    </div>
                    {domain.status.registration.expirationDate && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Expires</dt>
                        <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                          {formatDate(domain.status.registration.expirationDate)}
                        </dd>
                      </div>
                    )}
                    {domain.status.registration.nameServers && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Name Servers</dt>
                        <dd className="mt-1 space-y-1">
                          {domain.status.registration.nameServers.map((ns) => (
                            <div key={ns} className="text-sm font-mono text-gray-600 dark:text-dark-300">
                              {ns}
                            </div>
                          ))}
                        </dd>
                      </div>
                    )}
                  </dl>
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
                  {domain.status?.conditions?.map((condition, idx) => (
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

        <TabPanel id="verification">
          <div className="space-y-6">
            {/* DNS Verification */}
            {domain.status?.verification?.dns && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Shield className="w-4 h-4" />
                    DNS Verification
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <p className="text-sm text-gray-600 dark:text-dark-300 mb-4">
                    Add the following DNS TXT record to verify ownership of this domain:
                  </p>
                  <dl className="space-y-4">
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Record Type</dt>
                      <dd className="mt-1">
                        <span className="inline-block px-2 py-1 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded text-sm font-medium">
                          {domain.status.verification.dns.recordType}
                        </span>
                      </dd>
                    </div>
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Record Name</dt>
                      <dd className="mt-1">
                        <CopyableValue value={domain.status.verification.dns.recordName} />
                      </dd>
                    </div>
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Record Value</dt>
                      <dd className="mt-1">
                        <CopyableValue value={domain.status.verification.dns.recordValue} />
                      </dd>
                    </div>
                  </dl>
                </CardContent>
              </Card>
            )}

            {/* HTTP Verification */}
            {domain.status?.verification?.http && (
              <Card>
                <CardHeader>
                  <CardTitle>HTTP Verification (Alternative)</CardTitle>
                </CardHeader>
                <CardContent>
                  <p className="text-sm text-gray-600 dark:text-dark-300 mb-4">
                    Alternatively, you can verify by hosting a file at the following path:
                  </p>
                  <dl className="space-y-4">
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">File Path</dt>
                      <dd className="mt-1">
                        <CopyableValue value={domain.status.verification.http.path} />
                      </dd>
                    </div>
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">File Content</dt>
                      <dd className="mt-1">
                        <CopyableValue value={domain.status.verification.http.content} />
                      </dd>
                    </div>
                  </dl>
                </CardContent>
              </Card>
            )}
          </div>
        </TabPanel>

        <TabPanel id="yaml">
          <YamlViewer data={domain} title={`${domain.metadata.name}.yaml`} />
        </TabPanel>
      </Tabs>
    </div>
  );
}

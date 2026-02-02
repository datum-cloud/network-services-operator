'use client';

import { useParams } from 'next/navigation';
import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';
import {
  ArrowLeft,
  Shield,
  Target,
  Key,
  FileKey,
  Globe,
  Lock,
  Scale,
  Edit,
  Trash2,
} from 'lucide-react';
import { Breadcrumb } from '@/components/layout/Breadcrumb';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/common/Card';
import { StatusBadge } from '@/components/common/StatusBadge';
import { Tabs, TabPanel } from '@/components/common/Tabs';
import { YamlViewer } from '@/components/common/YamlViewer';
import { Button } from '@/components/common/Button';
import { LoadingState } from '@/components/common/LoadingState';
import { ErrorState } from '@/components/common/ErrorState';
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

function formatDate(timestamp?: string): string {
  if (!timestamp) return '-';
  const date = new Date(timestamp);
  return date.toLocaleString();
}

export default function SecurityPolicyDetailPage() {
  const params = useParams();
  const namespace = params.namespace as string;
  const name = params.name as string;

  const { data: policy, isLoading, error, refetch } = useQuery({
    queryKey: ['securityPolicy', namespace, name],
    queryFn: () => apiClient.getSecurityPolicy(name, namespace),
    enabled: !!namespace && !!name,
  });

  if (isLoading) {
    return <LoadingState message="Loading security policy..." />;
  }

  if (error || !policy) {
    return (
      <ErrorState
        title="Security policy not found"
        message="The security policy you're looking for doesn't exist or has been deleted."
        onRetry={() => refetch()}
      />
    );
  }

  const status = getStatusFromConditions(policy);
  const tabs = [
    { id: 'overview', label: 'Overview' },
    { id: 'authentication', label: 'Authentication' },
    { id: 'yaml', label: 'YAML' },
  ];

  return (
    <div className="space-y-6">
      <Breadcrumb
        items={[
          { name: 'Security Policies', href: '/security-policies' },
          { name: policy.metadata.name },
        ]}
      />

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="flex items-start gap-4">
          <Link
            href="/security-policies"
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
              <StatusBadge status={status}>
                {policy.status?.conditions?.find(c => c.type === 'Programmed')?.reason || 'Unknown'}
              </StatusBadge>
            </div>
            <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">
              Namespace: {policy.metadata.namespace}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Link href={`/security-policies/${namespace}/${name}/edit`}>
            <Button variant="secondary" icon={<Edit className="w-4 h-4" />}>
              Edit
            </Button>
          </Link>
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
                          {ref.sectionName && (
                            <p className="text-xs text-gray-500">{ref.sectionName}</p>
                          )}
                        </div>
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
                {policy.status?.conditions && policy.status.conditions.length > 0 ? (
                  <div className="space-y-3">
                    {policy.status.conditions.map((condition, idx) => (
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
                ) : (
                  <p className="text-sm text-gray-500 dark:text-dark-400">No conditions reported</p>
                )}
              </CardContent>
            </Card>
          </div>
        </TabPanel>

        <TabPanel id="authentication">
          <div className="space-y-6">
            {/* Basic Auth */}
            {policy.spec.basicAuth && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Key className="w-4 h-4" />
                    Basic Authentication
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-3">
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Users Secret</dt>
                      <dd className="mt-1 text-sm font-mono text-gray-900 dark:text-white">
                        {policy.spec.basicAuth.users.name}
                      </dd>
                    </div>
                    {policy.spec.basicAuth.forwardUsernameHeader && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Forward Username Header</dt>
                        <dd className="mt-1 text-sm font-mono text-gray-900 dark:text-white">
                          {policy.spec.basicAuth.forwardUsernameHeader}
                        </dd>
                      </div>
                    )}
                  </dl>
                </CardContent>
              </Card>
            )}

            {/* API Key Auth */}
            {policy.spec.apiKeyAuth && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <FileKey className="w-4 h-4" />
                    API Key Authentication
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-3">
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Credential Secrets</dt>
                      <dd className="mt-1 flex flex-wrap gap-2">
                        {policy.spec.apiKeyAuth.credentialRefs.map((ref, i) => (
                          <span key={i} className="text-sm font-mono px-2 py-1 bg-gray-100 dark:bg-dark-800 rounded">
                            {ref.name}
                          </span>
                        ))}
                      </dd>
                    </div>
                    {policy.spec.apiKeyAuth.extractFrom && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Extract From</dt>
                        <dd className="mt-1 space-y-2">
                          {policy.spec.apiKeyAuth.extractFrom.map((e, i) => (
                            <div key={i} className="text-sm text-gray-900 dark:text-white">
                              {e.headers?.length ? `Headers: ${e.headers.join(', ')}` : null}
                              {e.params?.length ? ` | Params: ${e.params.join(', ')}` : null}
                              {e.cookies?.length ? ` | Cookies: ${e.cookies.join(', ')}` : null}
                            </div>
                          ))}
                        </dd>
                      </div>
                    )}
                  </dl>
                </CardContent>
              </Card>
            )}

            {/* JWT */}
            {policy.spec.jwt && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Shield className="w-4 h-4" />
                    JWT Authentication
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <div className="space-y-4">
                    {policy.spec.jwt.optional && (
                      <StatusBadge status="info">Optional</StatusBadge>
                    )}
                    {policy.spec.jwt.providers.map((provider, i) => (
                      <div key={i} className="p-4 bg-gray-50 dark:bg-dark-800 rounded-lg">
                        <h5 className="font-medium text-gray-900 dark:text-white mb-2">
                          {provider.name}
                        </h5>
                        <dl className="space-y-2 text-sm">
                          {provider.issuer && (
                            <div>
                              <dt className="text-gray-500 dark:text-dark-400">Issuer</dt>
                              <dd className="font-mono text-gray-900 dark:text-white">{provider.issuer}</dd>
                            </div>
                          )}
                          {provider.audiences && provider.audiences.length > 0 && (
                            <div>
                              <dt className="text-gray-500 dark:text-dark-400">Audiences</dt>
                              <dd className="text-gray-900 dark:text-white">{provider.audiences.join(', ')}</dd>
                            </div>
                          )}
                          {provider.remoteJWKS && (
                            <div>
                              <dt className="text-gray-500 dark:text-dark-400">JWKS URI</dt>
                              <dd className="font-mono text-gray-900 dark:text-white break-all">{provider.remoteJWKS.uri}</dd>
                            </div>
                          )}
                        </dl>
                      </div>
                    ))}
                  </div>
                </CardContent>
              </Card>
            )}

            {/* OIDC */}
            {policy.spec.oidc && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Globe className="w-4 h-4" />
                    OIDC Authentication
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-3">
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Issuer</dt>
                      <dd className="mt-1 text-sm font-mono text-gray-900 dark:text-white">
                        {policy.spec.oidc.provider.issuer}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Client ID</dt>
                      <dd className="mt-1 text-sm font-mono text-gray-900 dark:text-white">
                        {policy.spec.oidc.clientID}
                      </dd>
                    </div>
                    {policy.spec.oidc.scopes && policy.spec.oidc.scopes.length > 0 && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Scopes</dt>
                        <dd className="mt-1 flex flex-wrap gap-1">
                          {policy.spec.oidc.scopes.map((scope, i) => (
                            <span key={i} className="text-xs px-2 py-1 bg-gray-100 dark:bg-dark-800 rounded">
                              {scope}
                            </span>
                          ))}
                        </dd>
                      </div>
                    )}
                  </dl>
                </CardContent>
              </Card>
            )}

            {/* CORS */}
            {policy.spec.cors && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Lock className="w-4 h-4" />
                    CORS
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-3">
                    {policy.spec.cors.allowOrigins && policy.spec.cors.allowOrigins.length > 0 && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Allowed Origins</dt>
                        <dd className="mt-1 space-y-1">
                          {policy.spec.cors.allowOrigins.map((origin, i) => (
                            <div key={i} className="text-sm text-gray-900 dark:text-white">
                              <span className="text-xs text-gray-500 mr-2">{origin.type}:</span>
                              {origin.value}
                            </div>
                          ))}
                        </dd>
                      </div>
                    )}
                    {policy.spec.cors.allowMethods && policy.spec.cors.allowMethods.length > 0 && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Allowed Methods</dt>
                        <dd className="mt-1 flex flex-wrap gap-1">
                          {policy.spec.cors.allowMethods.map((method, i) => (
                            <span key={i} className="text-xs px-2 py-1 bg-gray-100 dark:bg-dark-800 rounded">
                              {method}
                            </span>
                          ))}
                        </dd>
                      </div>
                    )}
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Allow Credentials</dt>
                      <dd className="mt-1 text-sm text-gray-900 dark:text-white">
                        {policy.spec.cors.allowCredentials ? 'Yes' : 'No'}
                      </dd>
                    </div>
                  </dl>
                </CardContent>
              </Card>
            )}

            {/* Authorization */}
            {policy.spec.authorization && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2">
                    <Scale className="w-4 h-4" />
                    Authorization
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-4">
                    <div>
                      <dt className="text-sm font-medium text-gray-500 dark:text-dark-400">Default Action</dt>
                      <dd className="mt-1">
                        <StatusBadge status={policy.spec.authorization.defaultAction === 'Allow' ? 'success' : 'error'}>
                          {policy.spec.authorization.defaultAction}
                        </StatusBadge>
                      </dd>
                    </div>
                    {policy.spec.authorization.rules && policy.spec.authorization.rules.length > 0 && (
                      <div>
                        <dt className="text-sm font-medium text-gray-500 dark:text-dark-400 mb-2">Rules</dt>
                        <dd className="space-y-2">
                          {policy.spec.authorization.rules.map((rule, i) => (
                            <div
                              key={i}
                              className={`p-3 rounded-lg ${
                                rule.action === 'Allow'
                                  ? 'bg-green-50 dark:bg-green-900/20'
                                  : 'bg-red-50 dark:bg-red-900/20'
                              }`}
                            >
                              <div className="flex items-center gap-2 mb-2">
                                <StatusBadge status={rule.action === 'Allow' ? 'success' : 'error'}>
                                  {rule.action}
                                </StatusBadge>
                                {rule.name && (
                                  <span className="text-sm font-medium text-gray-900 dark:text-white">
                                    {rule.name}
                                  </span>
                                )}
                              </div>
                              {rule.principal.clientCIDRs && rule.principal.clientCIDRs.length > 0 && (
                                <div className="text-sm text-gray-600 dark:text-dark-300">
                                  CIDRs: {rule.principal.clientCIDRs.map(c => c.cidr).join(', ')}
                                </div>
                              )}
                            </div>
                          ))}
                        </dd>
                      </div>
                    )}
                  </dl>
                </CardContent>
              </Card>
            )}

            {/* No Auth Configured */}
            {!policy.spec.basicAuth && !policy.spec.apiKeyAuth && !policy.spec.jwt &&
             !policy.spec.oidc && !policy.spec.cors && !policy.spec.authorization && (
              <Card>
                <CardContent className="py-8 text-center">
                  <Shield className="w-12 h-12 mx-auto text-gray-400 dark:text-dark-500 mb-3" />
                  <p className="text-gray-500 dark:text-dark-400">
                    No authentication or security features configured
                  </p>
                </CardContent>
              </Card>
            )}
          </div>
        </TabPanel>

        <TabPanel id="yaml">
          <YamlViewer data={policy} title={`${policy.metadata.name}.yaml`} />
        </TabPanel>
      </Tabs>
    </div>
  );
}

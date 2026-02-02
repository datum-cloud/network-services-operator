'use client';

import { AlertCircle, Check, Target, Shield, Key, FileKey, Globe, Lock, Scale } from 'lucide-react';
import { YamlViewer } from '@/components/common/YamlViewer';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/common/Card';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';
import { AUTH_TYPE_LABELS } from '@/lib/security-policy-defaults';

export function ReviewStep() {
  const {
    name,
    namespace,
    targetRefs,
    basicAuth,
    apiKeyAuth,
    jwt,
    oidc,
    cors,
    authorization,
    errors,
    toSecurityPolicy,
  } = useSecurityPolicyForm();

  const policy = toSecurityPolicy();
  const hasErrors = errors.length > 0;

  const getAuthTypeIcon = (type: string) => {
    switch (type) {
      case 'basicAuth': return Key;
      case 'apiKeyAuth': return FileKey;
      case 'jwt': return Shield;
      case 'oidc': return Globe;
      case 'cors': return Lock;
      case 'authorization': return Scale;
      default: return Shield;
    }
  };

  const enabledAuthTypes = [
    basicAuth && 'basicAuth',
    apiKeyAuth && 'apiKeyAuth',
    jwt && 'jwt',
    oidc && 'oidc',
    cors && 'cors',
    authorization && 'authorization',
  ].filter(Boolean) as string[];

  return (
    <div className="space-y-8">
      {/* Validation Errors */}
      {hasErrors && (
        <div className="p-4 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-800 rounded-lg">
          <div className="flex items-start gap-3">
            <AlertCircle className="w-5 h-5 text-red-500 flex-shrink-0 mt-0.5" />
            <div>
              <h4 className="text-sm font-medium text-red-800 dark:text-red-200">
                Please fix the following errors before creating the policy:
              </h4>
              <ul className="mt-2 space-y-1">
                {errors.slice(0, 5).map((error, index) => (
                  <li key={index} className="text-sm text-red-600 dark:text-red-300">
                    {error.field}: {error.message}
                  </li>
                ))}
                {errors.length > 5 && (
                  <li className="text-sm text-red-600 dark:text-red-300">
                    ...and {errors.length - 5} more errors
                  </li>
                )}
              </ul>
            </div>
          </div>
        </div>
      )}

      {/* Summary Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {/* Policy Info */}
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Policy Information</CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="space-y-3">
              <div>
                <dt className="text-sm text-gray-500 dark:text-dark-400">Name</dt>
                <dd className="text-sm font-medium text-gray-900 dark:text-white">{name || '-'}</dd>
              </div>
              <div>
                <dt className="text-sm text-gray-500 dark:text-dark-400">Namespace</dt>
                <dd className="text-sm font-medium text-gray-900 dark:text-white">{namespace}</dd>
              </div>
            </dl>
          </CardContent>
        </Card>

        {/* Targets */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Target className="w-4 h-4" />
              Targets
            </CardTitle>
          </CardHeader>
          <CardContent>
            {targetRefs.length === 0 ? (
              <p className="text-sm text-gray-500 dark:text-dark-400">No targets configured</p>
            ) : (
              <div className="space-y-2">
                {targetRefs.map((ref) => (
                  <div
                    key={ref.id}
                    className="flex items-center gap-2 p-2 bg-gray-50 dark:bg-dark-800 rounded"
                  >
                    <span className="text-xs px-2 py-0.5 bg-primary-100 dark:bg-primary-900/30 text-primary-700 dark:text-primary-400 rounded">
                      {ref.kind}
                    </span>
                    <span className="text-sm font-medium text-gray-900 dark:text-white">
                      {ref.name || 'unnamed'}
                    </span>
                    {ref.sectionName && (
                      <span className="text-xs text-gray-500 dark:text-dark-400">
                        ({ref.sectionName})
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Enabled Auth Types */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Authentication & Security</CardTitle>
        </CardHeader>
        <CardContent>
          {enabledAuthTypes.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-dark-400">
              No authentication or security features enabled
            </p>
          ) : (
            <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
              {enabledAuthTypes.map((type) => {
                const Icon = getAuthTypeIcon(type);
                return (
                  <div
                    key={type}
                    className="flex items-center gap-2 p-3 bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 rounded-lg"
                  >
                    <Check className="w-4 h-4 text-green-600 dark:text-green-400" />
                    <Icon className="w-4 h-4 text-green-600 dark:text-green-400" />
                    <span className="text-sm font-medium text-green-700 dark:text-green-300">
                      {AUTH_TYPE_LABELS[type]}
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Auth Type Details */}
      {basicAuth && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Key className="w-4 h-4" />
              Basic Auth Configuration
            </CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="space-y-2">
              <div>
                <dt className="text-sm text-gray-500 dark:text-dark-400">Users Secret</dt>
                <dd className="text-sm font-mono text-gray-900 dark:text-white">
                  {basicAuth.users.name || '-'}
                </dd>
              </div>
              {basicAuth.forwardUsernameHeader && (
                <div>
                  <dt className="text-sm text-gray-500 dark:text-dark-400">Forward Username Header</dt>
                  <dd className="text-sm font-mono text-gray-900 dark:text-white">
                    {basicAuth.forwardUsernameHeader}
                  </dd>
                </div>
              )}
            </dl>
          </CardContent>
        </Card>
      )}

      {jwt && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Shield className="w-4 h-4" />
              JWT Configuration
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              <div className="text-sm">
                <span className="text-gray-500 dark:text-dark-400">Providers: </span>
                <span className="font-medium text-gray-900 dark:text-white">
                  {jwt.providers.length}
                </span>
              </div>
              {jwt.providers.map((provider, index) => (
                <div
                  key={provider.id}
                  className="p-3 bg-gray-50 dark:bg-dark-800 rounded-lg text-sm"
                >
                  <div className="font-medium text-gray-900 dark:text-white">
                    {provider.name || `Provider ${index + 1}`}
                  </div>
                  {provider.issuer && (
                    <div className="text-gray-500 dark:text-dark-400 truncate">
                      Issuer: {provider.issuer}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {cors && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Lock className="w-4 h-4" />
              CORS Configuration
            </CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="space-y-2">
              <div>
                <dt className="text-sm text-gray-500 dark:text-dark-400">Allowed Origins</dt>
                <dd className="text-sm text-gray-900 dark:text-white">
                  {cors.allowOrigins.length || 'None'}
                </dd>
              </div>
              <div>
                <dt className="text-sm text-gray-500 dark:text-dark-400">Allowed Methods</dt>
                <dd className="text-sm text-gray-900 dark:text-white">
                  {cors.allowMethods.join(', ') || 'None'}
                </dd>
              </div>
              <div>
                <dt className="text-sm text-gray-500 dark:text-dark-400">Allow Credentials</dt>
                <dd className="text-sm text-gray-900 dark:text-white">
                  {cors.allowCredentials ? 'Yes' : 'No'}
                </dd>
              </div>
            </dl>
          </CardContent>
        </Card>
      )}

      {authorization && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Scale className="w-4 h-4" />
              Authorization Configuration
            </CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="space-y-2">
              <div>
                <dt className="text-sm text-gray-500 dark:text-dark-400">Default Action</dt>
                <dd className={`text-sm font-medium ${
                  authorization.defaultAction === 'Allow'
                    ? 'text-green-600 dark:text-green-400'
                    : 'text-red-600 dark:text-red-400'
                }`}>
                  {authorization.defaultAction}
                </dd>
              </div>
              <div>
                <dt className="text-sm text-gray-500 dark:text-dark-400">Rules</dt>
                <dd className="text-sm text-gray-900 dark:text-white">
                  {authorization.rules.length}
                </dd>
              </div>
            </dl>
          </CardContent>
        </Card>
      )}

      {/* YAML Preview */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">YAML Preview</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <YamlViewer data={policy} title={`${name || 'security-policy'}.yaml`} />
        </CardContent>
      </Card>
    </div>
  );
}

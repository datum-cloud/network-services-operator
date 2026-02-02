'use client';

import { useState } from 'react';
import { CheckCircle, AlertCircle, Globe, Route, Server, Filter, Code } from 'lucide-react';
import { Card, CardHeader, CardTitle, CardContent } from '@/components/common/Card';
import { YamlViewer } from '@/components/common/YamlViewer';
import { StatusBadge } from '@/components/common/StatusBadge';
import { useGatewayForm } from '@/hooks/useGatewayForm';

interface ReviewStepProps {
  isEdit?: boolean;
}

export function ReviewStep({ isEdit = false }: ReviewStepProps) {
  const [showYaml, setShowYaml] = useState(false);
  const {
    name,
    namespace,
    hostnames,
    rules,
    errors,
    toHTTPProxy,
    getRuleSummary,
    getFilterSummary,
  } = useGatewayForm();

  const proxy = toHTTPProxy();
  const hasErrors = errors.length > 0;

  // Count totals
  const totalMatches = rules.reduce((acc, rule) => acc + rule.matches.length, 0);
  const totalFilters = rules.reduce((acc, rule) => acc + rule.filters.length, 0);
  const totalBackends = rules.reduce((acc, rule) => acc + rule.backends.length, 0);

  return (
    <div className="space-y-6">
      {/* Validation Status */}
      <div
        className={`p-4 rounded-lg flex items-start gap-3 ${
          hasErrors
            ? 'bg-red-50 dark:bg-red-900/20'
            : 'bg-green-50 dark:bg-green-900/20'
        }`}
      >
        {hasErrors ? (
          <AlertCircle className="w-5 h-5 text-red-500 flex-shrink-0 mt-0.5" />
        ) : (
          <CheckCircle className="w-5 h-5 text-green-500 flex-shrink-0 mt-0.5" />
        )}
        <div>
          <h3
            className={`font-medium ${
              hasErrors
                ? 'text-red-700 dark:text-red-300'
                : 'text-green-700 dark:text-green-300'
            }`}
          >
            {hasErrors ? 'Validation Issues' : 'Ready to Submit'}
          </h3>
          {hasErrors ? (
            <ul className="mt-2 text-sm text-red-600 dark:text-red-400 list-disc list-inside">
              {errors.map((error, idx) => (
                <li key={idx}>
                  <span className="font-mono text-xs">{error.field}</span>: {error.message}
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-green-600 dark:text-green-400">
              All configuration is valid. Click {isEdit ? 'Update' : 'Create'} to save your gateway.
            </p>
          )}
        </div>
      </div>

      {/* Summary Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {/* Basic Info */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Globe className="w-4 h-4" />
              Basic Information
            </CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="space-y-3">
              <div>
                <dt className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase">Name</dt>
                <dd className="text-sm text-gray-900 dark:text-white font-mono">{name || '-'}</dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase">Namespace</dt>
                <dd className="text-sm text-gray-900 dark:text-white font-mono">{namespace}</dd>
              </div>
              <div>
                <dt className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase">
                  Hostnames ({hostnames.length})
                </dt>
                <dd className="mt-1 flex flex-wrap gap-1">
                  {hostnames.length > 0 ? (
                    hostnames.map((hostname) => (
                      <span
                        key={hostname}
                        className="text-xs px-2 py-0.5 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded font-mono"
                      >
                        {hostname}
                      </span>
                    ))
                  ) : (
                    <span className="text-sm text-gray-400 dark:text-dark-500">No hostnames configured</span>
                  )}
                </dd>
              </div>
            </dl>
          </CardContent>
        </Card>

        {/* Statistics */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <Route className="w-4 h-4" />
              Configuration Summary
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-4">
              <div className="text-center p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
                <p className="text-2xl font-bold text-gray-900 dark:text-white">{rules.length}</p>
                <p className="text-xs text-gray-500 dark:text-dark-400 uppercase">Rules</p>
              </div>
              <div className="text-center p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
                <p className="text-2xl font-bold text-gray-900 dark:text-white">{totalMatches}</p>
                <p className="text-xs text-gray-500 dark:text-dark-400 uppercase">Matches</p>
              </div>
              <div className="text-center p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
                <p className="text-2xl font-bold text-gray-900 dark:text-white">{totalFilters}</p>
                <p className="text-xs text-gray-500 dark:text-dark-400 uppercase">Filters</p>
              </div>
              <div className="text-center p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
                <p className="text-2xl font-bold text-gray-900 dark:text-white">{totalBackends}</p>
                <p className="text-xs text-gray-500 dark:text-dark-400 uppercase">Backends</p>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Rules Summary */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Route className="w-4 h-4" />
            Routing Rules
          </CardTitle>
        </CardHeader>
        <CardContent>
          {rules.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-dark-400">No routing rules configured</p>
          ) : (
            <div className="space-y-3">
              {rules.map((rule, idx) => (
                <div
                  key={rule.id}
                  className="p-3 bg-gray-50 dark:bg-dark-800 rounded-lg"
                >
                  <div className="flex items-center justify-between mb-2">
                    <span className="font-medium text-gray-900 dark:text-white">
                      Rule {idx + 1}
                    </span>
                    <div className="flex items-center gap-2">
                      {rule.matches.length > 0 && (
                        <StatusBadge status="info" size="sm">
                          {rule.matches.length} match{rule.matches.length !== 1 ? 'es' : ''}
                        </StatusBadge>
                      )}
                      {rule.filters.length > 0 && (
                        <StatusBadge status="warning" size="sm">
                          {rule.filters.length} filter{rule.filters.length !== 1 ? 's' : ''}
                        </StatusBadge>
                      )}
                      <StatusBadge status="success" size="sm">
                        {rule.backends.length} backend{rule.backends.length !== 1 ? 's' : ''}
                      </StatusBadge>
                    </div>
                  </div>
                  <p className="text-sm text-gray-600 dark:text-dark-300">{getRuleSummary(rule)}</p>

                  {/* Filters detail */}
                  {rule.filters.length > 0 && (
                    <div className="mt-2 pt-2 border-t border-gray-200 dark:border-dark-700">
                      <p className="text-xs text-gray-500 dark:text-dark-400 mb-1">Filters:</p>
                      <div className="flex flex-wrap gap-1">
                        {rule.filters.map((filter) => (
                          <span
                            key={filter.id}
                            className="text-xs px-2 py-0.5 bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-300 rounded"
                          >
                            {getFilterSummary(filter)}
                          </span>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* Backends detail */}
                  <div className="mt-2 pt-2 border-t border-gray-200 dark:border-dark-700">
                    <p className="text-xs text-gray-500 dark:text-dark-400 mb-1">Backends:</p>
                    <div className="flex flex-wrap gap-1">
                      {rule.backends.map((backend) => (
                        <span
                          key={backend.id}
                          className="text-xs px-2 py-0.5 bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300 rounded font-mono"
                        >
                          {backend.endpoint || 'No endpoint'}
                          {backend.connectorRef && ` via ${backend.connectorRef.name}`}
                        </span>
                      ))}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* YAML Preview Toggle */}
      <div className="border-t border-gray-200 dark:border-dark-700 pt-4">
        <button
          type="button"
          onClick={() => setShowYaml(!showYaml)}
          className="flex items-center gap-2 text-sm text-primary-600 dark:text-primary-400 hover:underline"
        >
          <Code className="w-4 h-4" />
          {showYaml ? 'Hide' : 'Show'} YAML Preview
        </button>

        {showYaml && (
          <div className="mt-4">
            <YamlViewer data={proxy} title={`${name || 'gateway'}.yaml`} />
          </div>
        )}
      </div>
    </div>
  );
}

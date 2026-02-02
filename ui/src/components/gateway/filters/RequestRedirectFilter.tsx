'use client';

import { Input } from '@/components/forms/Input';
import { Select } from '@/components/forms/Select';
import { REDIRECT_STATUS_CODES, PATH_MATCH_TYPES, PathMatchType } from '@/lib/gateway-defaults';
import type { FilterFormState } from '@/lib/gateway-defaults';

interface RequestRedirectFilterProps {
  filter: FilterFormState;
  ruleId: string;
  onUpdate: (ruleId: string, filterId: string, filter: Partial<FilterFormState>) => void;
}

const schemeOptions = [
  { value: '', label: 'Keep original' },
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' },
];

const statusCodeOptions = REDIRECT_STATUS_CODES.map((code) => ({
  value: code.toString(),
  label: `${code} - ${getStatusCodeDescription(code)}`,
}));

function getStatusCodeDescription(code: number): string {
  switch (code) {
    case 301:
      return 'Moved Permanently';
    case 302:
      return 'Found (Temporary)';
    case 303:
      return 'See Other';
    case 307:
      return 'Temporary Redirect';
    case 308:
      return 'Permanent Redirect';
    default:
      return '';
  }
}

export function RequestRedirectFilter({
  filter,
  ruleId,
  onUpdate,
}: RequestRedirectFilterProps) {
  const redirect = filter.requestRedirect;
  if (!redirect) return null;

  const pathTypeOptions = PATH_MATCH_TYPES.map((type) => ({
    value: type,
    label: type === 'RegularExpression' ? 'Regex' : type,
  }));

  const handleSchemeChange = (scheme: string) => {
    onUpdate(ruleId, filter.id, {
      requestRedirect: { ...redirect, scheme: scheme || undefined },
    });
  };

  const handleHostnameChange = (hostname: string) => {
    onUpdate(ruleId, filter.id, {
      requestRedirect: { ...redirect, hostname: hostname || undefined },
    });
  };

  const handlePortChange = (port: string) => {
    onUpdate(ruleId, filter.id, {
      requestRedirect: { ...redirect, port: port ? parseInt(port, 10) : undefined },
    });
  };

  const handleStatusCodeChange = (statusCode: string) => {
    onUpdate(ruleId, filter.id, {
      requestRedirect: { ...redirect, statusCode: parseInt(statusCode, 10) },
    });
  };

  const handlePathTypeChange = (type: string) => {
    onUpdate(ruleId, filter.id, {
      requestRedirect: {
        ...redirect,
        path: redirect.path
          ? { ...redirect.path, type: type as PathMatchType }
          : { type: type as PathMatchType, value: '' },
      },
    });
  };

  const handlePathValueChange = (value: string) => {
    onUpdate(ruleId, filter.id, {
      requestRedirect: {
        ...redirect,
        path: redirect.path
          ? { ...redirect.path, value }
          : { type: 'PathPrefix', value },
      },
    });
  };

  const handleClearPath = () => {
    onUpdate(ruleId, filter.id, {
      requestRedirect: {
        ...redirect,
        path: undefined,
      },
    });
  };

  // Build example URL
  const buildExampleUrl = () => {
    const scheme = redirect.scheme || 'https';
    const host = redirect.hostname || 'example.com';
    const port = redirect.port ? `:${redirect.port}` : '';
    const path = redirect.path?.value || '/new-path';
    return `${scheme}://${host}${port}${path}`;
  };

  return (
    <div className="space-y-6">
      <p className="text-sm text-gray-500 dark:text-dark-400">
        Redirect requests to a different URL. The client will receive a redirect response instead of the backend response.
      </p>

      <div className="grid grid-cols-2 gap-4">
        {/* Scheme */}
        <Select
          label="Scheme"
          value={redirect.scheme || ''}
          onChange={(e) => handleSchemeChange(e.target.value)}
          options={schemeOptions}
          hint="Change protocol (http/https)"
        />

        {/* Status Code */}
        <Select
          label="Status Code"
          value={redirect.statusCode?.toString() || '302'}
          onChange={(e) => handleStatusCodeChange(e.target.value)}
          options={statusCodeOptions}
          hint="HTTP redirect status code"
        />
      </div>

      <div className="grid grid-cols-2 gap-4">
        {/* Hostname */}
        <Input
          label="Hostname"
          value={redirect.hostname || ''}
          onChange={(e) => handleHostnameChange(e.target.value)}
          placeholder="new-domain.example.com"
          hint="Redirect to a different host (optional)"
        />

        {/* Port */}
        <Input
          type="number"
          label="Port"
          value={redirect.port?.toString() || ''}
          onChange={(e) => handlePortChange(e.target.value)}
          placeholder="443"
          min={1}
          max={65535}
          hint="Custom port number (optional)"
        />
      </div>

      {/* Path Rewrite */}
      <div className="space-y-3">
        <label className="block text-sm font-medium text-gray-700 dark:text-dark-300">
          Path Modification
        </label>
        {!redirect.path ? (
          <button
            type="button"
            onClick={() => handlePathTypeChange('PathPrefix')}
            className="w-full p-4 border-2 border-dashed border-gray-300 dark:border-dark-600 rounded-lg text-sm text-gray-500 dark:text-dark-400 hover:border-primary-500 hover:text-primary-500 transition-colors"
          >
            + Add path modification
          </button>
        ) : (
          <div className="flex items-start gap-3">
            <div className="w-40 flex-shrink-0">
              <Select
                value={redirect.path.type}
                onChange={(e) => handlePathTypeChange(e.target.value)}
                options={pathTypeOptions}
              />
            </div>
            <div className="flex-1">
              <Input
                value={redirect.path.value}
                onChange={(e) => handlePathValueChange(e.target.value)}
                placeholder="/new-path"
              />
            </div>
            <button
              type="button"
              onClick={handleClearPath}
              className="mt-2 p-2 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
              title="Remove path modification"
            >
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        )}
      </div>

      {/* Example */}
      <div className="p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
        <p className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-2">
          Redirect Example
        </p>
        <div className="text-sm text-gray-700 dark:text-dark-200 space-y-2">
          <p>
            <span className="text-gray-500 dark:text-dark-400">Request to:</span>{' '}
            <code className="font-mono">https://example.com/old-path</code>
          </p>
          <p>
            <span className="text-gray-500 dark:text-dark-400">Will redirect ({redirect.statusCode || 302}) to:</span>{' '}
            <code className="font-mono">{buildExampleUrl()}</code>
          </p>
        </div>
      </div>

      {/* Status Code Info */}
      <div className="p-3 bg-blue-50 dark:bg-blue-900/20 rounded-lg">
        <p className="text-xs font-medium text-blue-700 dark:text-blue-300 mb-2">Status Code Guide</p>
        <ul className="text-xs text-blue-600 dark:text-blue-400 space-y-1">
          <li><strong>301:</strong> Permanent redirect - browsers cache this, search engines update links</li>
          <li><strong>302:</strong> Temporary redirect - most common, doesn't cache</li>
          <li><strong>307:</strong> Temporary redirect - preserves HTTP method (POST stays POST)</li>
          <li><strong>308:</strong> Permanent redirect - preserves HTTP method</li>
        </ul>
      </div>
    </div>
  );
}

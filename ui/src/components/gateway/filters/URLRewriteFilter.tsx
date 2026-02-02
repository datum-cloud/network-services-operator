'use client';

import { Input } from '@/components/forms/Input';
import { Select } from '@/components/forms/Select';
import { PATH_MATCH_TYPES, PathMatchType } from '@/lib/gateway-defaults';
import type { URLRewriteFormState } from '@/lib/gateway-defaults';
import type { FilterFormState } from '@/lib/gateway-defaults';

interface URLRewriteFilterProps {
  filter: FilterFormState;
  ruleId: string;
  onUpdate: (ruleId: string, filterId: string, filter: Partial<FilterFormState>) => void;
}

export function URLRewriteFilter({
  filter,
  ruleId,
  onUpdate,
}: URLRewriteFilterProps) {
  const rewrite = filter.urlRewrite;
  if (!rewrite) return null;

  const pathTypeOptions = PATH_MATCH_TYPES.map((type) => ({
    value: type,
    label: type === 'RegularExpression' ? 'Regex' : type,
  }));

  const handleHostnameChange = (hostname: string) => {
    onUpdate(ruleId, filter.id, {
      urlRewrite: { ...rewrite, hostname },
    });
  };

  const handlePathTypeChange = (type: string) => {
    onUpdate(ruleId, filter.id, {
      urlRewrite: {
        ...rewrite,
        path: rewrite.path
          ? { ...rewrite.path, type: type as PathMatchType }
          : { type: type as PathMatchType, value: '' },
      },
    });
  };

  const handlePathValueChange = (value: string) => {
    onUpdate(ruleId, filter.id, {
      urlRewrite: {
        ...rewrite,
        path: rewrite.path
          ? { ...rewrite.path, value }
          : { type: 'PathPrefix', value },
      },
    });
  };

  const handleClearPath = () => {
    onUpdate(ruleId, filter.id, {
      urlRewrite: {
        ...rewrite,
        path: undefined,
      },
    });
  };

  return (
    <div className="space-y-6">
      <p className="text-sm text-gray-500 dark:text-dark-400">
        Rewrite the request URL before forwarding to the backend. You can modify the hostname, path, or both.
      </p>

      {/* Hostname Rewrite */}
      <div className="space-y-2">
        <Input
          label="Rewrite Hostname"
          value={rewrite.hostname || ''}
          onChange={(e) => handleHostnameChange(e.target.value)}
          placeholder="api.internal.example.com"
          hint="Replace the Host header with this value (optional)"
        />
      </div>

      {/* Path Rewrite */}
      <div className="space-y-3">
        <label className="block text-sm font-medium text-gray-700 dark:text-dark-300">
          Rewrite Path
        </label>
        {!rewrite.path ? (
          <button
            type="button"
            onClick={() => handlePathTypeChange('PathPrefix')}
            className="w-full p-4 border-2 border-dashed border-gray-300 dark:border-dark-600 rounded-lg text-sm text-gray-500 dark:text-dark-400 hover:border-primary-500 hover:text-primary-500 transition-colors"
          >
            + Add path rewrite
          </button>
        ) : (
          <div className="flex items-start gap-3">
            <div className="w-40 flex-shrink-0">
              <Select
                value={rewrite.path.type}
                onChange={(e) => handlePathTypeChange(e.target.value)}
                options={pathTypeOptions}
              />
            </div>
            <div className="flex-1">
              <Input
                value={rewrite.path.value}
                onChange={(e) => handlePathValueChange(e.target.value)}
                placeholder={rewrite.path.type === 'RegularExpression' ? '/api/v2$1' : '/api/v2'}
              />
            </div>
            <button
              type="button"
              onClick={handleClearPath}
              className="mt-2 p-2 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
              title="Remove path rewrite"
            >
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        )}
        <p className="text-xs text-gray-500 dark:text-dark-400">
          {rewrite.path?.type === 'PathPrefix'
            ? 'Replace the matched path prefix with this value'
            : rewrite.path?.type === 'Exact'
            ? 'Replace the entire path with this value'
            : 'Use regex capture groups (e.g., $1) for replacements'}
        </p>
      </div>

      {/* Example */}
      <div className="p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
        <p className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-2">Example</p>
        <div className="text-sm text-gray-700 dark:text-dark-200 space-y-1">
          <p>
            <span className="text-gray-500 dark:text-dark-400">Original:</span>{' '}
            <code className="font-mono">https://example.com/api/v1/users</code>
          </p>
          <p>
            <span className="text-gray-500 dark:text-dark-400">Rewritten:</span>{' '}
            <code className="font-mono">
              https://{rewrite.hostname || 'example.com'}
              {rewrite.path?.value || '/api/v1'}/users
            </code>
          </p>
        </div>
      </div>
    </div>
  );
}

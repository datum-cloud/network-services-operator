'use client';

import { Plus, X } from 'lucide-react';
import { Input } from '@/components/forms/Input';
import { Button } from '@/components/common/Button';
import { TagInput } from '@/components/forms/TagInput';
import { Toggle } from '@/components/forms/Checkbox';
import { Select } from '@/components/forms/Select';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';
import { CORS_METHODS, STRING_MATCH_TYPES } from '@/lib/security-policy-defaults';
import { MAX_CORS_FIELD_LENGTH } from '@/lib/security-policy-validation';
import type { StringMatchFormState } from '@/lib/security-policy-defaults';

export function CORSForm() {
  const {
    cors,
    updateCORS,
    addCORSOrigin,
    removeCORSOrigin,
    updateCORSOrigin,
    getErrors,
  } = useSecurityPolicyForm();

  if (!cors) return null;

  return (
    <div className="space-y-6">
      <div>
        <h4 className="text-sm font-medium text-gray-900 dark:text-white mb-1">
          Cross-Origin Resource Sharing (CORS)
        </h4>
        <p className="text-sm text-gray-500 dark:text-dark-400">
          Configure CORS headers to allow cross-origin requests from browsers.
        </p>
      </div>

      {/* Allow Origins */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
            Allowed Origins ({cors.allowOrigins.length}/{MAX_CORS_FIELD_LENGTH})
          </label>
          <Button
            type="button"
            variant="outline"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={addCORSOrigin}
            disabled={cors.allowOrigins.length >= MAX_CORS_FIELD_LENGTH}
          >
            Add Origin
          </Button>
        </div>

        {cors.allowOrigins.length === 0 ? (
          <div className="text-center py-4 bg-gray-50 dark:bg-dark-800 rounded-lg">
            <p className="text-sm text-gray-500 dark:text-dark-400">No origins configured</p>
            <p className="text-xs text-gray-400 dark:text-dark-500 mt-1">
              Add origins to allow cross-origin requests
            </p>
          </div>
        ) : (
          <div className="space-y-2">
            {cors.allowOrigins.map((origin, index) => (
              <div key={origin.id} className="flex gap-2 items-start">
                <div className="w-36 flex-shrink-0">
                  <Select
                    value={origin.type}
                    onChange={(e) =>
                      updateCORSOrigin(origin.id, { type: e.target.value as StringMatchFormState['type'] })
                    }
                    options={STRING_MATCH_TYPES.map(t => ({ value: t.value, label: t.label }))}
                  />
                </div>
                <div className="flex-1">
                  <Input
                    value={origin.value}
                    onChange={(e) => updateCORSOrigin(origin.id, { value: e.target.value })}
                    placeholder={
                      origin.type === 'Exact' ? 'https://example.com' :
                      origin.type === 'Prefix' ? 'https://app.' :
                      origin.type === 'Suffix' ? '.example.com' :
                      '.*\\.example\\.com'
                    }
                    error={getErrors(`cors.allowOrigins.${index}.value`)[0]}
                  />
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  icon={<X className="w-4 h-4" />}
                  onClick={() => removeCORSOrigin(origin.id)}
                />
              </div>
            ))}
          </div>
        )}
        <p className="text-xs text-gray-500 dark:text-dark-400">
          Origins that are allowed to make cross-origin requests
        </p>
      </div>

      {/* Allow Methods */}
      <div className="space-y-2">
        <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
          Allowed Methods
        </label>
        <div className="flex flex-wrap gap-2">
          {CORS_METHODS.map((method) => {
            const isSelected = cors.allowMethods.includes(method);
            return (
              <button
                key={method}
                type="button"
                onClick={() => {
                  if (isSelected) {
                    updateCORS({ allowMethods: cors.allowMethods.filter(m => m !== method) });
                  } else if (cors.allowMethods.length < MAX_CORS_FIELD_LENGTH) {
                    updateCORS({ allowMethods: [...cors.allowMethods, method] });
                  }
                }}
                className={`px-3 py-1.5 text-sm font-medium rounded-lg transition-colors ${
                  isSelected
                    ? 'bg-primary-600 text-white'
                    : 'bg-gray-100 dark:bg-dark-700 text-gray-700 dark:text-dark-300 hover:bg-gray-200 dark:hover:bg-dark-600'
                }`}
              >
                {method}
              </button>
            );
          })}
        </div>
        <p className="text-xs text-gray-500 dark:text-dark-400">
          HTTP methods allowed for cross-origin requests
        </p>
      </div>

      {/* Allow Headers */}
      <TagInput
        label={`Allowed Headers (max ${MAX_CORS_FIELD_LENGTH})`}
        value={cors.allowHeaders}
        onChange={(headers) => updateCORS({ allowHeaders: headers.slice(0, MAX_CORS_FIELD_LENGTH) })}
        placeholder="Authorization, Content-Type"
        hint="Request headers allowed for cross-origin requests"
        error={getErrors('cors.allowHeaders')[0]}
      />

      {/* Expose Headers */}
      <TagInput
        label={`Expose Headers (max ${MAX_CORS_FIELD_LENGTH})`}
        value={cors.exposeHeaders}
        onChange={(headers) => updateCORS({ exposeHeaders: headers.slice(0, MAX_CORS_FIELD_LENGTH) })}
        placeholder="X-Custom-Header"
        hint="Response headers exposed to cross-origin requests"
        error={getErrors('cors.exposeHeaders')[0]}
      />

      {/* Max Age */}
      <Input
        label="Max Age"
        value={cors.maxAge || ''}
        onChange={(e) => updateCORS({ maxAge: e.target.value || undefined })}
        placeholder="86400s"
        hint="How long preflight results can be cached (e.g., 86400s for 24 hours)"
      />

      {/* Allow Credentials */}
      <Toggle
        label="Allow Credentials"
        description="Allow cookies and authentication headers in cross-origin requests"
        checked={cors.allowCredentials || false}
        onChange={(e) => updateCORS({ allowCredentials: e.target.checked })}
      />
    </div>
  );
}

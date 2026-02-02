'use client';

import { Plus, X } from 'lucide-react';
import { Input } from '@/components/forms/Input';
import { Button } from '@/components/common/Button';
import { TagInput } from '@/components/forms/TagInput';
import { SecretRefInput } from '../shared/SecretRefInput';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';
import { MAX_CREDENTIAL_REFS, MAX_EXTRACT_FROM } from '@/lib/security-policy-validation';

export function APIKeyAuthForm() {
  const {
    apiKeyAuth,
    updateAPIKeyAuth,
    addCredentialRef,
    removeCredentialRef,
    updateCredentialRef,
    addExtractFrom,
    removeExtractFrom,
    updateExtractFrom,
    getErrors,
  } = useSecurityPolicyForm();

  if (!apiKeyAuth) return null;

  return (
    <div className="space-y-6">
      <div>
        <h4 className="text-sm font-medium text-gray-900 dark:text-white mb-1">
          API Key Authentication
        </h4>
        <p className="text-sm text-gray-500 dark:text-dark-400">
          Configure API key validation from secrets and specify where to extract keys from requests.
        </p>
      </div>

      {/* Credential References */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
            API Key Secrets ({apiKeyAuth.credentialRefs.length}/{MAX_CREDENTIAL_REFS})
          </label>
          <Button
            type="button"
            variant="outline"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={addCredentialRef}
            disabled={apiKeyAuth.credentialRefs.length >= MAX_CREDENTIAL_REFS}
          >
            Add Secret
          </Button>
        </div>

        {apiKeyAuth.credentialRefs.map((ref, index) => (
          <div key={ref.id} className="flex gap-2 items-start">
            <div className="flex-1">
              <SecretRefInput
                value={ref}
                onChange={(updates) => updateCredentialRef(ref.id, updates)}
                hint={index === 0 ? 'Secret containing API keys' : undefined}
                error={getErrors(`apiKeyAuth.credentialRefs.${index}.name`)[0]}
                required
              />
            </div>
            {apiKeyAuth.credentialRefs.length > 1 && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                icon={<X className="w-4 h-4" />}
                onClick={() => removeCredentialRef(ref.id)}
                className="mt-7"
              />
            )}
          </div>
        ))}
      </div>

      {/* Extract From */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
            Extract API Key From ({apiKeyAuth.extractFrom.length}/{MAX_EXTRACT_FROM})
          </label>
          <Button
            type="button"
            variant="outline"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={addExtractFrom}
            disabled={apiKeyAuth.extractFrom.length >= MAX_EXTRACT_FROM}
          >
            Add Source
          </Button>
        </div>

        {apiKeyAuth.extractFrom.map((entry, index) => (
          <div
            key={entry.id}
            className="p-4 bg-gray-50 dark:bg-dark-800 rounded-lg space-y-4"
          >
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase">
                Source {index + 1}
              </span>
              {apiKeyAuth.extractFrom.length > 1 && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  icon={<X className="w-4 h-4" />}
                  onClick={() => removeExtractFrom(entry.id)}
                />
              )}
            </div>

            <TagInput
              label="Headers"
              value={entry.headers}
              onChange={(headers) => updateExtractFrom(entry.id, { headers })}
              placeholder="X-API-Key, Authorization"
              hint="HTTP headers to extract API key from"
            />

            <TagInput
              label="Query Parameters"
              value={entry.params}
              onChange={(params) => updateExtractFrom(entry.id, { params })}
              placeholder="api_key, token"
              hint="Query parameters to extract API key from"
            />

            <TagInput
              label="Cookies"
              value={entry.cookies}
              onChange={(cookies) => updateExtractFrom(entry.id, { cookies })}
              placeholder="api_key"
              hint="Cookies to extract API key from"
            />
          </div>
        ))}
      </div>

      {/* Forward Client ID Header */}
      <Input
        label="Forward Client ID Header"
        value={apiKeyAuth.forwardClientIDHeader || ''}
        onChange={(e) => updateAPIKeyAuth({ forwardClientIDHeader: e.target.value || undefined })}
        placeholder="X-Client-ID"
        hint="Optional header to forward the client identity to the backend"
        error={getErrors('apiKeyAuth.forwardClientIDHeader')[0]}
      />
    </div>
  );
}

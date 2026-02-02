'use client';

import { Plus, X, ChevronDown, ChevronRight } from 'lucide-react';
import { useState } from 'react';
import { Input } from '@/components/forms/Input';
import { Button } from '@/components/common/Button';
import { TagInput } from '@/components/forms/TagInput';
import { Toggle } from '@/components/forms/Checkbox';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';
import { MAX_JWT_CLAIM_TO_HEADERS } from '@/lib/security-policy-validation';

export function JWTForm() {
  const {
    jwt,
    updateJWT,
    addJWTProvider,
    removeJWTProvider,
    updateJWTProvider,
    addClaimToHeader,
    removeClaimToHeader,
    updateClaimToHeader,
    getErrors,
  } = useSecurityPolicyForm();

  const [expandedProviders, setExpandedProviders] = useState<Set<string>>(new Set());

  if (!jwt) return null;

  const toggleProviderExpanded = (id: string) => {
    const newExpanded = new Set(expandedProviders);
    if (newExpanded.has(id)) {
      newExpanded.delete(id);
    } else {
      newExpanded.add(id);
    }
    setExpandedProviders(newExpanded);
  };

  return (
    <div className="space-y-6">
      <div>
        <h4 className="text-sm font-medium text-gray-900 dark:text-white mb-1">
          JWT Authentication
        </h4>
        <p className="text-sm text-gray-500 dark:text-dark-400">
          Configure JSON Web Token validation with one or more providers.
        </p>
      </div>

      {/* Optional JWT */}
      <Toggle
        label="Optional JWT"
        description="Allow requests without JWT to pass through (useful for public endpoints)"
        checked={jwt.optional || false}
        onChange={(e) => updateJWT({ optional: e.target.checked })}
      />

      {/* Providers */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
            JWT Providers ({jwt.providers.length})
          </label>
          <Button
            type="button"
            variant="outline"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={() => {
              addJWTProvider();
            }}
          >
            Add Provider
          </Button>
        </div>

        {jwt.providers.length === 0 ? (
          <div className="text-center py-8 bg-gray-50 dark:bg-dark-800 rounded-lg">
            <p className="text-sm text-gray-500 dark:text-dark-400">No JWT providers configured</p>
            <p className="text-xs text-gray-400 dark:text-dark-500 mt-1">
              At least one provider is required
            </p>
          </div>
        ) : (
          jwt.providers.map((provider, index) => {
            const isExpanded = expandedProviders.has(provider.id);
            return (
              <div
                key={provider.id}
                className="border border-gray-200 dark:border-dark-700 rounded-lg overflow-hidden"
              >
                {/* Provider Header */}
                <div
                  className="flex items-center justify-between p-4 bg-gray-50 dark:bg-dark-800 cursor-pointer"
                  onClick={() => toggleProviderExpanded(provider.id)}
                >
                  <div className="flex items-center gap-3">
                    {isExpanded ? (
                      <ChevronDown className="w-4 h-4 text-gray-400" />
                    ) : (
                      <ChevronRight className="w-4 h-4 text-gray-400" />
                    )}
                    <div>
                      <span className="text-sm font-medium text-gray-900 dark:text-white">
                        {provider.name || `Provider ${index + 1}`}
                      </span>
                      {provider.issuer && (
                        <p className="text-xs text-gray-500 dark:text-dark-400 truncate max-w-xs">
                          {provider.issuer}
                        </p>
                      )}
                    </div>
                  </div>
                  {jwt.providers.length > 1 && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      icon={<X className="w-4 h-4" />}
                      onClick={(e) => {
                        e.stopPropagation();
                        removeJWTProvider(provider.id);
                      }}
                    />
                  )}
                </div>

                {/* Provider Details */}
                {isExpanded && (
                  <div className="p-4 space-y-4">
                    <Input
                      label="Provider Name"
                      value={provider.name}
                      onChange={(e) =>
                        updateJWTProvider(provider.id, { name: e.target.value })
                      }
                      placeholder="auth0"
                      hint="Unique identifier for this JWT provider"
                      error={getErrors(`jwt.providers.${index}.name`)[0]}
                      required
                    />

                    <Input
                      label="Issuer"
                      value={provider.issuer || ''}
                      onChange={(e) =>
                        updateJWTProvider(provider.id, { issuer: e.target.value || undefined })
                      }
                      placeholder="https://example.auth0.com/"
                      hint="JWT issuer (iss) claim value"
                    />

                    <TagInput
                      label="Audiences"
                      value={provider.audiences}
                      onChange={(audiences) =>
                        updateJWTProvider(provider.id, { audiences })
                      }
                      placeholder="https://api.example.com"
                      hint="Allowed audience (aud) claim values"
                    />

                    <Input
                      label="Remote JWKS URI"
                      value={provider.remoteJWKS?.uri || ''}
                      onChange={(e) =>
                        updateJWTProvider(provider.id, {
                          remoteJWKS: e.target.value
                            ? { uri: e.target.value }
                            : undefined,
                        })
                      }
                      placeholder="https://example.auth0.com/.well-known/jwks.json"
                      hint="URL to fetch JSON Web Key Set for signature verification"
                    />

                    {/* Claim to Headers */}
                    <div className="space-y-3">
                      <div className="flex items-center justify-between">
                        <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
                          Claim to Headers ({provider.claimToHeaders.length}/{MAX_JWT_CLAIM_TO_HEADERS})
                        </label>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          icon={<Plus className="w-4 h-4" />}
                          onClick={() => addClaimToHeader(provider.id)}
                          disabled={provider.claimToHeaders.length >= MAX_JWT_CLAIM_TO_HEADERS}
                        >
                          Add Mapping
                        </Button>
                      </div>

                      {provider.claimToHeaders.length > 0 && (
                        <div className="space-y-2">
                          {provider.claimToHeaders.map((mapping, mappingIndex) => (
                            <div
                              key={mapping.id}
                              className="flex gap-2 items-start p-3 bg-gray-50 dark:bg-dark-800 rounded-lg"
                            >
                              <div className="flex-1">
                                <Input
                                  label={mappingIndex === 0 ? 'Claim' : undefined}
                                  value={mapping.claim}
                                  onChange={(e) =>
                                    updateClaimToHeader(provider.id, mapping.id, {
                                      claim: e.target.value,
                                    })
                                  }
                                  placeholder="sub"
                                />
                              </div>
                              <div className="flex-1">
                                <Input
                                  label={mappingIndex === 0 ? 'Header' : undefined}
                                  value={mapping.header}
                                  onChange={(e) =>
                                    updateClaimToHeader(provider.id, mapping.id, {
                                      header: e.target.value,
                                    })
                                  }
                                  placeholder="x-user-id"
                                />
                              </div>
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                icon={<X className="w-4 h-4" />}
                                onClick={() => removeClaimToHeader(provider.id, mapping.id)}
                                className={mappingIndex === 0 ? 'mt-7' : ''}
                              />
                            </div>
                          ))}
                        </div>
                      )}
                      <p className="text-xs text-gray-500 dark:text-dark-400">
                        Forward JWT claims as HTTP headers to the backend
                      </p>
                    </div>

                    <Toggle
                      label="Recompute Route"
                      description="Re-evaluate route matching after JWT validation (useful for claim-based routing)"
                      checked={provider.recomputeRoute || false}
                      onChange={(e) =>
                        updateJWTProvider(provider.id, {
                          recomputeRoute: e.target.checked,
                        })
                      }
                    />
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

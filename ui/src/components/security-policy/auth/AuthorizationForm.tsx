'use client';

import { Plus, X, ChevronDown, ChevronRight } from 'lucide-react';
import { useState } from 'react';
import { Input } from '@/components/forms/Input';
import { Button } from '@/components/common/Button';
import { Select } from '@/components/forms/Select';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';
import { AUTHORIZATION_ACTIONS } from '@/lib/security-policy-defaults';
import { MAX_AUTHORIZATION_RULES, MAX_CLIENT_CIDRS } from '@/lib/security-policy-validation';
import type { AuthorizationRuleFormState } from '@/lib/security-policy-defaults';

export function AuthorizationForm() {
  const {
    authorization,
    updateAuthorization,
    addAuthorizationRule,
    removeAuthorizationRule,
    updateAuthorizationRule,
    addClientCIDR,
    removeClientCIDR,
    updateClientCIDR,
    getErrors,
  } = useSecurityPolicyForm();

  const [expandedRules, setExpandedRules] = useState<Set<string>>(new Set());

  if (!authorization) return null;

  const toggleRuleExpanded = (id: string) => {
    const newExpanded = new Set(expandedRules);
    if (newExpanded.has(id)) {
      newExpanded.delete(id);
    } else {
      newExpanded.add(id);
    }
    setExpandedRules(newExpanded);
  };

  return (
    <div className="space-y-6">
      <div>
        <h4 className="text-sm font-medium text-gray-900 dark:text-white mb-1">
          Authorization
        </h4>
        <p className="text-sm text-gray-500 dark:text-dark-400">
          Configure allow/deny rules based on client IP addresses and JWT claims.
        </p>
      </div>

      {/* Default Action */}
      <Select
        label="Default Action"
        value={authorization.defaultAction}
        onChange={(e) => updateAuthorization({ defaultAction: e.target.value as 'Allow' | 'Deny' })}
        options={AUTHORIZATION_ACTIONS.map(a => ({ value: a, label: a }))}
        hint="Action to take when no rules match"
        required
      />

      {/* Rules */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
            Authorization Rules ({authorization.rules.length}/{MAX_AUTHORIZATION_RULES})
          </label>
          <Button
            type="button"
            variant="outline"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={addAuthorizationRule}
            disabled={authorization.rules.length >= MAX_AUTHORIZATION_RULES}
          >
            Add Rule
          </Button>
        </div>

        {authorization.rules.length === 0 ? (
          <div className="text-center py-8 bg-gray-50 dark:bg-dark-800 rounded-lg">
            <p className="text-sm text-gray-500 dark:text-dark-400">No authorization rules configured</p>
            <p className="text-xs text-gray-400 dark:text-dark-500 mt-1">
              Add rules to allow or deny requests based on criteria
            </p>
          </div>
        ) : (
          authorization.rules.map((rule, index) => {
            const isExpanded = expandedRules.has(rule.id);
            return (
              <div
                key={rule.id}
                className="border border-gray-200 dark:border-dark-700 rounded-lg overflow-hidden"
              >
                {/* Rule Header */}
                <div
                  className={`flex items-center justify-between p-4 cursor-pointer ${
                    rule.action === 'Allow'
                      ? 'bg-green-50 dark:bg-green-900/20'
                      : 'bg-red-50 dark:bg-red-900/20'
                  }`}
                  onClick={() => toggleRuleExpanded(rule.id)}
                >
                  <div className="flex items-center gap-3">
                    {isExpanded ? (
                      <ChevronDown className="w-4 h-4 text-gray-400" />
                    ) : (
                      <ChevronRight className="w-4 h-4 text-gray-400" />
                    )}
                    <div className="flex items-center gap-2">
                      <span
                        className={`px-2 py-0.5 text-xs font-medium rounded ${
                          rule.action === 'Allow'
                            ? 'bg-green-100 dark:bg-green-900/50 text-green-700 dark:text-green-400'
                            : 'bg-red-100 dark:bg-red-900/50 text-red-700 dark:text-red-400'
                        }`}
                      >
                        {rule.action}
                      </span>
                      <span className="text-sm font-medium text-gray-900 dark:text-white">
                        {rule.name || `Rule ${index + 1}`}
                      </span>
                    </div>
                    {rule.principal.clientCIDRs.length > 0 && (
                      <span className="text-xs text-gray-500 dark:text-dark-400">
                        {rule.principal.clientCIDRs.length} CIDR(s)
                      </span>
                    )}
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    icon={<X className="w-4 h-4" />}
                    onClick={(e) => {
                      e.stopPropagation();
                      removeAuthorizationRule(rule.id);
                    }}
                  />
                </div>

                {/* Rule Details */}
                {isExpanded && (
                  <div className="p-4 space-y-4 bg-white dark:bg-dark-900">
                    <div className="grid grid-cols-2 gap-4">
                      <Input
                        label="Rule Name"
                        value={rule.name || ''}
                        onChange={(e) =>
                          updateAuthorizationRule(rule.id, { name: e.target.value || undefined })
                        }
                        placeholder="allow-internal"
                        hint="Optional name for this rule"
                      />

                      <Select
                        label="Action"
                        value={rule.action}
                        onChange={(e) =>
                          updateAuthorizationRule(rule.id, { action: e.target.value as 'Allow' | 'Deny' })
                        }
                        options={AUTHORIZATION_ACTIONS.map(a => ({ value: a, label: a }))}
                        required
                      />
                    </div>

                    {/* Client CIDRs */}
                    <div className="space-y-3">
                      <div className="flex items-center justify-between">
                        <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
                          Client CIDRs ({rule.principal.clientCIDRs.length}/{MAX_CLIENT_CIDRS})
                        </label>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          icon={<Plus className="w-4 h-4" />}
                          onClick={() => addClientCIDR(rule.id)}
                          disabled={rule.principal.clientCIDRs.length >= MAX_CLIENT_CIDRS}
                        >
                          Add CIDR
                        </Button>
                      </div>

                      {rule.principal.clientCIDRs.length > 0 && (
                        <div className="space-y-2">
                          {rule.principal.clientCIDRs.map((cidr, cidrIndex) => (
                            <div key={cidr.id} className="flex gap-2 items-start">
                              <div className="flex-1">
                                <Input
                                  value={cidr.cidr}
                                  onChange={(e) =>
                                    updateClientCIDR(rule.id, cidr.id, { cidr: e.target.value })
                                  }
                                  placeholder="10.0.0.0/8"
                                  error={getErrors(`authorization.rules.${index}.principal.clientCIDRs.${cidrIndex}.cidr`)[0]}
                                />
                              </div>
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                icon={<X className="w-4 h-4" />}
                                onClick={() => removeClientCIDR(rule.id, cidr.id)}
                              />
                            </div>
                          ))}
                        </div>
                      )}
                      <p className="text-xs text-gray-500 dark:text-dark-400">
                        IP address ranges in CIDR notation (e.g., 10.0.0.0/8, 192.168.0.0/16)
                      </p>
                    </div>
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

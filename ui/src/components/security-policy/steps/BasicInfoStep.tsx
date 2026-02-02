'use client';

import { Plus, X } from 'lucide-react';
import { Input } from '@/components/forms/Input';
import { Select } from '@/components/forms/Select';
import { Button } from '@/components/common/Button';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';
import { TARGET_REF_KINDS } from '@/lib/security-policy-defaults';

interface BasicInfoStepProps {
  namespaces?: string[];
  isEdit?: boolean;
}

export function BasicInfoStep({ namespaces = [], isEdit = false }: BasicInfoStepProps) {
  const {
    name,
    namespace,
    targetRefs,
    setName,
    setNamespace,
    addTargetRef,
    removeTargetRef,
    updateTargetRef,
    getErrors,
  } = useSecurityPolicyForm();

  const namespaceOptions = namespaces.map(ns => ({ value: ns, label: ns }));

  return (
    <div className="space-y-8">
      {/* Metadata Section */}
      <div className="space-y-4">
        <h3 className="text-lg font-medium text-gray-900 dark:text-white">
          Policy Information
        </h3>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Input
            label="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="my-security-policy"
            hint="Lowercase alphanumeric with hyphens"
            error={getErrors('name')[0]}
            required
            disabled={isEdit}
          />

          {namespaceOptions.length > 0 ? (
            <Select
              label="Namespace"
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
              options={namespaceOptions}
              error={getErrors('namespace')[0]}
              required
              disabled={isEdit}
            />
          ) : (
            <Input
              label="Namespace"
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
              placeholder="default"
              error={getErrors('namespace')[0]}
              required
              disabled={isEdit}
            />
          )}
        </div>
      </div>

      {/* Target References Section */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <h3 className="text-lg font-medium text-gray-900 dark:text-white">
              Target References
            </h3>
            <p className="text-sm text-gray-500 dark:text-dark-400">
              Specify which Gateway or HTTPRoute resources this policy applies to
            </p>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={addTargetRef}
          >
            Add Target
          </Button>
        </div>

        {targetRefs.length === 0 ? (
          <div className="text-center py-8 bg-gray-50 dark:bg-dark-800 rounded-lg">
            <p className="text-sm text-gray-500 dark:text-dark-400">No targets configured</p>
            <p className="text-xs text-gray-400 dark:text-dark-500 mt-1">
              At least one target reference is required
            </p>
          </div>
        ) : (
          <div className="space-y-4">
            {targetRefs.map((ref, index) => (
              <div
                key={ref.id}
                className="p-4 bg-gray-50 dark:bg-dark-800 rounded-lg space-y-4"
              >
                <div className="flex items-start justify-between">
                  <span className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase">
                    Target {index + 1}
                  </span>
                  {targetRefs.length > 0 && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      icon={<X className="w-4 h-4" />}
                      onClick={() => removeTargetRef(ref.id)}
                    />
                  )}
                </div>

                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                  <Select
                    label="Kind"
                    value={ref.kind}
                    onChange={(e) => updateTargetRef(ref.id, { kind: e.target.value })}
                    options={TARGET_REF_KINDS.map(k => ({ value: k.value, label: k.label }))}
                    required
                  />

                  <Input
                    label="Name"
                    value={ref.name}
                    onChange={(e) => updateTargetRef(ref.id, { name: e.target.value })}
                    placeholder="my-gateway"
                    required
                  />

                  <Input
                    label="Section Name"
                    value={ref.sectionName || ''}
                    onChange={(e) => updateTargetRef(ref.id, { sectionName: e.target.value || undefined })}
                    placeholder="Optional"
                    hint="Specific section of the resource"
                  />
                </div>
              </div>
            ))}
          </div>
        )}

        {getErrors('targetRefs')[0] && (
          <p className="text-sm text-red-500">{getErrors('targetRefs')[0]}</p>
        )}
      </div>
    </div>
  );
}

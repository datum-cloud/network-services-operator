'use client';

import { Input } from '@/components/forms/Input';
import { Select } from '@/components/forms/Select';
import { TagInput } from '@/components/forms/TagInput';
import { FormSection, FormGrid } from '@/components/forms/FormSection';
import { MAX_HOSTNAMES } from '@/lib/gateway-validation';

interface BasicInfoStepProps {
  name: string;
  namespace: string;
  hostnames: string[];
  namespaceOptions: { value: string; label: string }[];
  onNameChange: (name: string) => void;
  onNamespaceChange: (namespace: string) => void;
  onHostnamesChange: (hostnames: string[]) => void;
  errors: {
    name?: string;
    namespace?: string;
    hostnames?: string;
  };
  isEdit?: boolean;
}

export function BasicInfoStep({
  name,
  namespace,
  hostnames,
  namespaceOptions,
  onNameChange,
  onNamespaceChange,
  onHostnamesChange,
  errors,
  isEdit = false,
}: BasicInfoStepProps) {
  const hostnameCount = hostnames.length;
  const hostnameError = hostnameCount > MAX_HOSTNAMES
    ? `Maximum ${MAX_HOSTNAMES} hostnames allowed (currently ${hostnameCount})`
    : errors.hostnames;

  return (
    <div className="space-y-8">
      <FormSection
        title="Basic Information"
        description="Configure the name and namespace for this gateway"
      >
        <FormGrid columns={2}>
          <Input
            label="Name"
            value={name}
            onChange={(e) => onNameChange(e.target.value)}
            placeholder="my-gateway"
            required
            disabled={isEdit}
            hint={isEdit ? 'Name cannot be changed after creation' : 'Unique name for this gateway'}
            error={errors.name}
          />
          <Select
            label="Namespace"
            value={namespace}
            onChange={(e) => onNamespaceChange(e.target.value)}
            options={namespaceOptions}
            required
            disabled={isEdit}
            hint={isEdit ? 'Namespace cannot be changed after creation' : undefined}
            error={errors.namespace}
          />
        </FormGrid>
      </FormSection>

      <FormSection
        title="Hostnames"
        description={`Specify the hostnames this gateway will respond to (up to ${MAX_HOSTNAMES})`}
      >
        <div className="space-y-2">
          <TagInput
            label={`Hostnames (${hostnameCount} of ${MAX_HOSTNAMES})`}
            value={hostnames}
            onChange={onHostnamesChange}
            placeholder="Add hostname and press Enter"
            hint="e.g., api.example.com, www.example.com, *.example.com"
            error={hostnameError}
          />
          {hostnames.length > 0 && (
            <div className="flex flex-wrap gap-2 pt-2">
              <p className="text-xs text-gray-500 dark:text-dark-400 w-full mb-1">
                Preview:
              </p>
              {hostnames.map((hostname) => (
                <a
                  key={hostname}
                  href={`https://${hostname.replace('*.', 'www.')}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs px-2 py-1 bg-blue-50 dark:bg-blue-900/20 text-blue-600 dark:text-blue-400 rounded hover:underline"
                >
                  {hostname}
                </a>
              ))}
            </div>
          )}
        </div>
      </FormSection>

      {/* Tips */}
      <div className="p-4 bg-gray-50 dark:bg-dark-800 rounded-lg">
        <h4 className="text-sm font-medium text-gray-900 dark:text-white mb-2">Tips</h4>
        <ul className="text-sm text-gray-600 dark:text-dark-300 space-y-1 list-disc list-inside">
          <li>Names must be lowercase alphanumeric characters or hyphens</li>
          <li>Use wildcard hostnames (*.example.com) to match subdomains</li>
          <li>Multiple hostnames can share the same routing rules</li>
        </ul>
      </div>
    </div>
  );
}

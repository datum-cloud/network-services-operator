'use client';

import { Input } from '@/components/forms/Input';
import { SecretRefInput } from '../shared/SecretRefInput';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';

export function BasicAuthForm() {
  const { basicAuth, updateBasicAuth, getErrors } = useSecurityPolicyForm();

  if (!basicAuth) return null;

  return (
    <div className="space-y-6">
      <div>
        <h4 className="text-sm font-medium text-gray-900 dark:text-white mb-1">
          Basic Authentication
        </h4>
        <p className="text-sm text-gray-500 dark:text-dark-400">
          Configure HTTP Basic Authentication using an htpasswd-formatted secret.
        </p>
      </div>

      <SecretRefInput
        label="Users Secret"
        value={basicAuth.users}
        onChange={(updates) =>
          updateBasicAuth({ users: { ...basicAuth.users, ...updates } })
        }
        hint="Secret containing htpasswd-formatted credentials"
        error={getErrors('basicAuth.users.name')[0]}
        required
      />

      <Input
        label="Forward Username Header"
        value={basicAuth.forwardUsernameHeader || ''}
        onChange={(e) => updateBasicAuth({ forwardUsernameHeader: e.target.value || undefined })}
        placeholder="X-Forwarded-User"
        hint="Optional header to forward the authenticated username to the backend"
      />
    </div>
  );
}

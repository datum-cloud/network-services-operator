'use client';

import { Toggle } from '@/components/forms/Checkbox';
import { AUTH_TYPE_LABELS, AUTH_TYPE_DESCRIPTIONS } from '@/lib/security-policy-defaults';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';

const AUTH_TYPES = [
  'basicAuth',
  'apiKeyAuth',
  'jwt',
  'oidc',
  'cors',
  'authorization',
] as const;

type AuthType = typeof AUTH_TYPES[number];

export function AuthTypeToggle() {
  const {
    basicAuth,
    apiKeyAuth,
    jwt,
    oidc,
    cors,
    authorization,
    toggleBasicAuth,
    toggleAPIKeyAuth,
    toggleJWT,
    toggleOIDC,
    toggleCORS,
    toggleAuthorization,
  } = useSecurityPolicyForm();

  const isEnabled = (type: AuthType): boolean => {
    switch (type) {
      case 'basicAuth': return !!basicAuth;
      case 'apiKeyAuth': return !!apiKeyAuth;
      case 'jwt': return !!jwt;
      case 'oidc': return !!oidc;
      case 'cors': return !!cors;
      case 'authorization': return !!authorization;
    }
  };

  const toggle = (type: AuthType, enabled: boolean) => {
    switch (type) {
      case 'basicAuth': toggleBasicAuth(enabled); break;
      case 'apiKeyAuth': toggleAPIKeyAuth(enabled); break;
      case 'jwt': toggleJWT(enabled); break;
      case 'oidc': toggleOIDC(enabled); break;
      case 'cors': toggleCORS(enabled); break;
      case 'authorization': toggleAuthorization(enabled); break;
    }
  };

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium text-gray-900 dark:text-white mb-2">
          Authentication & Security
        </h3>
        <p className="text-sm text-gray-500 dark:text-dark-400 mb-4">
          Enable the security features you want to configure. Multiple types can be enabled simultaneously.
        </p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {AUTH_TYPES.map((type) => (
          <div
            key={type}
            className={`p-4 rounded-lg border transition-colors ${
              isEnabled(type)
                ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
                : 'border-gray-200 dark:border-dark-700 bg-white dark:bg-dark-800'
            }`}
          >
            <Toggle
              label={AUTH_TYPE_LABELS[type]}
              description={AUTH_TYPE_DESCRIPTIONS[type]}
              checked={isEnabled(type)}
              onChange={(e) => toggle(type, e.target.checked)}
            />
          </div>
        ))}
      </div>
    </div>
  );
}

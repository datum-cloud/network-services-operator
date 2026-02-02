'use client';

import { AuthTypeToggle } from '../AuthTypeToggle';
import { BasicAuthForm } from '../auth/BasicAuthForm';
import { APIKeyAuthForm } from '../auth/APIKeyAuthForm';
import { JWTForm } from '../auth/JWTForm';
import { OIDCForm } from '../auth/OIDCForm';
import { CORSForm } from '../auth/CORSForm';
import { AuthorizationForm } from '../auth/AuthorizationForm';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';

export function AuthenticationStep() {
  const { basicAuth, apiKeyAuth, jwt, oidc, cors, authorization } = useSecurityPolicyForm();

  const hasAnyAuthEnabled = basicAuth || apiKeyAuth || jwt || oidc || cors || authorization;

  return (
    <div className="space-y-8">
      {/* Auth Type Selector */}
      <AuthTypeToggle />

      {/* Enabled Auth Type Forms */}
      {hasAnyAuthEnabled && (
        <div className="space-y-8 pt-6 border-t border-gray-200 dark:border-dark-700">
          {basicAuth && (
            <div className="p-6 bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-700 rounded-lg">
              <BasicAuthForm />
            </div>
          )}

          {apiKeyAuth && (
            <div className="p-6 bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-700 rounded-lg">
              <APIKeyAuthForm />
            </div>
          )}

          {jwt && (
            <div className="p-6 bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-700 rounded-lg">
              <JWTForm />
            </div>
          )}

          {oidc && (
            <div className="p-6 bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-700 rounded-lg">
              <OIDCForm />
            </div>
          )}

          {cors && (
            <div className="p-6 bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-700 rounded-lg">
              <CORSForm />
            </div>
          )}

          {authorization && (
            <div className="p-6 bg-white dark:bg-dark-900 border border-gray-200 dark:border-dark-700 rounded-lg">
              <AuthorizationForm />
            </div>
          )}
        </div>
      )}

      {/* Empty state */}
      {!hasAnyAuthEnabled && (
        <div className="text-center py-12 bg-gray-50 dark:bg-dark-800 rounded-lg">
          <p className="text-gray-500 dark:text-dark-400">
            Enable at least one authentication or security feature above to configure it.
          </p>
        </div>
      )}
    </div>
  );
}

'use client';

import { Input } from '@/components/forms/Input';
import { TagInput } from '@/components/forms/TagInput';
import { Toggle } from '@/components/forms/Checkbox';
import { SecretRefInput } from '../shared/SecretRefInput';
import { CollapsibleSection } from '@/components/forms/CollapsibleSection';
import { useSecurityPolicyForm } from '@/hooks/useSecurityPolicyForm';
import { MAX_OIDC_SCOPES, MAX_OIDC_RESOURCES, MIN_REFRESH_TOKEN_TTL } from '@/lib/security-policy-validation';

export function OIDCForm() {
  const {
    oidc,
    updateOIDC,
    updateOIDCProvider,
    getErrors,
  } = useSecurityPolicyForm();

  if (!oidc) return null;

  return (
    <div className="space-y-6">
      <div>
        <h4 className="text-sm font-medium text-gray-900 dark:text-white mb-1">
          OpenID Connect (OIDC)
        </h4>
        <p className="text-sm text-gray-500 dark:text-dark-400">
          Configure OAuth 2.0 / OpenID Connect authentication with your identity provider.
        </p>
      </div>

      {/* Provider Configuration */}
      <div className="space-y-4">
        <h5 className="text-sm font-medium text-gray-700 dark:text-dark-300">
          Provider Configuration
        </h5>

        <Input
          label="Issuer URL"
          value={oidc.provider.issuer}
          onChange={(e) => updateOIDCProvider({ issuer: e.target.value })}
          placeholder="https://accounts.google.com"
          hint="OpenID Connect issuer URL"
          error={getErrors('oidc.provider.issuer')[0]}
          required
        />

        <Input
          label="Authorization Endpoint"
          value={oidc.provider.authorizationEndpoint || ''}
          onChange={(e) => updateOIDCProvider({ authorizationEndpoint: e.target.value || undefined })}
          placeholder="https://accounts.google.com/o/oauth2/v2/auth"
          hint="Optional - discovered automatically from issuer if not specified"
        />

        <Input
          label="Token Endpoint"
          value={oidc.provider.tokenEndpoint || ''}
          onChange={(e) => updateOIDCProvider({ tokenEndpoint: e.target.value || undefined })}
          placeholder="https://oauth2.googleapis.com/token"
          hint="Optional - discovered automatically from issuer if not specified"
        />
      </div>

      {/* Client Configuration */}
      <div className="space-y-4">
        <h5 className="text-sm font-medium text-gray-700 dark:text-dark-300">
          Client Configuration
        </h5>

        <Input
          label="Client ID"
          value={oidc.clientID}
          onChange={(e) => updateOIDC({ clientID: e.target.value })}
          placeholder="your-client-id.apps.googleusercontent.com"
          error={getErrors('oidc.clientID')[0]}
          required
        />

        <SecretRefInput
          label="Client Secret"
          value={oidc.clientSecret}
          onChange={(updates) => updateOIDC({ clientSecret: { ...oidc.clientSecret, ...updates } })}
          hint="Secret containing the client secret"
          error={getErrors('oidc.clientSecret.name')[0]}
          required
        />

        <Input
          label="Redirect URL"
          value={oidc.redirectURL || ''}
          onChange={(e) => updateOIDC({ redirectURL: e.target.value || undefined })}
          placeholder="https://app.example.com/oauth2/callback"
          hint="OAuth callback URL - must match provider configuration"
        />

        <Input
          label="Logout Path"
          value={oidc.logoutPath || ''}
          onChange={(e) => updateOIDC({ logoutPath: e.target.value || undefined })}
          placeholder="/logout"
          hint="Path to trigger OIDC logout"
        />
      </div>

      {/* Scopes and Resources */}
      <div className="space-y-4">
        <TagInput
          label={`Scopes (max ${MAX_OIDC_SCOPES})`}
          value={oidc.scopes}
          onChange={(scopes) => updateOIDC({ scopes: scopes.slice(0, MAX_OIDC_SCOPES) })}
          placeholder="openid, profile, email"
          hint="OAuth scopes to request"
          error={getErrors('oidc.scopes')[0]}
        />

        <TagInput
          label={`Resources (max ${MAX_OIDC_RESOURCES})`}
          value={oidc.resources}
          onChange={(resources) => updateOIDC({ resources: resources.slice(0, MAX_OIDC_RESOURCES) })}
          placeholder="https://api.example.com"
          hint="Resource indicators for RFC 8707"
          error={getErrors('oidc.resources')[0]}
        />
      </div>

      {/* Token Settings */}
      <CollapsibleSection title="Token Settings" defaultExpanded={false}>
        <div className="space-y-4">
          <Toggle
            label="Forward Access Token"
            description="Forward the access token to the backend in the Authorization header"
            checked={oidc.forwardAccessToken || false}
            onChange={(e) => updateOIDC({ forwardAccessToken: e.target.checked })}
          />

          <Input
            label="Default Token TTL"
            value={oidc.defaultTokenTTL || ''}
            onChange={(e) => updateOIDC({ defaultTokenTTL: e.target.value || undefined })}
            placeholder="1h"
            hint="Default TTL for access tokens (e.g., 30m, 1h)"
          />

          <Toggle
            label="Enable Refresh Token"
            description="Use refresh tokens for long-lived sessions"
            checked={oidc.refreshToken || false}
            onChange={(e) => updateOIDC({ refreshToken: e.target.checked })}
          />

          {oidc.refreshToken && (
            <Input
              label="Default Refresh Token TTL"
              value={oidc.defaultRefreshTokenTTL || ''}
              onChange={(e) => updateOIDC({ defaultRefreshTokenTTL: e.target.value || undefined })}
              placeholder="24h"
              hint={`Minimum TTL is ${MIN_REFRESH_TOKEN_TTL}`}
              error={getErrors('oidc.defaultRefreshTokenTTL')[0]}
            />
          )}

          <Input
            label="Cookie Domain"
            value={oidc.cookieDomain || ''}
            onChange={(e) => updateOIDC({ cookieDomain: e.target.value || undefined })}
            placeholder=".example.com"
            hint="Domain for OIDC session cookies"
          />
        </div>
      </CollapsibleSection>
    </div>
  );
}

'use client';

import { Input } from '@/components/forms/Input';
import type { SecretRefFormState } from '@/lib/security-policy-defaults';

interface SecretRefInputProps {
  value: SecretRefFormState;
  onChange: (updates: Partial<SecretRefFormState>) => void;
  label?: string;
  hint?: string;
  error?: string;
  required?: boolean;
}

export function SecretRefInput({
  value,
  onChange,
  label = 'Secret Reference',
  hint = 'Secret must exist in the same namespace as the SecurityPolicy',
  error,
  required = false,
}: SecretRefInputProps) {
  return (
    <Input
      label={label}
      value={value.name}
      onChange={(e) => onChange({ name: e.target.value })}
      placeholder="secret-name"
      hint={hint}
      error={error}
      required={required}
    />
  );
}

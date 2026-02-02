'use client';

import { Input } from '@/components/forms/Input';
import { Select } from '@/components/forms/Select';
import { STRING_MATCH_TYPES, type StringMatchFormState } from '@/lib/security-policy-defaults';

interface StringMatchInputProps {
  value: StringMatchFormState;
  onChange: (updates: Partial<StringMatchFormState>) => void;
  label?: string;
  valuePlaceholder?: string;
  error?: string;
}

export function StringMatchInput({
  value,
  onChange,
  label,
  valuePlaceholder = 'Enter value',
  error,
}: StringMatchInputProps) {
  return (
    <div className="flex gap-2 items-start">
      <div className="w-32 flex-shrink-0">
        <Select
          label={label ? 'Match Type' : undefined}
          value={value.type}
          onChange={(e) => onChange({ type: e.target.value as StringMatchFormState['type'] })}
          options={STRING_MATCH_TYPES.map(t => ({ value: t.value, label: t.label }))}
        />
      </div>
      <div className="flex-1">
        <Input
          label={label}
          value={value.value}
          onChange={(e) => onChange({ value: e.target.value })}
          placeholder={valuePlaceholder}
          error={error}
        />
      </div>
    </div>
  );
}

'use client';

import { Select } from '@/components/forms/Select';
import { Input } from '@/components/forms/Input';
import { PATH_MATCH_TYPES, PathMatchType } from '@/lib/gateway-defaults';
import type { HTTPPathMatch } from '@/api/types';

interface PathMatchEditorProps {
  value?: HTTPPathMatch;
  onChange: (value: HTTPPathMatch | undefined) => void;
  error?: string;
}

const pathTypeDescriptions: Record<PathMatchType, string> = {
  Exact: 'Matches the path exactly',
  PathPrefix: 'Matches paths starting with this prefix',
  RegularExpression: 'Matches paths using a regex pattern',
};

export function PathMatchEditor({ value, onChange, error }: PathMatchEditorProps) {
  const pathTypeOptions = PATH_MATCH_TYPES.map((type) => ({
    value: type,
    label: type === 'RegularExpression' ? 'Regex' : type,
  }));

  const handleTypeChange = (type: string) => {
    if (!value) {
      onChange({ type: type as PathMatchType, value: '' });
    } else {
      onChange({ ...value, type: type as PathMatchType });
    }
  };

  const handleValueChange = (newValue: string) => {
    if (!value) {
      onChange({ type: 'PathPrefix', value: newValue });
    } else {
      onChange({ ...value, value: newValue });
    }
  };

  const handleClear = () => {
    onChange(undefined);
  };

  if (!value) {
    return (
      <button
        type="button"
        onClick={() => onChange({ type: 'PathPrefix', value: '/' })}
        className="w-full p-4 border-2 border-dashed border-gray-300 dark:border-dark-600 rounded-lg text-sm text-gray-500 dark:text-dark-400 hover:border-primary-500 hover:text-primary-500 transition-colors"
      >
        + Add path match
      </button>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-start gap-3">
        <div className="w-40 flex-shrink-0">
          <Select
            label="Match Type"
            value={value.type}
            onChange={(e) => handleTypeChange(e.target.value)}
            options={pathTypeOptions}
          />
        </div>
        <div className="flex-1">
          <Input
            label="Path Value"
            value={value.value}
            onChange={(e) => handleValueChange(e.target.value)}
            placeholder={value.type === 'RegularExpression' ? '^/api/v[0-9]+/.*' : '/api/v1'}
            error={error}
          />
        </div>
        <button
          type="button"
          onClick={handleClear}
          className="mt-6 p-2 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
          title="Remove path match"
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </div>
      <p className="text-xs text-gray-500 dark:text-dark-400">
        {pathTypeDescriptions[value.type]}
        {value.type === 'RegularExpression' && (
          <span className="ml-1 text-yellow-600 dark:text-yellow-400">
            (Use valid regex syntax)
          </span>
        )}
      </p>
    </div>
  );
}

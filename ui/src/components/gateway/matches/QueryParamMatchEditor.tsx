'use client';

import { Plus, X } from 'lucide-react';
import { Input } from '@/components/forms/Input';
import { Select } from '@/components/forms/Select';
import { Button } from '@/components/common/Button';
import { HEADER_MATCH_TYPES, HeaderMatchType } from '@/lib/gateway-defaults';
import type { QueryParamMatchFormState } from '@/lib/gateway-defaults';

interface QueryParamMatchEditorProps {
  queryParams: QueryParamMatchFormState[];
  onAdd: () => void;
  onRemove: (paramId: string) => void;
  onUpdate: (paramId: string, param: Partial<QueryParamMatchFormState>) => void;
  errors?: string[];
}

export function QueryParamMatchEditor({
  queryParams,
  onAdd,
  onRemove,
  onUpdate,
  errors = [],
}: QueryParamMatchEditorProps) {
  const typeOptions = HEADER_MATCH_TYPES.map((type) => ({
    value: type,
    label: type === 'RegularExpression' ? 'Regex' : type,
  }));

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <label className="block text-sm font-medium text-gray-700 dark:text-dark-300">
          Query Parameter Matches
        </label>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon={<Plus className="w-4 h-4" />}
          onClick={onAdd}
        >
          Add Parameter
        </Button>
      </div>

      {queryParams.length === 0 ? (
        <p className="text-sm text-gray-500 dark:text-dark-400 py-2">
          No query parameter matches configured. Click "Add Parameter" to match requests by URL query parameters.
        </p>
      ) : (
        <div className="space-y-3">
          {queryParams.map((param) => (
            <div key={param.id} className="flex items-start gap-2">
              <div className="flex-1 grid grid-cols-3 gap-2">
                <Input
                  placeholder="Parameter name"
                  value={param.name}
                  onChange={(e) => onUpdate(param.id, { name: e.target.value })}
                />
                <Select
                  value={param.type}
                  onChange={(e) => onUpdate(param.id, { type: e.target.value as HeaderMatchType })}
                  options={typeOptions}
                />
                <Input
                  placeholder={param.type === 'RegularExpression' ? 'Regex pattern' : 'Parameter value'}
                  value={param.value}
                  onChange={(e) => onUpdate(param.id, { value: e.target.value })}
                />
              </div>
              <button
                type="button"
                onClick={() => onRemove(param.id)}
                className="mt-2 p-1 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
                title="Remove query parameter match"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
          ))}
        </div>
      )}

      {queryParams.length > 0 && (
        <p className="text-xs text-gray-500 dark:text-dark-400">
          Multiple query parameter matches are combined with AND logic (all must match)
        </p>
      )}

      {errors.length > 0 && (
        <div className="text-sm text-red-500">
          {errors.map((error, idx) => (
            <p key={idx}>{error}</p>
          ))}
        </div>
      )}
    </div>
  );
}

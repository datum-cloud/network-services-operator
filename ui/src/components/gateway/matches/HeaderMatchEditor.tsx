'use client';

import { Plus, X } from 'lucide-react';
import { Input } from '@/components/forms/Input';
import { Select } from '@/components/forms/Select';
import { Button } from '@/components/common/Button';
import { HEADER_MATCH_TYPES, HeaderMatchType } from '@/lib/gateway-defaults';
import type { HeaderMatchFormState } from '@/lib/gateway-defaults';

interface HeaderMatchEditorProps {
  headers: HeaderMatchFormState[];
  onAdd: () => void;
  onRemove: (headerId: string) => void;
  onUpdate: (headerId: string, header: Partial<HeaderMatchFormState>) => void;
  errors?: string[];
}

export function HeaderMatchEditor({
  headers,
  onAdd,
  onRemove,
  onUpdate,
  errors = [],
}: HeaderMatchEditorProps) {
  const typeOptions = HEADER_MATCH_TYPES.map((type) => ({
    value: type,
    label: type === 'RegularExpression' ? 'Regex' : type,
  }));

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <label className="block text-sm font-medium text-gray-700 dark:text-dark-300">
          Header Matches
        </label>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          icon={<Plus className="w-4 h-4" />}
          onClick={onAdd}
        >
          Add Header
        </Button>
      </div>

      {headers.length === 0 ? (
        <p className="text-sm text-gray-500 dark:text-dark-400 py-2">
          No header matches configured. Click "Add Header" to match requests by header values.
        </p>
      ) : (
        <div className="space-y-3">
          {headers.map((header) => (
            <div key={header.id} className="flex items-start gap-2">
              <div className="flex-1 grid grid-cols-3 gap-2">
                <Input
                  placeholder="Header name"
                  value={header.name}
                  onChange={(e) => onUpdate(header.id, { name: e.target.value })}
                />
                <Select
                  value={header.type}
                  onChange={(e) => onUpdate(header.id, { type: e.target.value as HeaderMatchType })}
                  options={typeOptions}
                />
                <Input
                  placeholder={header.type === 'RegularExpression' ? 'Regex pattern' : 'Header value'}
                  value={header.value}
                  onChange={(e) => onUpdate(header.id, { value: e.target.value })}
                />
              </div>
              <button
                type="button"
                onClick={() => onRemove(header.id)}
                className="mt-2 p-1 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
                title="Remove header match"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
          ))}
        </div>
      )}

      {headers.length > 0 && (
        <p className="text-xs text-gray-500 dark:text-dark-400">
          Multiple header matches are combined with AND logic (all must match)
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

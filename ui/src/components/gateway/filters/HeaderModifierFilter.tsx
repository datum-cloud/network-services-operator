'use client';

import { Plus, X } from 'lucide-react';
import { Input } from '@/components/forms/Input';
import { Button } from '@/components/common/Button';
import { TagInput } from '@/components/forms/TagInput';
import type { HeaderModifierFormState, HTTPHeaderFormState } from '@/lib/gateway-defaults';

interface HeaderModifierFilterProps {
  modifier: HeaderModifierFormState;
  filterType: 'RequestHeaderModifier' | 'ResponseHeaderModifier';
  ruleId: string;
  filterId: string;
  onAddHeader: (ruleId: string, filterId: string, section: 'set' | 'add') => void;
  onRemoveHeader: (ruleId: string, filterId: string, section: 'set' | 'add', headerId: string) => void;
  onUpdateHeader: (ruleId: string, filterId: string, section: 'set' | 'add', headerId: string, header: Partial<HTTPHeaderFormState>) => void;
  onAddRemoveHeader: (ruleId: string, filterId: string, headerName: string) => void;
  onRemoveRemoveHeader: (ruleId: string, filterId: string, headerName: string) => void;
}

export function HeaderModifierFilter({
  modifier,
  filterType,
  ruleId,
  filterId,
  onAddHeader,
  onRemoveHeader,
  onUpdateHeader,
  onAddRemoveHeader,
  onRemoveRemoveHeader,
}: HeaderModifierFilterProps) {
  const label = filterType === 'RequestHeaderModifier' ? 'Request' : 'Response';

  const handleRemoveTagChange = (headers: string[]) => {
    // Find removed headers
    const removed = modifier.remove.filter(h => !headers.includes(h));
    removed.forEach(h => onRemoveRemoveHeader(ruleId, filterId, h));

    // Find added headers
    const added = headers.filter(h => !modifier.remove.includes(h));
    added.forEach(h => onAddRemoveHeader(ruleId, filterId, h));
  };

  return (
    <div className="space-y-6">
      <p className="text-sm text-gray-500 dark:text-dark-400">
        Modify {label.toLowerCase()} headers before {filterType === 'RequestHeaderModifier' ? 'forwarding to backend' : 'returning to client'}
      </p>

      {/* Set Headers */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
            Set Headers
          </label>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={() => onAddHeader(ruleId, filterId, 'set')}
          >
            Add
          </Button>
        </div>
        <p className="text-xs text-gray-500 dark:text-dark-400">
          Set a header to a specific value (overwrites if exists)
        </p>
        {modifier.set.length === 0 ? (
          <p className="text-sm text-gray-400 dark:text-dark-500 italic py-2">No headers to set</p>
        ) : (
          <div className="space-y-2">
            {modifier.set.map((header) => (
              <div key={header.id} className="flex items-center gap-2">
                <Input
                  placeholder="Header name"
                  value={header.name}
                  onChange={(e) => onUpdateHeader(ruleId, filterId, 'set', header.id, { name: e.target.value })}
                  className="flex-1"
                />
                <Input
                  placeholder="Header value"
                  value={header.value}
                  onChange={(e) => onUpdateHeader(ruleId, filterId, 'set', header.id, { value: e.target.value })}
                  className="flex-1"
                />
                <button
                  type="button"
                  onClick={() => onRemoveHeader(ruleId, filterId, 'set', header.id)}
                  className="p-2 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Add Headers */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
            Add Headers
          </label>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={() => onAddHeader(ruleId, filterId, 'add')}
          >
            Add
          </Button>
        </div>
        <p className="text-xs text-gray-500 dark:text-dark-400">
          Add a header value (appends to existing headers)
        </p>
        {modifier.add.length === 0 ? (
          <p className="text-sm text-gray-400 dark:text-dark-500 italic py-2">No headers to add</p>
        ) : (
          <div className="space-y-2">
            {modifier.add.map((header) => (
              <div key={header.id} className="flex items-center gap-2">
                <Input
                  placeholder="Header name"
                  value={header.name}
                  onChange={(e) => onUpdateHeader(ruleId, filterId, 'add', header.id, { name: e.target.value })}
                  className="flex-1"
                />
                <Input
                  placeholder="Header value"
                  value={header.value}
                  onChange={(e) => onUpdateHeader(ruleId, filterId, 'add', header.id, { value: e.target.value })}
                  className="flex-1"
                />
                <button
                  type="button"
                  onClick={() => onRemoveHeader(ruleId, filterId, 'add', header.id)}
                  className="p-2 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Remove Headers */}
      <div className="space-y-3">
        <label className="text-sm font-medium text-gray-700 dark:text-dark-300">
          Remove Headers
        </label>
        <p className="text-xs text-gray-500 dark:text-dark-400">
          Remove headers by name (if they exist)
        </p>
        <TagInput
          value={modifier.remove}
          onChange={handleRemoveTagChange}
          placeholder="Type header name and press Enter"
          hint="e.g., X-Internal-Header, Authorization"
        />
      </div>
    </div>
  );
}

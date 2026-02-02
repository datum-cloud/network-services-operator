'use client';

import { ChevronDown, ChevronRight, Copy, Trash2, ArrowUp, ArrowDown, GripVertical } from 'lucide-react';
import { Input } from '@/components/forms/Input';
import type { RuleFormState } from '@/lib/gateway-defaults';

interface RuleCardProps {
  rule: RuleFormState;
  index: number;
  isExpanded: boolean;
  isFirst: boolean;
  isLast: boolean;
  summary: string;
  onToggle: () => void;
  onDuplicate: () => void;
  onRemove: () => void;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onNameChange: (name: string) => void;
  children: React.ReactNode;
  hasError?: boolean;
}

export function RuleCard({
  rule,
  index,
  isExpanded,
  isFirst,
  isLast,
  summary,
  onToggle,
  onDuplicate,
  onRemove,
  onMoveUp,
  onMoveDown,
  onNameChange,
  children,
  hasError = false,
}: RuleCardProps) {
  const displayName = rule.name || `Rule ${index + 1}`;

  return (
    <div
      className={`border rounded-lg overflow-hidden ${
        hasError
          ? 'border-red-300 dark:border-red-800'
          : 'border-gray-200 dark:border-dark-700'
      }`}
    >
      {/* Header */}
      <div
        className={`flex items-center gap-3 p-4 cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-800/50 transition-colors ${
          isExpanded ? 'border-b border-gray-200 dark:border-dark-700' : ''
        }`}
        onClick={onToggle}
      >
        {/* Drag Handle (visual only for now) */}
        <div className="text-gray-400 dark:text-dark-500">
          <GripVertical className="w-4 h-4" />
        </div>

        {/* Expand/Collapse Icon */}
        {isExpanded ? (
          <ChevronDown className="w-4 h-4 text-gray-400 dark:text-dark-500 flex-shrink-0" />
        ) : (
          <ChevronRight className="w-4 h-4 text-gray-400 dark:text-dark-500 flex-shrink-0" />
        )}

        {/* Error indicator */}
        {hasError && (
          <span className="w-2 h-2 rounded-full bg-red-500 flex-shrink-0" />
        )}

        {/* Title and Summary */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-medium text-gray-900 dark:text-white">
              {displayName}
            </span>
            {rule.name && (
              <span className="text-xs text-gray-500 dark:text-dark-400">
                (#{index + 1})
              </span>
            )}
            {rule.matches.length > 0 && (
              <span className="text-xs px-2 py-0.5 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded">
                {rule.matches.length} match{rule.matches.length !== 1 ? 'es' : ''}
              </span>
            )}
            {rule.filters.length > 0 && (
              <span className="text-xs px-2 py-0.5 bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-300 rounded">
                {rule.filters.length} filter{rule.filters.length !== 1 ? 's' : ''}
              </span>
            )}
            <span className="text-xs px-2 py-0.5 bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300 rounded">
              {rule.backends.length} backend{rule.backends.length !== 1 ? 's' : ''}
            </span>
          </div>
          {!isExpanded && (
            <p className="text-sm text-gray-500 dark:text-dark-400 truncate mt-1">
              {summary}
            </p>
          )}
        </div>

        {/* Actions */}
        <div
          className="flex items-center gap-1 flex-shrink-0"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Move buttons */}
          <button
            type="button"
            onClick={onMoveUp}
            disabled={isFirst}
            className="p-1.5 text-gray-400 hover:text-gray-600 dark:hover:text-dark-200 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
            title="Move up"
          >
            <ArrowUp className="w-4 h-4" />
          </button>
          <button
            type="button"
            onClick={onMoveDown}
            disabled={isLast}
            className="p-1.5 text-gray-400 hover:text-gray-600 dark:hover:text-dark-200 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
            title="Move down"
          >
            <ArrowDown className="w-4 h-4" />
          </button>

          {/* Duplicate */}
          <button
            type="button"
            onClick={onDuplicate}
            className="p-1.5 text-gray-400 hover:text-gray-600 dark:hover:text-dark-200 transition-colors"
            title="Duplicate rule"
          >
            <Copy className="w-4 h-4" />
          </button>

          {/* Delete */}
          <button
            type="button"
            onClick={onRemove}
            className="p-1.5 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
            title="Delete rule"
          >
            <Trash2 className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Content */}
      {isExpanded && (
        <div className="p-4 bg-white dark:bg-dark-900 space-y-6">
          {/* Rule Name */}
          <Input
            label="Rule Name"
            value={rule.name}
            onChange={(e) => onNameChange(e.target.value)}
            placeholder="e.g., api-v1-routes, static-assets"
            hint="Optional. Give this rule a descriptive name for easier identification."
          />

          {/* Rest of the rule configuration */}
          {children}
        </div>
      )}
    </div>
  );
}

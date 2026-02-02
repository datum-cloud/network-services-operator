'use client';

import { Plus, X, Filter } from 'lucide-react';
import { Select } from '@/components/forms/Select';
import { Button } from '@/components/common/Button';
import { CollapsibleSection } from '@/components/forms/CollapsibleSection';
import { HeaderModifierFilter } from './HeaderModifierFilter';
import { URLRewriteFilter } from './URLRewriteFilter';
import { RequestRedirectFilter } from './RequestRedirectFilter';
import type { FilterFormState, RuleFormState, HTTPHeaderFormState } from '@/lib/gateway-defaults';
import type { HTTPFilter } from '@/api/types';

interface FilterBuilderProps {
  filters: FilterFormState[];
  ruleId: string;
  rule: RuleFormState;
  expandedFilters: Set<string>;
  onToggleFilter: (filterId: string) => void;
  onAddFilter: (ruleId: string, filterType: HTTPFilter['type']) => void;
  onRemoveFilter: (ruleId: string, filterId: string) => void;
  onUpdateFilter: (ruleId: string, filterId: string, filter: Partial<FilterFormState>) => void;
  onAddHeaderToModifier: (ruleId: string, filterId: string, section: 'set' | 'add') => void;
  onRemoveHeaderFromModifier: (ruleId: string, filterId: string, section: 'set' | 'add', headerId: string) => void;
  onUpdateHeaderInModifier: (ruleId: string, filterId: string, section: 'set' | 'add', headerId: string, header: Partial<HTTPHeaderFormState>) => void;
  onAddRemoveHeader: (ruleId: string, filterId: string, headerName: string) => void;
  onRemoveRemoveHeader: (ruleId: string, filterId: string, headerName: string) => void;
  canAddFilter: (rule: RuleFormState, filterType: HTTPFilter['type']) => boolean;
  getAvailableFilterTypes: (rule: RuleFormState) => HTTPFilter['type'][];
  getFilterSummary: (filter: FilterFormState) => string;
  errors?: string[];
}

const filterTypeLabels: Record<HTTPFilter['type'], string> = {
  RequestHeaderModifier: 'Request Header Modifier',
  ResponseHeaderModifier: 'Response Header Modifier',
  URLRewrite: 'URL Rewrite',
  RequestRedirect: 'Request Redirect',
};

const filterTypeDescriptions: Record<HTTPFilter['type'], string> = {
  RequestHeaderModifier: 'Modify headers sent to the backend',
  ResponseHeaderModifier: 'Modify headers in the response',
  URLRewrite: 'Rewrite URL before forwarding',
  RequestRedirect: 'Redirect to a different URL',
};

export function FilterBuilder({
  filters,
  ruleId,
  rule,
  expandedFilters,
  onToggleFilter,
  onAddFilter,
  onRemoveFilter,
  onUpdateFilter,
  onAddHeaderToModifier,
  onRemoveHeaderFromModifier,
  onUpdateHeaderInModifier,
  onAddRemoveHeader,
  onRemoveRemoveHeader,
  canAddFilter,
  getAvailableFilterTypes,
  getFilterSummary,
  errors = [],
}: FilterBuilderProps) {
  const availableTypes = getAvailableFilterTypes(rule);

  const handleAddFilter = (type: string) => {
    if (type) {
      onAddFilter(ruleId, type as HTTPFilter['type']);
    }
  };

  const renderFilterContent = (filter: FilterFormState) => {
    switch (filter.type) {
      case 'RequestHeaderModifier':
      case 'ResponseHeaderModifier':
        const modifier = filter.type === 'RequestHeaderModifier'
          ? filter.requestHeaderModifier
          : filter.responseHeaderModifier;
        if (!modifier) return null;
        return (
          <HeaderModifierFilter
            modifier={modifier}
            filterType={filter.type}
            ruleId={ruleId}
            filterId={filter.id}
            onAddHeader={onAddHeaderToModifier}
            onRemoveHeader={onRemoveHeaderFromModifier}
            onUpdateHeader={onUpdateHeaderInModifier}
            onAddRemoveHeader={onAddRemoveHeader}
            onRemoveRemoveHeader={onRemoveRemoveHeader}
          />
        );
      case 'URLRewrite':
        return (
          <URLRewriteFilter
            filter={filter}
            ruleId={ruleId}
            onUpdate={onUpdateFilter}
          />
        );
      case 'RequestRedirect':
        return (
          <RequestRedirectFilter
            filter={filter}
            ruleId={ruleId}
            onUpdate={onUpdateFilter}
          />
        );
      default:
        return null;
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium text-gray-900 dark:text-white flex items-center gap-2">
          <Filter className="w-4 h-4" />
          Filters ({filters.length})
        </h4>
        {availableTypes.length > 0 && (
          <div className="flex items-center gap-2">
            <Select
              value=""
              onChange={(e) => handleAddFilter(e.target.value)}
              options={[
                { value: '', label: 'Add filter...' },
                ...availableTypes.map((type) => ({
                  value: type,
                  label: filterTypeLabels[type],
                })),
              ]}
              className="w-48"
            />
          </div>
        )}
      </div>

      {filters.length === 0 ? (
        <div className="text-center py-8 bg-gray-50 dark:bg-dark-800 rounded-lg">
          <Filter className="w-8 h-8 mx-auto text-gray-400 dark:text-dark-500 mb-2" />
          <p className="text-sm text-gray-500 dark:text-dark-400">No filters configured</p>
          <p className="text-xs text-gray-400 dark:text-dark-500 mt-1">
            Add filters to modify requests, responses, or redirect traffic
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {filters.map((filter) => (
            <CollapsibleSection
              key={filter.id}
              title={filterTypeLabels[filter.type]}
              summary={getFilterSummary(filter)}
              expanded={expandedFilters.has(filter.id)}
              onToggle={() => onToggleFilter(filter.id)}
              actions={
                <button
                  type="button"
                  onClick={() => onRemoveFilter(ruleId, filter.id)}
                  className="p-1 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
                  title="Remove filter"
                >
                  <X className="w-4 h-4" />
                </button>
              }
            >
              {renderFilterContent(filter)}
            </CollapsibleSection>
          ))}
        </div>
      )}

      {/* Constraints warnings */}
      {filters.some(f => f.type === 'RequestRedirect') && (
        <p className="text-xs text-yellow-600 dark:text-yellow-400 flex items-center gap-1">
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
          RequestRedirect cannot be combined with URLRewrite
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

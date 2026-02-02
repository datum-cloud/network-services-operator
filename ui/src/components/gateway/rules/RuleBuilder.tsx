'use client';

import { Plus, X, Target } from 'lucide-react';
import { Button } from '@/components/common/Button';
import { CollapsibleSection } from '@/components/forms/CollapsibleSection';
import { PathMatchEditor } from '../matches/PathMatchEditor';
import { MethodSelector } from '../matches/MethodSelector';
import { HeaderMatchEditor } from '../matches/HeaderMatchEditor';
import { QueryParamMatchEditor } from '../matches/QueryParamMatchEditor';
import { FilterBuilder } from '../filters/FilterBuilder';
import { BackendEditor } from '../BackendEditor';
import type {
  RuleFormState,
  MatchFormState,
  FilterFormState,
  BackendFormState,
  HeaderMatchFormState,
  QueryParamMatchFormState,
  HTTPHeaderFormState,
} from '@/lib/gateway-defaults';
import type { HTTPPathMatch, HTTPFilter, Connector } from '@/api/types';

interface RuleBuilderProps {
  rule: RuleFormState;
  ruleIndex: number;
  expandedMatches: Set<string>;
  expandedFilters: Set<string>;
  connectors?: Connector[];
  // Match actions
  onAddMatch: (ruleId: string) => void;
  onRemoveMatch: (ruleId: string, matchId: string) => void;
  onUpdateMatchPath: (ruleId: string, matchId: string, path: HTTPPathMatch | undefined) => void;
  onUpdateMatchMethod: (ruleId: string, matchId: string, method: string | undefined) => void;
  onToggleMatchExpanded: (matchId: string) => void;
  // Header match actions
  onAddHeaderMatch: (ruleId: string, matchId: string) => void;
  onRemoveHeaderMatch: (ruleId: string, matchId: string, headerId: string) => void;
  onUpdateHeaderMatch: (ruleId: string, matchId: string, headerId: string, header: Partial<HeaderMatchFormState>) => void;
  // Query param match actions
  onAddQueryParamMatch: (ruleId: string, matchId: string) => void;
  onRemoveQueryParamMatch: (ruleId: string, matchId: string, paramId: string) => void;
  onUpdateQueryParamMatch: (ruleId: string, matchId: string, paramId: string, param: Partial<QueryParamMatchFormState>) => void;
  // Filter actions
  onToggleFilterExpanded: (filterId: string) => void;
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
  // Backend actions
  onAddBackend: (ruleId: string) => void;
  onRemoveBackend: (ruleId: string, backendId: string) => void;
  onUpdateBackend: (ruleId: string, backendId: string, backend: Partial<BackendFormState>) => void;
  // Summaries
  getMatchSummary: (match: MatchFormState) => string;
  // Errors
  errors?: string[];
}

export function RuleBuilder({
  rule,
  ruleIndex,
  expandedMatches,
  expandedFilters,
  connectors = [],
  onAddMatch,
  onRemoveMatch,
  onUpdateMatchPath,
  onUpdateMatchMethod,
  onToggleMatchExpanded,
  onAddHeaderMatch,
  onRemoveHeaderMatch,
  onUpdateHeaderMatch,
  onAddQueryParamMatch,
  onRemoveQueryParamMatch,
  onUpdateQueryParamMatch,
  onToggleFilterExpanded,
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
  onAddBackend,
  onRemoveBackend,
  onUpdateBackend,
  getMatchSummary,
  errors = [],
}: RuleBuilderProps) {
  return (
    <div className="space-y-6">
      {/* Match Conditions */}
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <h4 className="text-sm font-medium text-gray-900 dark:text-white flex items-center gap-2">
            <Target className="w-4 h-4" />
            Match Conditions ({rule.matches.length})
          </h4>
          <Button
            type="button"
            variant="outline"
            size="sm"
            icon={<Plus className="w-4 h-4" />}
            onClick={() => onAddMatch(rule.id)}
          >
            Add Match
          </Button>
        </div>

        {rule.matches.length === 0 ? (
          <div className="text-center py-8 bg-gray-50 dark:bg-dark-800 rounded-lg">
            <Target className="w-8 h-8 mx-auto text-gray-400 dark:text-dark-500 mb-2" />
            <p className="text-sm text-gray-500 dark:text-dark-400">No match conditions</p>
            <p className="text-xs text-gray-400 dark:text-dark-500 mt-1">
              This rule will match all requests. Add conditions to filter specific traffic.
            </p>
          </div>
        ) : (
          <div className="space-y-3">
            {rule.matches.map((match, matchIndex) => (
              <CollapsibleSection
                key={match.id}
                title={`Match ${matchIndex + 1}`}
                summary={getMatchSummary(match)}
                expanded={expandedMatches.has(match.id)}
                onToggle={() => onToggleMatchExpanded(match.id)}
                actions={
                  <button
                    type="button"
                    onClick={() => onRemoveMatch(rule.id, match.id)}
                    className="p-1 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
                    title="Remove match"
                  >
                    <X className="w-4 h-4" />
                  </button>
                }
              >
                <div className="space-y-6">
                  {/* Path Match */}
                  <PathMatchEditor
                    value={match.path}
                    onChange={(path) => onUpdateMatchPath(rule.id, match.id, path)}
                  />

                  {/* Method Selector */}
                  <MethodSelector
                    value={match.method}
                    onChange={(method) => onUpdateMatchMethod(rule.id, match.id, method)}
                  />

                  {/* Header Matches */}
                  <HeaderMatchEditor
                    headers={match.headers}
                    onAdd={() => onAddHeaderMatch(rule.id, match.id)}
                    onRemove={(headerId) => onRemoveHeaderMatch(rule.id, match.id, headerId)}
                    onUpdate={(headerId, header) => onUpdateHeaderMatch(rule.id, match.id, headerId, header)}
                  />

                  {/* Query Param Matches */}
                  <QueryParamMatchEditor
                    queryParams={match.queryParams}
                    onAdd={() => onAddQueryParamMatch(rule.id, match.id)}
                    onRemove={(paramId) => onRemoveQueryParamMatch(rule.id, match.id, paramId)}
                    onUpdate={(paramId, param) => onUpdateQueryParamMatch(rule.id, match.id, paramId, param)}
                  />
                </div>
              </CollapsibleSection>
            ))}
          </div>
        )}

        {rule.matches.length > 1 && (
          <p className="text-xs text-gray-500 dark:text-dark-400">
            Multiple matches are combined with OR logic (any match will route to this rule)
          </p>
        )}
      </div>

      {/* Divider */}
      <hr className="border-gray-200 dark:border-dark-700" />

      {/* Filters */}
      <FilterBuilder
        filters={rule.filters}
        ruleId={rule.id}
        rule={rule}
        expandedFilters={expandedFilters}
        onToggleFilter={onToggleFilterExpanded}
        onAddFilter={onAddFilter}
        onRemoveFilter={onRemoveFilter}
        onUpdateFilter={onUpdateFilter}
        onAddHeaderToModifier={onAddHeaderToModifier}
        onRemoveHeaderFromModifier={onRemoveHeaderFromModifier}
        onUpdateHeaderInModifier={onUpdateHeaderInModifier}
        onAddRemoveHeader={onAddRemoveHeader}
        onRemoveRemoveHeader={onRemoveRemoveHeader}
        canAddFilter={canAddFilter}
        getAvailableFilterTypes={getAvailableFilterTypes}
        getFilterSummary={getFilterSummary}
      />

      {/* Divider */}
      <hr className="border-gray-200 dark:border-dark-700" />

      {/* Backends */}
      <BackendEditor
        backends={rule.backends}
        ruleId={rule.id}
        connectors={connectors}
        onAdd={onAddBackend}
        onRemove={onRemoveBackend}
        onUpdate={onUpdateBackend}
      />

      {errors.length > 0 && (
        <div className="p-3 bg-red-50 dark:bg-red-900/20 rounded-lg">
          <p className="text-sm font-medium text-red-700 dark:text-red-300 mb-1">Validation Errors</p>
          <ul className="text-sm text-red-600 dark:text-red-400 list-disc list-inside">
            {errors.map((error, idx) => (
              <li key={idx}>{error}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

'use client';

import { Plus, Route } from 'lucide-react';
import { Button } from '@/components/common/Button';
import { RuleCard } from './RuleCard';
import { RuleBuilder } from './RuleBuilder';
import { MAX_RULES } from '@/lib/gateway-validation';
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

interface RulesListProps {
  rules: RuleFormState[];
  expandedRules: Set<string>;
  expandedMatches: Set<string>;
  expandedFilters: Set<string>;
  connectors?: Connector[];
  // Rule actions
  onAddRule: () => void;
  onRemoveRule: (ruleId: string) => void;
  onDuplicateRule: (ruleId: string) => void;
  onMoveRule: (ruleId: string, direction: 'up' | 'down') => void;
  onToggleRuleExpanded: (ruleId: string) => void;
  onUpdateRuleName: (ruleId: string, name: string) => void;
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
  getRuleSummary: (rule: RuleFormState) => string;
  getMatchSummary: (match: MatchFormState) => string;
  // Errors
  getErrors: (fieldPrefix: string) => string[];
  hasError: (fieldPrefix: string) => boolean;
}

export function RulesList({
  rules,
  expandedRules,
  expandedMatches,
  expandedFilters,
  connectors = [],
  onAddRule,
  onRemoveRule,
  onDuplicateRule,
  onMoveRule,
  onToggleRuleExpanded,
  onUpdateRuleName,
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
  getRuleSummary,
  getMatchSummary,
  getErrors,
  hasError,
}: RulesListProps) {
  const canAddMore = rules.length < MAX_RULES;

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Route className="w-5 h-5 text-gray-400" />
          <h3 className="text-lg font-medium text-gray-900 dark:text-white">
            Routing Rules
          </h3>
          <span className="text-sm text-gray-500 dark:text-dark-400">
            ({rules.length} of {MAX_RULES})
          </span>
        </div>
        <Button
          type="button"
          variant="primary"
          size="sm"
          icon={<Plus className="w-4 h-4" />}
          onClick={onAddRule}
          disabled={!canAddMore}
        >
          Add Rule
        </Button>
      </div>

      {/* Info text */}
      <p className="text-sm text-gray-500 dark:text-dark-400">
        Rules are evaluated in order. The first matching rule handles the request.
        Configure match conditions to route specific traffic patterns.
      </p>

      {/* Rules List */}
      {rules.length === 0 ? (
        <div className="text-center py-12 bg-gray-50 dark:bg-dark-800 rounded-lg">
          <Route className="w-12 h-12 mx-auto text-gray-400 dark:text-dark-500 mb-3" />
          <h4 className="text-lg font-medium text-gray-900 dark:text-white mb-1">
            No routing rules
          </h4>
          <p className="text-sm text-gray-500 dark:text-dark-400 mb-4">
            Add a routing rule to define how traffic should be handled
          </p>
          <Button
            type="button"
            variant="primary"
            icon={<Plus className="w-4 h-4" />}
            onClick={onAddRule}
          >
            Add First Rule
          </Button>
        </div>
      ) : (
        <div className="space-y-4">
          {rules.map((rule, index) => (
            <RuleCard
              key={rule.id}
              rule={rule}
              index={index}
              isExpanded={expandedRules.has(rule.id)}
              isFirst={index === 0}
              isLast={index === rules.length - 1}
              summary={getRuleSummary(rule)}
              onToggle={() => onToggleRuleExpanded(rule.id)}
              onDuplicate={() => onDuplicateRule(rule.id)}
              onRemove={() => onRemoveRule(rule.id)}
              onMoveUp={() => onMoveRule(rule.id, 'up')}
              onMoveDown={() => onMoveRule(rule.id, 'down')}
              onNameChange={(name) => onUpdateRuleName(rule.id, name)}
              hasError={hasError(`rules.${index}`)}
            >
              <RuleBuilder
                rule={rule}
                ruleIndex={index}
                expandedMatches={expandedMatches}
                expandedFilters={expandedFilters}
                connectors={connectors}
                onAddMatch={onAddMatch}
                onRemoveMatch={onRemoveMatch}
                onUpdateMatchPath={onUpdateMatchPath}
                onUpdateMatchMethod={onUpdateMatchMethod}
                onToggleMatchExpanded={onToggleMatchExpanded}
                onAddHeaderMatch={onAddHeaderMatch}
                onRemoveHeaderMatch={onRemoveHeaderMatch}
                onUpdateHeaderMatch={onUpdateHeaderMatch}
                onAddQueryParamMatch={onAddQueryParamMatch}
                onRemoveQueryParamMatch={onRemoveQueryParamMatch}
                onUpdateQueryParamMatch={onUpdateQueryParamMatch}
                onToggleFilterExpanded={onToggleFilterExpanded}
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
                onAddBackend={onAddBackend}
                onRemoveBackend={onRemoveBackend}
                onUpdateBackend={onUpdateBackend}
                getMatchSummary={getMatchSummary}
                errors={getErrors(`rules.${index}`)}
              />
            </RuleCard>
          ))}
        </div>
      )}

      {/* Add more button at bottom */}
      {rules.length > 0 && canAddMore && (
        <button
          type="button"
          onClick={onAddRule}
          className="w-full p-4 border-2 border-dashed border-gray-300 dark:border-dark-600 rounded-lg text-sm text-gray-500 dark:text-dark-400 hover:border-primary-500 hover:text-primary-500 transition-colors flex items-center justify-center gap-2"
        >
          <Plus className="w-4 h-4" />
          Add another rule
        </button>
      )}

      {!canAddMore && (
        <p className="text-sm text-yellow-600 dark:text-yellow-400 text-center">
          Maximum of {MAX_RULES} rules reached
        </p>
      )}
    </div>
  );
}

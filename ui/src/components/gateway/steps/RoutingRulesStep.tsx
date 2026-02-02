'use client';

import { RulesList } from '../rules/RulesList';
import { useGatewayForm } from '@/hooks/useGatewayForm';
import type { Connector } from '@/api/types';

interface RoutingRulesStepProps {
  connectors?: Connector[];
}

export function RoutingRulesStep({ connectors = [] }: RoutingRulesStepProps) {
  const {
    rules,
    expandedRules,
    expandedMatches,
    expandedFilters,
    // Rule actions
    addRule,
    removeRule,
    duplicateRule,
    moveRule,
    toggleRuleExpanded,
    updateRuleName,
    // Match actions
    addMatch,
    removeMatch,
    updateMatchPath,
    updateMatchMethod,
    toggleMatchExpanded,
    // Header match actions
    addHeaderMatch,
    removeHeaderMatch,
    updateHeaderMatch,
    // Query param match actions
    addQueryParamMatch,
    removeQueryParamMatch,
    updateQueryParamMatch,
    // Filter actions
    toggleFilterExpanded,
    addFilter,
    removeFilter,
    updateFilter,
    addHeaderToModifier,
    removeHeaderFromModifier,
    updateHeaderInModifier,
    addRemoveHeader,
    removeRemoveHeader,
    canAddFilter,
    getAvailableFilterTypes,
    getFilterSummary,
    // Backend actions
    addBackend,
    removeBackend,
    updateBackend,
    // Summaries
    getRuleSummary,
    getMatchSummary,
    // Errors
    getErrors,
    hasError,
  } = useGatewayForm();

  return (
    <RulesList
      rules={rules}
      expandedRules={expandedRules}
      expandedMatches={expandedMatches}
      expandedFilters={expandedFilters}
      connectors={connectors}
      onAddRule={addRule}
      onRemoveRule={removeRule}
      onDuplicateRule={duplicateRule}
      onMoveRule={moveRule}
      onToggleRuleExpanded={toggleRuleExpanded}
      onUpdateRuleName={updateRuleName}
      onAddMatch={addMatch}
      onRemoveMatch={removeMatch}
      onUpdateMatchPath={updateMatchPath}
      onUpdateMatchMethod={updateMatchMethod}
      onToggleMatchExpanded={toggleMatchExpanded}
      onAddHeaderMatch={addHeaderMatch}
      onRemoveHeaderMatch={removeHeaderMatch}
      onUpdateHeaderMatch={updateHeaderMatch}
      onAddQueryParamMatch={addQueryParamMatch}
      onRemoveQueryParamMatch={removeQueryParamMatch}
      onUpdateQueryParamMatch={updateQueryParamMatch}
      onToggleFilterExpanded={toggleFilterExpanded}
      onAddFilter={addFilter}
      onRemoveFilter={removeFilter}
      onUpdateFilter={updateFilter}
      onAddHeaderToModifier={addHeaderToModifier}
      onRemoveHeaderFromModifier={removeHeaderFromModifier}
      onUpdateHeaderInModifier={updateHeaderInModifier}
      onAddRemoveHeader={addRemoveHeader}
      onRemoveRemoveHeader={removeRemoveHeader}
      canAddFilter={canAddFilter}
      getAvailableFilterTypes={getAvailableFilterTypes}
      getFilterSummary={getFilterSummary}
      onAddBackend={addBackend}
      onRemoveBackend={removeBackend}
      onUpdateBackend={updateBackend}
      getRuleSummary={getRuleSummary}
      getMatchSummary={getMatchSummary}
      getErrors={getErrors}
      hasError={hasError}
    />
  );
}

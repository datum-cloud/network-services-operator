'use client';

import { useCallback, useMemo } from 'react';
import { useGatewayFormContext } from '@/components/gateway/GatewayFormContext';
import type { HTTPFilter, HTTPPathMatch } from '@/api/types';
import type {
  RuleFormState,
  MatchFormState,
  FilterFormState,
  BackendFormState,
  HeaderMatchFormState,
  QueryParamMatchFormState,
  HTTPHeaderFormState,
} from '@/lib/gateway-defaults';
import { getFieldErrors, hasFieldError } from '@/lib/gateway-validation';

export function useGatewayForm() {
  const { state, dispatch, toHTTPProxy, validate, isValid } = useGatewayFormContext();

  // Metadata helpers
  const setName = useCallback(
    (name: string) => dispatch({ type: 'SET_NAME', payload: name }),
    [dispatch]
  );

  const setNamespace = useCallback(
    (namespace: string) => dispatch({ type: 'SET_NAMESPACE', payload: namespace }),
    [dispatch]
  );

  // Hostname helpers
  const setHostnames = useCallback(
    (hostnames: string[]) => dispatch({ type: 'SET_HOSTNAMES', payload: hostnames }),
    [dispatch]
  );

  const addHostname = useCallback(
    (hostname: string) => dispatch({ type: 'ADD_HOSTNAME', payload: hostname }),
    [dispatch]
  );

  const removeHostname = useCallback(
    (hostname: string) => dispatch({ type: 'REMOVE_HOSTNAME', payload: hostname }),
    [dispatch]
  );

  // Rule helpers
  const addRule = useCallback(() => dispatch({ type: 'ADD_RULE' }), [dispatch]);

  const removeRule = useCallback(
    (ruleId: string) => dispatch({ type: 'REMOVE_RULE', payload: ruleId }),
    [dispatch]
  );

  const duplicateRule = useCallback(
    (ruleId: string) => dispatch({ type: 'DUPLICATE_RULE', payload: ruleId }),
    [dispatch]
  );

  const moveRule = useCallback(
    (ruleId: string, direction: 'up' | 'down') =>
      dispatch({ type: 'MOVE_RULE', payload: { ruleId, direction } }),
    [dispatch]
  );

  const updateRuleName = useCallback(
    (ruleId: string, name: string) =>
      dispatch({ type: 'UPDATE_RULE_NAME', payload: { ruleId, name } }),
    [dispatch]
  );

  // Match helpers
  const addMatch = useCallback(
    (ruleId: string) => dispatch({ type: 'ADD_MATCH', payload: { ruleId } }),
    [dispatch]
  );

  const removeMatch = useCallback(
    (ruleId: string, matchId: string) =>
      dispatch({ type: 'REMOVE_MATCH', payload: { ruleId, matchId } }),
    [dispatch]
  );

  const updateMatchPath = useCallback(
    (ruleId: string, matchId: string, path: HTTPPathMatch | undefined) =>
      dispatch({ type: 'UPDATE_MATCH_PATH', payload: { ruleId, matchId, path } }),
    [dispatch]
  );

  const updateMatchMethod = useCallback(
    (ruleId: string, matchId: string, method: string | undefined) =>
      dispatch({ type: 'UPDATE_MATCH_METHOD', payload: { ruleId, matchId, method } }),
    [dispatch]
  );

  // Header match helpers
  const addHeaderMatch = useCallback(
    (ruleId: string, matchId: string) =>
      dispatch({ type: 'ADD_HEADER_MATCH', payload: { ruleId, matchId } }),
    [dispatch]
  );

  const removeHeaderMatch = useCallback(
    (ruleId: string, matchId: string, headerId: string) =>
      dispatch({ type: 'REMOVE_HEADER_MATCH', payload: { ruleId, matchId, headerId } }),
    [dispatch]
  );

  const updateHeaderMatch = useCallback(
    (ruleId: string, matchId: string, headerId: string, header: Partial<HeaderMatchFormState>) =>
      dispatch({ type: 'UPDATE_HEADER_MATCH', payload: { ruleId, matchId, headerId, header } }),
    [dispatch]
  );

  // Query param match helpers
  const addQueryParamMatch = useCallback(
    (ruleId: string, matchId: string) =>
      dispatch({ type: 'ADD_QUERY_PARAM_MATCH', payload: { ruleId, matchId } }),
    [dispatch]
  );

  const removeQueryParamMatch = useCallback(
    (ruleId: string, matchId: string, paramId: string) =>
      dispatch({ type: 'REMOVE_QUERY_PARAM_MATCH', payload: { ruleId, matchId, paramId } }),
    [dispatch]
  );

  const updateQueryParamMatch = useCallback(
    (ruleId: string, matchId: string, paramId: string, param: Partial<QueryParamMatchFormState>) =>
      dispatch({ type: 'UPDATE_QUERY_PARAM_MATCH', payload: { ruleId, matchId, paramId, param } }),
    [dispatch]
  );

  // Filter helpers
  const addFilter = useCallback(
    (ruleId: string, filterType: HTTPFilter['type']) =>
      dispatch({ type: 'ADD_FILTER', payload: { ruleId, filterType } }),
    [dispatch]
  );

  const removeFilter = useCallback(
    (ruleId: string, filterId: string) =>
      dispatch({ type: 'REMOVE_FILTER', payload: { ruleId, filterId } }),
    [dispatch]
  );

  const updateFilter = useCallback(
    (ruleId: string, filterId: string, filter: Partial<FilterFormState>) =>
      dispatch({ type: 'UPDATE_FILTER', payload: { ruleId, filterId, filter } }),
    [dispatch]
  );

  // Header modifier helpers
  const addHeaderToModifier = useCallback(
    (ruleId: string, filterId: string, section: 'set' | 'add') =>
      dispatch({ type: 'ADD_HEADER_TO_MODIFIER', payload: { ruleId, filterId, section } }),
    [dispatch]
  );

  const removeHeaderFromModifier = useCallback(
    (ruleId: string, filterId: string, section: 'set' | 'add', headerId: string) =>
      dispatch({
        type: 'REMOVE_HEADER_FROM_MODIFIER',
        payload: { ruleId, filterId, section, headerId },
      }),
    [dispatch]
  );

  const updateHeaderInModifier = useCallback(
    (
      ruleId: string,
      filterId: string,
      section: 'set' | 'add',
      headerId: string,
      header: Partial<HTTPHeaderFormState>
    ) =>
      dispatch({
        type: 'UPDATE_HEADER_IN_MODIFIER',
        payload: { ruleId, filterId, section, headerId, header },
      }),
    [dispatch]
  );

  const addRemoveHeader = useCallback(
    (ruleId: string, filterId: string, headerName: string) =>
      dispatch({ type: 'ADD_REMOVE_HEADER', payload: { ruleId, filterId, headerName } }),
    [dispatch]
  );

  const removeRemoveHeader = useCallback(
    (ruleId: string, filterId: string, headerName: string) =>
      dispatch({ type: 'REMOVE_REMOVE_HEADER', payload: { ruleId, filterId, headerName } }),
    [dispatch]
  );

  // Backend helpers
  const addBackend = useCallback(
    (ruleId: string) => dispatch({ type: 'ADD_BACKEND', payload: { ruleId } }),
    [dispatch]
  );

  const removeBackend = useCallback(
    (ruleId: string, backendId: string) =>
      dispatch({ type: 'REMOVE_BACKEND', payload: { ruleId, backendId } }),
    [dispatch]
  );

  const updateBackend = useCallback(
    (ruleId: string, backendId: string, backend: Partial<BackendFormState>) =>
      dispatch({ type: 'UPDATE_BACKEND', payload: { ruleId, backendId, backend } }),
    [dispatch]
  );

  // UI helpers
  const toggleRuleExpanded = useCallback(
    (ruleId: string) => dispatch({ type: 'TOGGLE_RULE_EXPANDED', payload: ruleId }),
    [dispatch]
  );

  const toggleMatchExpanded = useCallback(
    (matchId: string) => dispatch({ type: 'TOGGLE_MATCH_EXPANDED', payload: matchId }),
    [dispatch]
  );

  const toggleFilterExpanded = useCallback(
    (filterId: string) => dispatch({ type: 'TOGGLE_FILTER_EXPANDED', payload: filterId }),
    [dispatch]
  );

  const setFieldTouched = useCallback(
    (field: string) => dispatch({ type: 'SET_FIELD_TOUCHED', payload: field }),
    [dispatch]
  );

  // Reset
  const reset = useCallback(() => dispatch({ type: 'RESET' }), [dispatch]);

  // Validation helpers
  const getErrors = useCallback(
    (fieldPrefix: string) => getFieldErrors(state.validation.errors, fieldPrefix),
    [state.validation.errors]
  );

  const hasError = useCallback(
    (fieldPrefix: string) => hasFieldError(state.validation.errors, fieldPrefix),
    [state.validation.errors]
  );

  // Check if a rule can have specific filter types added
  const canAddFilter = useCallback(
    (rule: RuleFormState, filterType: HTTPFilter['type']): boolean => {
      const hasRedirect = rule.filters.some(f => f.type === 'RequestRedirect');
      const hasRewrite = rule.filters.some(f => f.type === 'URLRewrite');
      const hasRequestHeaderModifier = rule.filters.some(f => f.type === 'RequestHeaderModifier');
      const hasResponseHeaderModifier = rule.filters.some(f => f.type === 'ResponseHeaderModifier');

      switch (filterType) {
        case 'RequestRedirect':
          return !hasRedirect && !hasRewrite;
        case 'URLRewrite':
          return !hasRewrite && !hasRedirect;
        case 'RequestHeaderModifier':
          return !hasRequestHeaderModifier;
        case 'ResponseHeaderModifier':
          return !hasResponseHeaderModifier;
        default:
          return false;
      }
    },
    []
  );

  // Get available filter types for a rule
  const getAvailableFilterTypes = useCallback(
    (rule: RuleFormState): HTTPFilter['type'][] => {
      const allTypes: HTTPFilter['type'][] = [
        'RequestHeaderModifier',
        'ResponseHeaderModifier',
        'URLRewrite',
        'RequestRedirect',
      ];
      return allTypes.filter(type => canAddFilter(rule, type));
    },
    [canAddFilter]
  );

  // Summary helpers for display
  const getRuleSummary = useCallback((rule: RuleFormState): string => {
    const parts: string[] = [];

    if (rule.matches.length > 0) {
      rule.matches.forEach(match => {
        if (match.path?.value) {
          parts.push(`${match.path.type}: ${match.path.value}`);
        }
        if (match.method) {
          parts.push(match.method);
        }
        if (match.headers.length > 0) {
          parts.push(`${match.headers.length} header(s)`);
        }
        if (match.queryParams.length > 0) {
          parts.push(`${match.queryParams.length} query param(s)`);
        }
      });
    }

    if (rule.filters.length > 0) {
      parts.push(`${rule.filters.length} filter(s)`);
    }

    parts.push(`${rule.backends.length} backend(s)`);

    return parts.join(' | ') || 'No match conditions';
  }, []);

  const getMatchSummary = useCallback((match: MatchFormState): string => {
    const parts: string[] = [];

    if (match.path?.value) {
      parts.push(`Path: ${match.path.type} "${match.path.value}"`);
    }
    if (match.method) {
      parts.push(`Method: ${match.method}`);
    }
    if (match.headers.length > 0) {
      parts.push(`${match.headers.length} header match(es)`);
    }
    if (match.queryParams.length > 0) {
      parts.push(`${match.queryParams.length} query param match(es)`);
    }

    return parts.join(', ') || 'Empty match';
  }, []);

  const getFilterSummary = useCallback((filter: FilterFormState): string => {
    switch (filter.type) {
      case 'RequestHeaderModifier':
      case 'ResponseHeaderModifier': {
        const modifier = filter.type === 'RequestHeaderModifier'
          ? filter.requestHeaderModifier
          : filter.responseHeaderModifier;
        if (!modifier) return filter.type;
        const counts: string[] = [];
        if (modifier.set.length > 0) counts.push(`set: ${modifier.set.length}`);
        if (modifier.add.length > 0) counts.push(`add: ${modifier.add.length}`);
        if (modifier.remove.length > 0) counts.push(`remove: ${modifier.remove.length}`);
        return `${filter.type} (${counts.join(', ') || 'empty'})`;
      }
      case 'URLRewrite': {
        const parts: string[] = [];
        if (filter.urlRewrite?.hostname) parts.push(`host: ${filter.urlRewrite.hostname}`);
        if (filter.urlRewrite?.path?.value) parts.push(`path: ${filter.urlRewrite.path.value}`);
        return `URL Rewrite (${parts.join(', ') || 'empty'})`;
      }
      case 'RequestRedirect': {
        const parts: string[] = [];
        if (filter.requestRedirect?.scheme) parts.push(filter.requestRedirect.scheme);
        if (filter.requestRedirect?.hostname) parts.push(filter.requestRedirect.hostname);
        if (filter.requestRedirect?.statusCode) parts.push(`${filter.requestRedirect.statusCode}`);
        return `Redirect (${parts.join(' ') || 'empty'})`;
      }
      default:
        return filter.type;
    }
  }, []);

  return {
    // State
    state,
    name: state.metadata.name,
    namespace: state.metadata.namespace,
    hostnames: state.spec.hostnames,
    rules: state.spec.rules,
    errors: state.validation.errors,
    expandedRules: state.ui.expandedRules,
    expandedMatches: state.ui.expandedMatches,
    expandedFilters: state.ui.expandedFilters,

    // Metadata
    setName,
    setNamespace,

    // Hostnames
    setHostnames,
    addHostname,
    removeHostname,

    // Rules
    addRule,
    removeRule,
    duplicateRule,
    moveRule,
    updateRuleName,

    // Matches
    addMatch,
    removeMatch,
    updateMatchPath,
    updateMatchMethod,

    // Header matches
    addHeaderMatch,
    removeHeaderMatch,
    updateHeaderMatch,

    // Query param matches
    addQueryParamMatch,
    removeQueryParamMatch,
    updateQueryParamMatch,

    // Filters
    addFilter,
    removeFilter,
    updateFilter,
    canAddFilter,
    getAvailableFilterTypes,

    // Header modifiers
    addHeaderToModifier,
    removeHeaderFromModifier,
    updateHeaderInModifier,
    addRemoveHeader,
    removeRemoveHeader,

    // Backends
    addBackend,
    removeBackend,
    updateBackend,

    // UI
    toggleRuleExpanded,
    toggleMatchExpanded,
    toggleFilterExpanded,
    setFieldTouched,

    // Validation
    validate,
    isValid,
    getErrors,
    hasError,

    // Conversion
    toHTTPProxy,

    // Reset
    reset,

    // Summaries
    getRuleSummary,
    getMatchSummary,
    getFilterSummary,
  };
}

export type GatewayFormHook = ReturnType<typeof useGatewayForm>;

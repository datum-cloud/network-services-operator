'use client';

import { createContext, useContext, useReducer, ReactNode } from 'react';
import type { HTTPProxy } from '@/api/types';
import {
  RuleFormState,
  MatchFormState,
  FilterFormState,
  BackendFormState,
  HeaderMatchFormState,
  QueryParamMatchFormState,
  HTTPHeaderFormState,
  createDefaultRule,
  createDefaultMatch,
  createDefaultBackend,
  createDefaultFilter,
  createDefaultHeaderMatch,
  createDefaultQueryParamMatch,
  createDefaultHTTPHeader,
  createDefaultPathMatch,
  ruleFormStateToHTTPRule,
  httpRuleToRuleFormState,
} from '@/lib/gateway-defaults';
import { validateGatewayForm, ValidationError } from '@/lib/gateway-validation';
import type { HTTPFilter, HTTPPathMatch } from '@/api/types';

// State interface
export interface GatewayFormState {
  metadata: {
    name: string;
    namespace: string;
  };
  spec: {
    hostnames: string[];
    rules: RuleFormState[];
  };
  validation: {
    errors: ValidationError[];
    touched: Record<string, boolean>;
  };
  ui: {
    expandedRules: Set<string>;
    expandedMatches: Set<string>;
    expandedFilters: Set<string>;
  };
}

// Action types
type GatewayFormAction =
  // Metadata actions
  | { type: 'SET_NAME'; payload: string }
  | { type: 'SET_NAMESPACE'; payload: string }
  // Hostname actions
  | { type: 'SET_HOSTNAMES'; payload: string[] }
  | { type: 'ADD_HOSTNAME'; payload: string }
  | { type: 'REMOVE_HOSTNAME'; payload: string }
  // Rule actions
  | { type: 'ADD_RULE' }
  | { type: 'REMOVE_RULE'; payload: string }
  | { type: 'DUPLICATE_RULE'; payload: string }
  | { type: 'MOVE_RULE'; payload: { ruleId: string; direction: 'up' | 'down' } }
  | { type: 'UPDATE_RULE_NAME'; payload: { ruleId: string; name: string } }
  // Match actions
  | { type: 'ADD_MATCH'; payload: { ruleId: string } }
  | { type: 'REMOVE_MATCH'; payload: { ruleId: string; matchId: string } }
  | { type: 'UPDATE_MATCH_PATH'; payload: { ruleId: string; matchId: string; path: HTTPPathMatch | undefined } }
  | { type: 'UPDATE_MATCH_METHOD'; payload: { ruleId: string; matchId: string; method: string | undefined } }
  // Header match actions
  | { type: 'ADD_HEADER_MATCH'; payload: { ruleId: string; matchId: string } }
  | { type: 'REMOVE_HEADER_MATCH'; payload: { ruleId: string; matchId: string; headerId: string } }
  | { type: 'UPDATE_HEADER_MATCH'; payload: { ruleId: string; matchId: string; headerId: string; header: Partial<HeaderMatchFormState> } }
  // Query param match actions
  | { type: 'ADD_QUERY_PARAM_MATCH'; payload: { ruleId: string; matchId: string } }
  | { type: 'REMOVE_QUERY_PARAM_MATCH'; payload: { ruleId: string; matchId: string; paramId: string } }
  | { type: 'UPDATE_QUERY_PARAM_MATCH'; payload: { ruleId: string; matchId: string; paramId: string; param: Partial<QueryParamMatchFormState> } }
  // Filter actions
  | { type: 'ADD_FILTER'; payload: { ruleId: string; filterType: HTTPFilter['type'] } }
  | { type: 'REMOVE_FILTER'; payload: { ruleId: string; filterId: string } }
  | { type: 'UPDATE_FILTER'; payload: { ruleId: string; filterId: string; filter: Partial<FilterFormState> } }
  // Header modifier actions
  | { type: 'ADD_HEADER_TO_MODIFIER'; payload: { ruleId: string; filterId: string; section: 'set' | 'add' } }
  | { type: 'REMOVE_HEADER_FROM_MODIFIER'; payload: { ruleId: string; filterId: string; section: 'set' | 'add'; headerId: string } }
  | { type: 'UPDATE_HEADER_IN_MODIFIER'; payload: { ruleId: string; filterId: string; section: 'set' | 'add'; headerId: string; header: Partial<HTTPHeaderFormState> } }
  | { type: 'ADD_REMOVE_HEADER'; payload: { ruleId: string; filterId: string; headerName: string } }
  | { type: 'REMOVE_REMOVE_HEADER'; payload: { ruleId: string; filterId: string; headerName: string } }
  // Backend actions
  | { type: 'ADD_BACKEND'; payload: { ruleId: string } }
  | { type: 'REMOVE_BACKEND'; payload: { ruleId: string; backendId: string } }
  | { type: 'UPDATE_BACKEND'; payload: { ruleId: string; backendId: string; backend: Partial<BackendFormState> } }
  // UI actions
  | { type: 'TOGGLE_RULE_EXPANDED'; payload: string }
  | { type: 'TOGGLE_MATCH_EXPANDED'; payload: string }
  | { type: 'TOGGLE_FILTER_EXPANDED'; payload: string }
  | { type: 'SET_FIELD_TOUCHED'; payload: string }
  // Validation
  | { type: 'VALIDATE' }
  | { type: 'CLEAR_VALIDATION' }
  // Load from existing proxy
  | { type: 'LOAD_FROM_PROXY'; payload: HTTPProxy }
  // Reset form
  | { type: 'RESET' };

// Initial state factory
function createInitialState(): GatewayFormState {
  return {
    metadata: {
      name: '',
      namespace: 'default',
    },
    spec: {
      hostnames: [],
      rules: [createDefaultRule()],
    },
    validation: {
      errors: [],
      touched: {},
    },
    ui: {
      expandedRules: new Set(),
      expandedMatches: new Set(),
      expandedFilters: new Set(),
    },
  };
}

// Helper to find and update a rule
function updateRule(
  rules: RuleFormState[],
  ruleId: string,
  updater: (rule: RuleFormState) => RuleFormState
): RuleFormState[] {
  return rules.map(rule => (rule.id === ruleId ? updater(rule) : rule));
}

// Helper to find and update a match within a rule
function updateMatch(
  rule: RuleFormState,
  matchId: string,
  updater: (match: MatchFormState) => MatchFormState
): RuleFormState {
  return {
    ...rule,
    matches: rule.matches.map(match => (match.id === matchId ? updater(match) : match)),
  };
}

// Helper to find and update a filter within a rule
function updateFilter(
  rule: RuleFormState,
  filterId: string,
  updater: (filter: FilterFormState) => FilterFormState
): RuleFormState {
  return {
    ...rule,
    filters: rule.filters.map(filter => (filter.id === filterId ? updater(filter) : filter)),
  };
}

// Helper to find and update a backend within a rule
function updateBackend(
  rule: RuleFormState,
  backendId: string,
  updater: (backend: BackendFormState) => BackendFormState
): RuleFormState {
  return {
    ...rule,
    backends: rule.backends.map(backend => (backend.id === backendId ? updater(backend) : backend)),
  };
}

// Reducer
function gatewayFormReducer(state: GatewayFormState, action: GatewayFormAction): GatewayFormState {
  switch (action.type) {
    // Metadata
    case 'SET_NAME':
      return {
        ...state,
        metadata: { ...state.metadata, name: action.payload },
      };

    case 'SET_NAMESPACE':
      return {
        ...state,
        metadata: { ...state.metadata, namespace: action.payload },
      };

    // Hostnames
    case 'SET_HOSTNAMES':
      return {
        ...state,
        spec: { ...state.spec, hostnames: action.payload },
      };

    case 'ADD_HOSTNAME':
      if (state.spec.hostnames.includes(action.payload)) {
        return state;
      }
      return {
        ...state,
        spec: { ...state.spec, hostnames: [...state.spec.hostnames, action.payload] },
      };

    case 'REMOVE_HOSTNAME':
      return {
        ...state,
        spec: {
          ...state.spec,
          hostnames: state.spec.hostnames.filter(h => h !== action.payload),
        },
      };

    // Rules
    case 'ADD_RULE': {
      const newRule = createDefaultRule();
      return {
        ...state,
        spec: { ...state.spec, rules: [...state.spec.rules, newRule] },
        ui: {
          ...state.ui,
          expandedRules: new Set([...state.ui.expandedRules, newRule.id]),
        },
      };
    }

    case 'REMOVE_RULE':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: state.spec.rules.filter(r => r.id !== action.payload),
        },
      };

    case 'DUPLICATE_RULE': {
      const ruleToDuplicate = state.spec.rules.find(r => r.id === action.payload);
      if (!ruleToDuplicate) return state;

      const duplicatedRule: RuleFormState = {
        ...JSON.parse(JSON.stringify(ruleToDuplicate)),
        id: `${Date.now()}-dup`,
      };
      // Update all nested IDs
      duplicatedRule.matches = duplicatedRule.matches.map((m: MatchFormState) => ({
        ...m,
        id: `${Date.now()}-m-${Math.random()}`,
        headers: m.headers.map((h: HeaderMatchFormState) => ({ ...h, id: `${Date.now()}-h-${Math.random()}` })),
        queryParams: m.queryParams.map((q: QueryParamMatchFormState) => ({ ...q, id: `${Date.now()}-q-${Math.random()}` })),
      }));
      duplicatedRule.filters = duplicatedRule.filters.map((f: FilterFormState) => ({ ...f, id: `${Date.now()}-f-${Math.random()}` }));
      duplicatedRule.backends = duplicatedRule.backends.map((b: BackendFormState) => ({ ...b, id: `${Date.now()}-b-${Math.random()}` }));

      const index = state.spec.rules.findIndex(r => r.id === action.payload);
      const newRules = [...state.spec.rules];
      newRules.splice(index + 1, 0, duplicatedRule);

      return {
        ...state,
        spec: { ...state.spec, rules: newRules },
      };
    }

    case 'MOVE_RULE': {
      const { ruleId, direction } = action.payload;
      const index = state.spec.rules.findIndex(r => r.id === ruleId);
      if (index === -1) return state;

      const newIndex = direction === 'up' ? index - 1 : index + 1;
      if (newIndex < 0 || newIndex >= state.spec.rules.length) return state;

      const newRules = [...state.spec.rules];
      [newRules[index], newRules[newIndex]] = [newRules[newIndex], newRules[index]];

      return {
        ...state,
        spec: { ...state.spec, rules: newRules },
      };
    }

    case 'UPDATE_RULE_NAME':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule => ({
            ...rule,
            name: action.payload.name,
          })),
        },
      };

    // Matches
    case 'ADD_MATCH': {
      const newMatch = createDefaultMatch();
      newMatch.path = createDefaultPathMatch();
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule => ({
            ...rule,
            matches: [...rule.matches, newMatch],
          })),
        },
        ui: {
          ...state.ui,
          expandedMatches: new Set([...state.ui.expandedMatches, newMatch.id]),
        },
      };
    }

    case 'REMOVE_MATCH':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule => ({
            ...rule,
            matches: rule.matches.filter(m => m.id !== action.payload.matchId),
          })),
        },
      };

    case 'UPDATE_MATCH_PATH':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateMatch(rule, action.payload.matchId, match => ({
              ...match,
              path: action.payload.path,
            }))
          ),
        },
      };

    case 'UPDATE_MATCH_METHOD':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateMatch(rule, action.payload.matchId, match => ({
              ...match,
              method: action.payload.method,
            }))
          ),
        },
      };

    // Header matches
    case 'ADD_HEADER_MATCH':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateMatch(rule, action.payload.matchId, match => ({
              ...match,
              headers: [...match.headers, createDefaultHeaderMatch()],
            }))
          ),
        },
      };

    case 'REMOVE_HEADER_MATCH':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateMatch(rule, action.payload.matchId, match => ({
              ...match,
              headers: match.headers.filter(h => h.id !== action.payload.headerId),
            }))
          ),
        },
      };

    case 'UPDATE_HEADER_MATCH':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateMatch(rule, action.payload.matchId, match => ({
              ...match,
              headers: match.headers.map(h =>
                h.id === action.payload.headerId ? { ...h, ...action.payload.header } : h
              ),
            }))
          ),
        },
      };

    // Query param matches
    case 'ADD_QUERY_PARAM_MATCH':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateMatch(rule, action.payload.matchId, match => ({
              ...match,
              queryParams: [...match.queryParams, createDefaultQueryParamMatch()],
            }))
          ),
        },
      };

    case 'REMOVE_QUERY_PARAM_MATCH':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateMatch(rule, action.payload.matchId, match => ({
              ...match,
              queryParams: match.queryParams.filter(q => q.id !== action.payload.paramId),
            }))
          ),
        },
      };

    case 'UPDATE_QUERY_PARAM_MATCH':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateMatch(rule, action.payload.matchId, match => ({
              ...match,
              queryParams: match.queryParams.map(q =>
                q.id === action.payload.paramId ? { ...q, ...action.payload.param } : q
              ),
            }))
          ),
        },
      };

    // Filters
    case 'ADD_FILTER': {
      const newFilter = createDefaultFilter(action.payload.filterType);
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule => ({
            ...rule,
            filters: [...rule.filters, newFilter],
          })),
        },
        ui: {
          ...state.ui,
          expandedFilters: new Set([...state.ui.expandedFilters, newFilter.id]),
        },
      };
    }

    case 'REMOVE_FILTER':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule => ({
            ...rule,
            filters: rule.filters.filter(f => f.id !== action.payload.filterId),
          })),
        },
      };

    case 'UPDATE_FILTER':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateFilter(rule, action.payload.filterId, filter => ({
              ...filter,
              ...action.payload.filter,
            }))
          ),
        },
      };

    // Header modifier actions
    case 'ADD_HEADER_TO_MODIFIER':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateFilter(rule, action.payload.filterId, filter => {
              const modifierKey = filter.type === 'RequestHeaderModifier'
                ? 'requestHeaderModifier'
                : 'responseHeaderModifier';
              const modifier = filter[modifierKey];
              if (!modifier) return filter;

              return {
                ...filter,
                [modifierKey]: {
                  ...modifier,
                  [action.payload.section]: [...modifier[action.payload.section], createDefaultHTTPHeader()],
                },
              };
            })
          ),
        },
      };

    case 'REMOVE_HEADER_FROM_MODIFIER':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateFilter(rule, action.payload.filterId, filter => {
              const modifierKey = filter.type === 'RequestHeaderModifier'
                ? 'requestHeaderModifier'
                : 'responseHeaderModifier';
              const modifier = filter[modifierKey];
              if (!modifier) return filter;

              return {
                ...filter,
                [modifierKey]: {
                  ...modifier,
                  [action.payload.section]: modifier[action.payload.section].filter(
                    (h: HTTPHeaderFormState) => h.id !== action.payload.headerId
                  ),
                },
              };
            })
          ),
        },
      };

    case 'UPDATE_HEADER_IN_MODIFIER':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateFilter(rule, action.payload.filterId, filter => {
              const modifierKey = filter.type === 'RequestHeaderModifier'
                ? 'requestHeaderModifier'
                : 'responseHeaderModifier';
              const modifier = filter[modifierKey];
              if (!modifier) return filter;

              return {
                ...filter,
                [modifierKey]: {
                  ...modifier,
                  [action.payload.section]: modifier[action.payload.section].map(
                    (h: HTTPHeaderFormState) =>
                      h.id === action.payload.headerId ? { ...h, ...action.payload.header } : h
                  ),
                },
              };
            })
          ),
        },
      };

    case 'ADD_REMOVE_HEADER':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateFilter(rule, action.payload.filterId, filter => {
              const modifierKey = filter.type === 'RequestHeaderModifier'
                ? 'requestHeaderModifier'
                : 'responseHeaderModifier';
              const modifier = filter[modifierKey];
              if (!modifier) return filter;

              if (modifier.remove.includes(action.payload.headerName)) {
                return filter;
              }

              return {
                ...filter,
                [modifierKey]: {
                  ...modifier,
                  remove: [...modifier.remove, action.payload.headerName],
                },
              };
            })
          ),
        },
      };

    case 'REMOVE_REMOVE_HEADER':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateFilter(rule, action.payload.filterId, filter => {
              const modifierKey = filter.type === 'RequestHeaderModifier'
                ? 'requestHeaderModifier'
                : 'responseHeaderModifier';
              const modifier = filter[modifierKey];
              if (!modifier) return filter;

              return {
                ...filter,
                [modifierKey]: {
                  ...modifier,
                  remove: modifier.remove.filter(h => h !== action.payload.headerName),
                },
              };
            })
          ),
        },
      };

    // Backends
    case 'ADD_BACKEND':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule => ({
            ...rule,
            backends: [...rule.backends, createDefaultBackend()],
          })),
        },
      };

    case 'REMOVE_BACKEND':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule => ({
            ...rule,
            backends: rule.backends.filter(b => b.id !== action.payload.backendId),
          })),
        },
      };

    case 'UPDATE_BACKEND':
      return {
        ...state,
        spec: {
          ...state.spec,
          rules: updateRule(state.spec.rules, action.payload.ruleId, rule =>
            updateBackend(rule, action.payload.backendId, backend => ({
              ...backend,
              ...action.payload.backend,
            }))
          ),
        },
      };

    // UI
    case 'TOGGLE_RULE_EXPANDED': {
      const newExpanded = new Set(state.ui.expandedRules);
      if (newExpanded.has(action.payload)) {
        newExpanded.delete(action.payload);
      } else {
        newExpanded.add(action.payload);
      }
      return {
        ...state,
        ui: { ...state.ui, expandedRules: newExpanded },
      };
    }

    case 'TOGGLE_MATCH_EXPANDED': {
      const newExpanded = new Set(state.ui.expandedMatches);
      if (newExpanded.has(action.payload)) {
        newExpanded.delete(action.payload);
      } else {
        newExpanded.add(action.payload);
      }
      return {
        ...state,
        ui: { ...state.ui, expandedMatches: newExpanded },
      };
    }

    case 'TOGGLE_FILTER_EXPANDED': {
      const newExpanded = new Set(state.ui.expandedFilters);
      if (newExpanded.has(action.payload)) {
        newExpanded.delete(action.payload);
      } else {
        newExpanded.add(action.payload);
      }
      return {
        ...state,
        ui: { ...state.ui, expandedFilters: newExpanded },
      };
    }

    case 'SET_FIELD_TOUCHED':
      return {
        ...state,
        validation: {
          ...state.validation,
          touched: { ...state.validation.touched, [action.payload]: true },
        },
      };

    // Validation
    case 'VALIDATE': {
      const result = validateGatewayForm(
        state.metadata.name,
        state.metadata.namespace,
        state.spec.hostnames,
        state.spec.rules
      );
      return {
        ...state,
        validation: {
          ...state.validation,
          errors: result.errors,
        },
      };
    }

    case 'CLEAR_VALIDATION':
      return {
        ...state,
        validation: {
          errors: [],
          touched: {},
        },
      };

    // Load from proxy
    case 'LOAD_FROM_PROXY': {
      const proxy = action.payload;
      const rules = proxy.spec.rules?.map(httpRuleToRuleFormState) || [createDefaultRule()];
      return {
        ...state,
        metadata: {
          name: proxy.metadata.name,
          namespace: proxy.metadata.namespace || 'default',
        },
        spec: {
          hostnames: proxy.spec.hostnames || [],
          rules,
        },
        ui: {
          expandedRules: new Set(rules.map(r => r.id)),
          expandedMatches: new Set(),
          expandedFilters: new Set(),
        },
      };
    }

    // Reset
    case 'RESET':
      return createInitialState();

    default:
      return state;
  }
}

// Context
interface GatewayFormContextType {
  state: GatewayFormState;
  dispatch: React.Dispatch<GatewayFormAction>;
  toHTTPProxy: () => HTTPProxy;
  validate: () => boolean;
  isValid: boolean;
}

const GatewayFormContext = createContext<GatewayFormContextType | null>(null);

// Provider
interface GatewayFormProviderProps {
  children: ReactNode;
  initialProxy?: HTTPProxy;
}

export function GatewayFormProvider({ children, initialProxy }: GatewayFormProviderProps) {
  const [state, dispatch] = useReducer(gatewayFormReducer, undefined, () => {
    const initial = createInitialState();
    if (initialProxy) {
      return gatewayFormReducer(initial, { type: 'LOAD_FROM_PROXY', payload: initialProxy });
    }
    return initial;
  });

  const toHTTPProxy = (): HTTPProxy => {
    return {
      apiVersion: 'networking.datumapis.com/v1alpha',
      kind: 'HTTPProxy',
      metadata: {
        name: state.metadata.name,
        namespace: state.metadata.namespace,
      },
      spec: {
        hostnames: state.spec.hostnames,
        rules: state.spec.rules.map(ruleFormStateToHTTPRule),
      },
    };
  };

  const validate = (): boolean => {
    dispatch({ type: 'VALIDATE' });
    const result = validateGatewayForm(
      state.metadata.name,
      state.metadata.namespace,
      state.spec.hostnames,
      state.spec.rules
    );
    return result.valid;
  };

  const isValid = state.validation.errors.length === 0;

  return (
    <GatewayFormContext.Provider value={{ state, dispatch, toHTTPProxy, validate, isValid }}>
      {children}
    </GatewayFormContext.Provider>
  );
}

// Hook to use the context
export function useGatewayFormContext(): GatewayFormContextType {
  const context = useContext(GatewayFormContext);
  if (!context) {
    throw new Error('useGatewayFormContext must be used within a GatewayFormProvider');
  }
  return context;
}

// Export the action type for external use
export type { GatewayFormAction };

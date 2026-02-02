import type {
  HTTPRule,
  HTTPMatch,
  HTTPFilter,
  HTTPBackend,
  HTTPPathMatch,
  HTTPHeaderMatch,
  HTTPQueryParamMatch,
  HTTPHeaderModifier,
  URLRewrite,
  RequestRedirect,
  HTTPHeader,
} from '@/api/types';

// Generate unique IDs for React keys
let idCounter = 0;
export function generateId(): string {
  return `${Date.now()}-${++idCounter}`;
}

// Form state types with IDs for React keys
export interface RuleFormState {
  id: string;
  name: string;
  matches: MatchFormState[];
  filters: FilterFormState[];
  backends: BackendFormState[];
}

export interface MatchFormState {
  id: string;
  path?: HTTPPathMatch;
  headers: HeaderMatchFormState[];
  queryParams: QueryParamMatchFormState[];
  method?: string;
}

export interface HeaderMatchFormState extends HTTPHeaderMatch {
  id: string;
}

export interface QueryParamMatchFormState extends HTTPQueryParamMatch {
  id: string;
}

export interface FilterFormState {
  id: string;
  type: HTTPFilter['type'];
  requestHeaderModifier?: HeaderModifierFormState;
  responseHeaderModifier?: HeaderModifierFormState;
  urlRewrite?: URLRewriteFormState;
  requestRedirect?: RequestRedirectFormState;
}

export interface HeaderModifierFormState {
  set: HTTPHeaderFormState[];
  add: HTTPHeaderFormState[];
  remove: string[];
}

export interface HTTPHeaderFormState extends HTTPHeader {
  id: string;
}

export interface URLRewriteFormState {
  hostname?: string;
  path?: HTTPPathMatch;
}

export interface RequestRedirectFormState {
  scheme?: string;
  hostname?: string;
  path?: HTTPPathMatch;
  port?: number;
  statusCode?: number;
}

export interface BackendFormState {
  id: string;
  endpoint: string;
  connectorRef?: {
    name: string;
    namespace?: string;
  };
  weight?: number;
  tls?: {
    hostname?: string;
  };
}

// Factory functions for creating default objects

export function createDefaultRule(): RuleFormState {
  return {
    id: generateId(),
    name: '',
    matches: [],
    filters: [],
    backends: [createDefaultBackend()],
  };
}

export function createDefaultMatch(): MatchFormState {
  return {
    id: generateId(),
    headers: [],
    queryParams: [],
  };
}

export function createDefaultPathMatch(): HTTPPathMatch {
  return {
    type: 'PathPrefix',
    value: '/',
  };
}

export function createDefaultHeaderMatch(): HeaderMatchFormState {
  return {
    id: generateId(),
    type: 'Exact',
    name: '',
    value: '',
  };
}

export function createDefaultQueryParamMatch(): QueryParamMatchFormState {
  return {
    id: generateId(),
    type: 'Exact',
    name: '',
    value: '',
  };
}

export function createDefaultFilter(type: HTTPFilter['type']): FilterFormState {
  const base = {
    id: generateId(),
    type,
  };

  switch (type) {
    case 'RequestHeaderModifier':
      return {
        ...base,
        requestHeaderModifier: createDefaultHeaderModifier(),
      };
    case 'ResponseHeaderModifier':
      return {
        ...base,
        responseHeaderModifier: createDefaultHeaderModifier(),
      };
    case 'URLRewrite':
      return {
        ...base,
        urlRewrite: createDefaultURLRewrite(),
      };
    case 'RequestRedirect':
      return {
        ...base,
        requestRedirect: createDefaultRequestRedirect(),
      };
  }
}

export function createDefaultHeaderModifier(): HeaderModifierFormState {
  return {
    set: [],
    add: [],
    remove: [],
  };
}

export function createDefaultHTTPHeader(): HTTPHeaderFormState {
  return {
    id: generateId(),
    name: '',
    value: '',
  };
}

export function createDefaultURLRewrite(): URLRewriteFormState {
  return {
    hostname: '',
  };
}

export function createDefaultRequestRedirect(): RequestRedirectFormState {
  return {
    statusCode: 302,
  };
}

export function createDefaultBackend(): BackendFormState {
  return {
    id: generateId(),
    endpoint: '',
  };
}

// Conversion functions: Form state -> API types

export function ruleFormStateToHTTPRule(rule: RuleFormState): HTTPRule {
  return {
    name: rule.name || undefined,
    matches: rule.matches.length > 0
      ? rule.matches.map(matchFormStateToHTTPMatch).filter(m => Object.keys(m).length > 0)
      : undefined,
    filters: rule.filters.length > 0
      ? rule.filters.map(filterFormStateToHTTPFilter)
      : undefined,
    backends: rule.backends.map(backendFormStateToHTTPBackend),
  };
}

export function matchFormStateToHTTPMatch(match: MatchFormState): HTTPMatch {
  const result: HTTPMatch = {};

  if (match.path?.value) {
    result.path = match.path;
  }

  if (match.headers.length > 0) {
    result.headers = match.headers
      .filter(h => h.name && h.value)
      .map(({ id, ...rest }) => rest);
  }

  if (match.queryParams.length > 0) {
    result.queryParams = match.queryParams
      .filter(q => q.name && q.value)
      .map(({ id, ...rest }) => rest);
  }

  if (match.method) {
    result.method = match.method;
  }

  return result;
}

export function filterFormStateToHTTPFilter(filter: FilterFormState): HTTPFilter {
  const result: HTTPFilter = {
    type: filter.type,
  };

  switch (filter.type) {
    case 'RequestHeaderModifier':
      if (filter.requestHeaderModifier) {
        result.requestHeaderModifier = headerModifierFormStateToHTTPHeaderModifier(
          filter.requestHeaderModifier
        );
      }
      break;
    case 'ResponseHeaderModifier':
      if (filter.responseHeaderModifier) {
        result.responseHeaderModifier = headerModifierFormStateToHTTPHeaderModifier(
          filter.responseHeaderModifier
        );
      }
      break;
    case 'URLRewrite':
      if (filter.urlRewrite) {
        result.urlRewrite = urlRewriteFormStateToURLRewrite(filter.urlRewrite);
      }
      break;
    case 'RequestRedirect':
      if (filter.requestRedirect) {
        result.requestRedirect = requestRedirectFormStateToRequestRedirect(filter.requestRedirect);
      }
      break;
  }

  return result;
}

export function headerModifierFormStateToHTTPHeaderModifier(
  modifier: HeaderModifierFormState
): HTTPHeaderModifier {
  const result: HTTPHeaderModifier = {};

  const set = modifier.set.filter(h => h.name && h.value).map(({ id, ...rest }) => rest);
  const add = modifier.add.filter(h => h.name && h.value).map(({ id, ...rest }) => rest);
  const remove = modifier.remove.filter(Boolean);

  if (set.length > 0) result.set = set;
  if (add.length > 0) result.add = add;
  if (remove.length > 0) result.remove = remove;

  return result;
}

export function urlRewriteFormStateToURLRewrite(rewrite: URLRewriteFormState): URLRewrite {
  const result: URLRewrite = {};

  if (rewrite.hostname) {
    result.hostname = rewrite.hostname;
  }

  if (rewrite.path?.value) {
    result.path = rewrite.path;
  }

  return result;
}

export function requestRedirectFormStateToRequestRedirect(
  redirect: RequestRedirectFormState
): RequestRedirect {
  const result: RequestRedirect = {};

  if (redirect.scheme) result.scheme = redirect.scheme;
  if (redirect.hostname) result.hostname = redirect.hostname;
  if (redirect.path?.value) result.path = redirect.path;
  if (redirect.port) result.port = redirect.port;
  if (redirect.statusCode) result.statusCode = redirect.statusCode;

  return result;
}

export function backendFormStateToHTTPBackend(backend: BackendFormState): HTTPBackend {
  const result: HTTPBackend = {};

  if (backend.endpoint) {
    result.endpoint = backend.endpoint;
  }

  if (backend.connectorRef?.name) {
    result.connectorRef = backend.connectorRef;
  }

  if (backend.weight !== undefined) {
    result.weight = backend.weight;
  }

  return result;
}

// Conversion functions: API types -> Form state

export function httpRuleToRuleFormState(rule: HTTPRule): RuleFormState {
  return {
    id: generateId(),
    name: rule.name || '',
    matches: rule.matches?.map(httpMatchToMatchFormState) || [],
    filters: rule.filters?.map(httpFilterToFilterFormState) || [],
    backends: rule.backends?.map(httpBackendToBackendFormState) || [createDefaultBackend()],
  };
}

export function httpMatchToMatchFormState(match: HTTPMatch): MatchFormState {
  return {
    id: generateId(),
    path: match.path,
    headers: match.headers?.map(h => ({ id: generateId(), ...h })) || [],
    queryParams: match.queryParams?.map(q => ({ id: generateId(), ...q })) || [],
    method: match.method,
  };
}

export function httpFilterToFilterFormState(filter: HTTPFilter): FilterFormState {
  const base = {
    id: generateId(),
    type: filter.type,
  };

  switch (filter.type) {
    case 'RequestHeaderModifier':
      return {
        ...base,
        requestHeaderModifier: filter.requestHeaderModifier
          ? httpHeaderModifierToHeaderModifierFormState(filter.requestHeaderModifier)
          : createDefaultHeaderModifier(),
      };
    case 'ResponseHeaderModifier':
      return {
        ...base,
        responseHeaderModifier: filter.responseHeaderModifier
          ? httpHeaderModifierToHeaderModifierFormState(filter.responseHeaderModifier)
          : createDefaultHeaderModifier(),
      };
    case 'URLRewrite':
      return {
        ...base,
        urlRewrite: filter.urlRewrite || createDefaultURLRewrite(),
      };
    case 'RequestRedirect':
      return {
        ...base,
        requestRedirect: filter.requestRedirect || createDefaultRequestRedirect(),
      };
  }
}

export function httpHeaderModifierToHeaderModifierFormState(
  modifier: HTTPHeaderModifier
): HeaderModifierFormState {
  return {
    set: modifier.set?.map(h => ({ id: generateId(), ...h })) || [],
    add: modifier.add?.map(h => ({ id: generateId(), ...h })) || [],
    remove: modifier.remove || [],
  };
}

export function httpBackendToBackendFormState(backend: HTTPBackend): BackendFormState {
  return {
    id: generateId(),
    endpoint: backend.endpoint || '',
    connectorRef: backend.connectorRef,
    weight: backend.weight,
  };
}

// HTTP Methods constant
export const HTTP_METHODS = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'] as const;
export type HTTPMethodType = typeof HTTP_METHODS[number];

// Path match types constant
export const PATH_MATCH_TYPES = ['Exact', 'PathPrefix', 'RegularExpression'] as const;
export type PathMatchType = typeof PATH_MATCH_TYPES[number];

// Header/query param match types constant
export const HEADER_MATCH_TYPES = ['Exact', 'RegularExpression'] as const;
export type HeaderMatchType = typeof HEADER_MATCH_TYPES[number];

// Redirect status codes
export const REDIRECT_STATUS_CODES = [301, 302, 303, 307, 308] as const;
export type RedirectStatusCode = typeof REDIRECT_STATUS_CODES[number];

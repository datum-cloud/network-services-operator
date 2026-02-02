import type {
  RuleFormState,
  MatchFormState,
  FilterFormState,
  BackendFormState,
} from './gateway-defaults';

// Constraint constants from the API
export const MAX_HOSTNAMES = 16;
export const MAX_RULES = 16;
export const MAX_MATCHES_PER_RULE = 64;
export const MAX_TOTAL_MATCHES = 128;

export interface ValidationError {
  field: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}

// Validate the entire gateway form
export function validateGatewayForm(
  name: string,
  namespace: string,
  hostnames: string[],
  rules: RuleFormState[]
): ValidationResult {
  const errors: ValidationError[] = [];

  // Validate metadata
  const nameErrors = validateName(name);
  errors.push(...nameErrors.errors);

  const namespaceErrors = validateNamespace(namespace);
  errors.push(...namespaceErrors.errors);

  // Validate hostnames
  const hostnameErrors = validateHostnames(hostnames);
  errors.push(...hostnameErrors.errors);

  // Validate rules
  const ruleErrors = validateRules(rules);
  errors.push(...ruleErrors.errors);

  return {
    valid: errors.length === 0,
    errors,
  };
}

// Validate gateway name
export function validateName(name: string): ValidationResult {
  const errors: ValidationError[] = [];

  if (!name || name.trim() === '') {
    errors.push({ field: 'name', message: 'Name is required' });
  } else {
    // Kubernetes naming rules: lowercase alphanumeric, hyphens allowed, max 253 chars
    const nameRegex = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
    if (!nameRegex.test(name)) {
      errors.push({
        field: 'name',
        message: 'Name must be lowercase alphanumeric characters or hyphens, and must start and end with an alphanumeric character',
      });
    }
    if (name.length > 253) {
      errors.push({ field: 'name', message: 'Name must be 253 characters or less' });
    }
  }

  return { valid: errors.length === 0, errors };
}

// Validate namespace
export function validateNamespace(namespace: string): ValidationResult {
  const errors: ValidationError[] = [];

  if (!namespace || namespace.trim() === '') {
    errors.push({ field: 'namespace', message: 'Namespace is required' });
  }

  return { valid: errors.length === 0, errors };
}

// Validate hostnames
export function validateHostnames(hostnames: string[]): ValidationResult {
  const errors: ValidationError[] = [];

  if (hostnames.length > MAX_HOSTNAMES) {
    errors.push({
      field: 'hostnames',
      message: `Maximum ${MAX_HOSTNAMES} hostnames allowed`,
    });
  }

  hostnames.forEach((hostname, index) => {
    const hostnameErrors = validateHostname(hostname);
    hostnameErrors.errors.forEach(err => {
      errors.push({
        field: `hostnames.${index}`,
        message: err.message,
      });
    });
  });

  return { valid: errors.length === 0, errors };
}

// Validate a single hostname
export function validateHostname(hostname: string): ValidationResult {
  const errors: ValidationError[] = [];

  if (!hostname || hostname.trim() === '') {
    errors.push({ field: 'hostname', message: 'Hostname cannot be empty' });
    return { valid: false, errors };
  }

  // Basic hostname validation - allows wildcards
  const hostnameRegex = /^(\*\.)?[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/i;
  if (!hostnameRegex.test(hostname)) {
    errors.push({
      field: 'hostname',
      message: 'Invalid hostname format',
    });
  }

  return { valid: errors.length === 0, errors };
}

// Validate all rules
export function validateRules(rules: RuleFormState[]): ValidationResult {
  const errors: ValidationError[] = [];

  if (rules.length > MAX_RULES) {
    errors.push({
      field: 'rules',
      message: `Maximum ${MAX_RULES} rules allowed`,
    });
  }

  // Count total matches across all rules
  let totalMatches = 0;

  rules.forEach((rule, ruleIndex) => {
    const ruleErrors = validateRule(rule, ruleIndex);
    errors.push(...ruleErrors.errors);

    totalMatches += rule.matches.length;

    if (rule.matches.length > MAX_MATCHES_PER_RULE) {
      errors.push({
        field: `rules.${ruleIndex}.matches`,
        message: `Maximum ${MAX_MATCHES_PER_RULE} matches per rule`,
      });
    }
  });

  if (totalMatches > MAX_TOTAL_MATCHES) {
    errors.push({
      field: 'rules',
      message: `Maximum ${MAX_TOTAL_MATCHES} total matches across all rules`,
    });
  }

  return { valid: errors.length === 0, errors };
}

// Validate a single rule
export function validateRule(rule: RuleFormState, index: number): ValidationResult {
  const errors: ValidationError[] = [];

  // Validate backends - at least one required
  if (rule.backends.length === 0) {
    errors.push({
      field: `rules.${index}.backends`,
      message: 'At least one backend is required',
    });
  }

  rule.backends.forEach((backend, backendIndex) => {
    const backendErrors = validateBackend(backend);
    backendErrors.errors.forEach(err => {
      errors.push({
        field: `rules.${index}.backends.${backendIndex}.${err.field}`,
        message: err.message,
      });
    });
  });

  // Validate matches
  rule.matches.forEach((match, matchIndex) => {
    const matchErrors = validateMatch(match);
    matchErrors.errors.forEach(err => {
      errors.push({
        field: `rules.${index}.matches.${matchIndex}.${err.field}`,
        message: err.message,
      });
    });
  });

  // Validate filters
  const filterErrors = validateFilters(rule.filters, index);
  errors.push(...filterErrors.errors);

  return { valid: errors.length === 0, errors };
}

// Validate a backend
export function validateBackend(backend: BackendFormState): ValidationResult {
  const errors: ValidationError[] = [];

  if (!backend.endpoint && !backend.connectorRef?.name) {
    errors.push({
      field: 'endpoint',
      message: 'Either endpoint or connector reference is required',
    });
  }

  if (backend.endpoint) {
    const urlErrors = validateBackendUrl(backend.endpoint);
    errors.push(...urlErrors.errors);
  }

  if (backend.weight !== undefined && (backend.weight < 0 || backend.weight > 1000000)) {
    errors.push({
      field: 'weight',
      message: 'Weight must be between 0 and 1000000',
    });
  }

  return { valid: errors.length === 0, errors };
}

// Validate a backend URL
export function validateBackendUrl(url: string): ValidationResult {
  const errors: ValidationError[] = [];

  try {
    const parsed = new URL(url);
    if (!['http:', 'https:'].includes(parsed.protocol)) {
      errors.push({
        field: 'endpoint',
        message: 'Backend URL must use http or https protocol',
      });
    }
  } catch {
    errors.push({
      field: 'endpoint',
      message: 'Invalid URL format',
    });
  }

  return { valid: errors.length === 0, errors };
}

// Validate a match condition
export function validateMatch(match: MatchFormState): ValidationResult {
  const errors: ValidationError[] = [];

  // Validate path if present
  if (match.path) {
    if (!match.path.value || match.path.value.trim() === '') {
      errors.push({
        field: 'path.value',
        message: 'Path value is required when path matching is enabled',
      });
    }

    if (match.path.type === 'RegularExpression') {
      try {
        new RegExp(match.path.value);
      } catch {
        errors.push({
          field: 'path.value',
          message: 'Invalid regular expression',
        });
      }
    }
  }

  // Validate headers
  match.headers.forEach((header, index) => {
    if (header.name && !header.value) {
      errors.push({
        field: `headers.${index}.value`,
        message: 'Header value is required',
      });
    }
    if (header.value && !header.name) {
      errors.push({
        field: `headers.${index}.name`,
        message: 'Header name is required',
      });
    }
    if (header.type === 'RegularExpression' && header.value) {
      try {
        new RegExp(header.value);
      } catch {
        errors.push({
          field: `headers.${index}.value`,
          message: 'Invalid regular expression',
        });
      }
    }
  });

  // Validate query params
  match.queryParams.forEach((param, index) => {
    if (param.name && !param.value) {
      errors.push({
        field: `queryParams.${index}.value`,
        message: 'Query parameter value is required',
      });
    }
    if (param.value && !param.name) {
      errors.push({
        field: `queryParams.${index}.name`,
        message: 'Query parameter name is required',
      });
    }
    if (param.type === 'RegularExpression' && param.value) {
      try {
        new RegExp(param.value);
      } catch {
        errors.push({
          field: `queryParams.${index}.value`,
          message: 'Invalid regular expression',
        });
      }
    }
  });

  return { valid: errors.length === 0, errors };
}

// Validate filters with mutual exclusion rules
export function validateFilters(filters: FilterFormState[], ruleIndex: number): ValidationResult {
  const errors: ValidationError[] = [];

  // Check for mutual exclusion: RequestRedirect and URLRewrite cannot be combined
  const hasRedirect = filters.some(f => f.type === 'RequestRedirect');
  const hasRewrite = filters.some(f => f.type === 'URLRewrite');

  if (hasRedirect && hasRewrite) {
    errors.push({
      field: `rules.${ruleIndex}.filters`,
      message: 'RequestRedirect and URLRewrite filters cannot be combined in the same rule',
    });
  }

  // Check for single RequestHeaderModifier and ResponseHeaderModifier per rule
  const requestHeaderModifierCount = filters.filter(f => f.type === 'RequestHeaderModifier').length;
  const responseHeaderModifierCount = filters.filter(f => f.type === 'ResponseHeaderModifier').length;

  if (requestHeaderModifierCount > 1) {
    errors.push({
      field: `rules.${ruleIndex}.filters`,
      message: 'Only one RequestHeaderModifier filter allowed per rule',
    });
  }

  if (responseHeaderModifierCount > 1) {
    errors.push({
      field: `rules.${ruleIndex}.filters`,
      message: 'Only one ResponseHeaderModifier filter allowed per rule',
    });
  }

  // Validate individual filters
  filters.forEach((filter, filterIndex) => {
    const filterErrors = validateFilter(filter);
    filterErrors.errors.forEach(err => {
      errors.push({
        field: `rules.${ruleIndex}.filters.${filterIndex}.${err.field}`,
        message: err.message,
      });
    });
  });

  return { valid: errors.length === 0, errors };
}

// Validate a single filter
export function validateFilter(filter: FilterFormState): ValidationResult {
  const errors: ValidationError[] = [];

  switch (filter.type) {
    case 'RequestRedirect':
      if (filter.requestRedirect) {
        if (filter.requestRedirect.port !== undefined) {
          if (filter.requestRedirect.port < 1 || filter.requestRedirect.port > 65535) {
            errors.push({
              field: 'requestRedirect.port',
              message: 'Port must be between 1 and 65535',
            });
          }
        }
        if (filter.requestRedirect.statusCode !== undefined) {
          if (![301, 302, 303, 307, 308].includes(filter.requestRedirect.statusCode)) {
            errors.push({
              field: 'requestRedirect.statusCode',
              message: 'Status code must be 301, 302, 303, 307, or 308',
            });
          }
        }
        if (filter.requestRedirect.path?.type === 'RegularExpression' && filter.requestRedirect.path.value) {
          try {
            new RegExp(filter.requestRedirect.path.value);
          } catch {
            errors.push({
              field: 'requestRedirect.path.value',
              message: 'Invalid regular expression',
            });
          }
        }
      }
      break;

    case 'URLRewrite':
      if (filter.urlRewrite?.path?.type === 'RegularExpression' && filter.urlRewrite.path.value) {
        try {
          new RegExp(filter.urlRewrite.path.value);
        } catch {
          errors.push({
            field: 'urlRewrite.path.value',
            message: 'Invalid regular expression',
          });
        }
      }
      break;
  }

  return { valid: errors.length === 0, errors };
}

// Helper to get errors for a specific field
export function getFieldErrors(errors: ValidationError[], fieldPrefix: string): string[] {
  return errors
    .filter(err => err.field === fieldPrefix || err.field.startsWith(`${fieldPrefix}.`))
    .map(err => err.message);
}

// Helper to check if a field has errors
export function hasFieldError(errors: ValidationError[], fieldPrefix: string): boolean {
  return getFieldErrors(errors, fieldPrefix).length > 0;
}

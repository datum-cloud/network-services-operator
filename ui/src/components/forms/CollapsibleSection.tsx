'use client';

import { ReactNode, useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';

interface CollapsibleSectionProps {
  title: string;
  summary?: string;
  defaultExpanded?: boolean;
  expanded?: boolean;
  onToggle?: () => void;
  status?: 'default' | 'success' | 'warning' | 'error';
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}

const statusStyles = {
  default: 'border-gray-200 dark:border-dark-700',
  success: 'border-green-200 dark:border-green-800',
  warning: 'border-yellow-200 dark:border-yellow-800',
  error: 'border-red-200 dark:border-red-800',
};

const statusDotStyles = {
  default: 'bg-gray-400',
  success: 'bg-green-500',
  warning: 'bg-yellow-500',
  error: 'bg-red-500',
};

export function CollapsibleSection({
  title,
  summary,
  defaultExpanded = false,
  expanded: controlledExpanded,
  onToggle,
  status = 'default',
  actions,
  children,
  className = '',
}: CollapsibleSectionProps) {
  const [internalExpanded, setInternalExpanded] = useState(defaultExpanded);

  const isControlled = controlledExpanded !== undefined;
  const isExpanded = isControlled ? controlledExpanded : internalExpanded;

  const handleToggle = () => {
    if (onToggle) {
      onToggle();
    } else {
      setInternalExpanded(!internalExpanded);
    }
  };

  return (
    <div className={`border rounded-lg ${statusStyles[status]} ${className}`}>
      <div
        className="flex items-center justify-between p-4 cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-800/50 transition-colors"
        onClick={handleToggle}
      >
        <div className="flex items-center gap-3 min-w-0 flex-1">
          {isExpanded ? (
            <ChevronDown className="w-4 h-4 text-gray-400 flex-shrink-0" />
          ) : (
            <ChevronRight className="w-4 h-4 text-gray-400 flex-shrink-0" />
          )}
          {status !== 'default' && (
            <span className={`w-2 h-2 rounded-full ${statusDotStyles[status]} flex-shrink-0`} />
          )}
          <span className="font-medium text-gray-900 dark:text-white truncate">{title}</span>
          {!isExpanded && summary && (
            <span className="text-sm text-gray-500 dark:text-dark-400 truncate">{summary}</span>
          )}
        </div>
        {actions && (
          <div
            className="flex items-center gap-2 flex-shrink-0 ml-4"
            onClick={(e) => e.stopPropagation()}
          >
            {actions}
          </div>
        )}
      </div>
      {isExpanded && (
        <div className="px-4 pb-4 border-t border-gray-200 dark:border-dark-700 pt-4">
          {children}
        </div>
      )}
    </div>
  );
}

// Simpler inline collapsible for nested use
interface InlineCollapsibleProps {
  label: string;
  defaultExpanded?: boolean;
  children: ReactNode;
  className?: string;
}

export function InlineCollapsible({
  label,
  defaultExpanded = false,
  children,
  className = '',
}: InlineCollapsibleProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);

  return (
    <div className={className}>
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-dark-300 hover:text-gray-900 dark:hover:text-white transition-colors"
      >
        {expanded ? (
          <ChevronDown className="w-4 h-4" />
        ) : (
          <ChevronRight className="w-4 h-4" />
        )}
        {label}
      </button>
      {expanded && <div className="mt-3 pl-6">{children}</div>}
    </div>
  );
}

import { CheckCircle, XCircle, Clock, AlertCircle, MinusCircle } from 'lucide-react';
import { ReactNode } from 'react';

type StatusType = 'success' | 'error' | 'warning' | 'pending' | 'disabled' | 'info';

interface StatusBadgeProps {
  status: StatusType;
  label?: string;
  children?: ReactNode;
  size?: 'sm' | 'md' | 'lg';
  showIcon?: boolean;
}

const statusConfig: Record<StatusType, { bg: string; text: string; icon: typeof CheckCircle }> = {
  success: {
    bg: 'bg-green-100 dark:bg-green-900/30',
    text: 'text-green-700 dark:text-green-400',
    icon: CheckCircle,
  },
  error: {
    bg: 'bg-red-100 dark:bg-red-900/30',
    text: 'text-red-700 dark:text-red-400',
    icon: XCircle,
  },
  warning: {
    bg: 'bg-yellow-100 dark:bg-yellow-900/30',
    text: 'text-yellow-700 dark:text-yellow-400',
    icon: AlertCircle,
  },
  pending: {
    bg: 'bg-blue-100 dark:bg-blue-900/30',
    text: 'text-blue-700 dark:text-blue-400',
    icon: Clock,
  },
  disabled: {
    bg: 'bg-gray-100 dark:bg-dark-700',
    text: 'text-gray-600 dark:text-dark-400',
    icon: MinusCircle,
  },
  info: {
    bg: 'bg-purple-100 dark:bg-purple-900/30',
    text: 'text-purple-700 dark:text-purple-400',
    icon: AlertCircle,
  },
};

const sizeConfig = {
  sm: 'px-2 py-0.5 text-xs',
  md: 'px-2.5 py-1 text-sm',
  lg: 'px-3 py-1.5 text-base',
};

const iconSizeConfig = {
  sm: 'w-3 h-3',
  md: 'w-4 h-4',
  lg: 'w-5 h-5',
};

export function StatusBadge({
  status,
  label,
  children,
  size = 'md',
  showIcon = true,
}: StatusBadgeProps) {
  const config = statusConfig[status];
  const Icon = config.icon;

  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full font-medium ${config.bg} ${config.text} ${sizeConfig[size]}`}
    >
      {showIcon && !children && <Icon className={iconSizeConfig[size]} />}
      {children || label}
    </span>
  );
}

// Helper function to determine status from Kubernetes condition
export function getConditionStatus(
  conditions: Array<{ type: string; status: string }> | undefined,
  type: string
): StatusType {
  if (!conditions) return 'pending';
  const condition = conditions.find((c) => c.type === type);
  if (!condition) return 'pending';
  return condition.status === 'True' ? 'success' : 'error';
}

export function getOverallStatus(
  conditions: Array<{ type: string; status: string }> | undefined
): StatusType {
  if (!conditions || conditions.length === 0) return 'pending';
  const hasError = conditions.some((c) => c.status === 'False');
  const hasPending = conditions.some((c) => c.status === 'Unknown');
  if (hasError) return 'error';
  if (hasPending) return 'pending';
  return 'success';
}

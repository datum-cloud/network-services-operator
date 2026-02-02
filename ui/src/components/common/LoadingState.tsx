import { Loader2 } from 'lucide-react';

interface LoadingStateProps {
  message?: string;
  size?: 'sm' | 'md' | 'lg';
}

const sizeStyles = {
  sm: {
    container: 'py-8',
    icon: 'w-6 h-6',
    text: 'text-sm',
  },
  md: {
    container: 'py-12',
    icon: 'w-8 h-8',
    text: 'text-base',
  },
  lg: {
    container: 'py-16',
    icon: 'w-12 h-12',
    text: 'text-lg',
  },
};

export function LoadingState({ message = 'Loading...', size = 'md' }: LoadingStateProps) {
  const styles = sizeStyles[size];

  return (
    <div className={`flex flex-col items-center justify-center ${styles.container}`}>
      <Loader2 className={`${styles.icon} text-primary-500 animate-spin mb-4`} />
      <p className={`${styles.text} text-gray-500 dark:text-dark-400`}>{message}</p>
    </div>
  );
}

export function LoadingSpinner({ className = '' }: { className?: string }) {
  return <Loader2 className={`animate-spin ${className}`} />;
}

export function LoadingSkeleton({ className = '' }: { className?: string }) {
  return (
    <div
      className={`animate-pulse bg-gray-200 dark:bg-dark-700 rounded ${className}`}
    />
  );
}

export function TableLoadingSkeleton({ rows = 5, columns = 4 }: { rows?: number; columns?: number }) {
  return (
    <div className="bg-white dark:bg-dark-900 rounded-xl border border-gray-200 dark:border-dark-700 overflow-hidden">
      <div className="p-4 border-b border-gray-200 dark:border-dark-700">
        <LoadingSkeleton className="h-10 w-64" />
      </div>
      <table className="w-full">
        <thead>
          <tr className="bg-gray-50 dark:bg-dark-800">
            {Array.from({ length: columns }).map((_, i) => (
              <th key={i} className="px-4 py-3">
                <LoadingSkeleton className="h-4 w-20" />
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-200 dark:divide-dark-700">
          {Array.from({ length: rows }).map((_, rowIndex) => (
            <tr key={rowIndex}>
              {Array.from({ length: columns }).map((_, colIndex) => (
                <td key={colIndex} className="px-4 py-4">
                  <LoadingSkeleton className="h-4 w-full" />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

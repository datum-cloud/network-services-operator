import { ReactNode } from 'react';

interface FormSectionProps {
  title: string;
  description?: string;
  children: ReactNode;
}

export function FormSection({ title, description, children }: FormSectionProps) {
  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-lg font-medium text-gray-900 dark:text-white">{title}</h3>
        {description && (
          <p className="mt-1 text-sm text-gray-500 dark:text-dark-400">{description}</p>
        )}
      </div>
      <div className="space-y-4">{children}</div>
    </div>
  );
}

interface FormGridProps {
  columns?: 1 | 2 | 3 | 4;
  children: ReactNode;
}

const columnStyles = {
  1: 'grid-cols-1',
  2: 'grid-cols-1 sm:grid-cols-2',
  3: 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3',
  4: 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-4',
};

export function FormGrid({ columns = 2, children }: FormGridProps) {
  return <div className={`grid gap-4 ${columnStyles[columns]}`}>{children}</div>;
}

interface FormActionsProps {
  children: ReactNode;
  align?: 'left' | 'right' | 'center' | 'between';
}

const alignStyles = {
  left: 'justify-start',
  right: 'justify-end',
  center: 'justify-center',
  between: 'justify-between',
};

export function FormActions({ children, align = 'right' }: FormActionsProps) {
  return (
    <div
      className={`flex items-center gap-3 pt-6 border-t border-gray-200 dark:border-dark-700 ${alignStyles[align]}`}
    >
      {children}
    </div>
  );
}

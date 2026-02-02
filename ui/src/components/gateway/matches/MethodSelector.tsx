'use client';

import { HTTP_METHODS, HTTPMethodType } from '@/lib/gateway-defaults';

interface MethodSelectorProps {
  value?: string;
  onChange: (value: string | undefined) => void;
}

const methodColors: Record<HTTPMethodType, string> = {
  GET: 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400 border-green-200 dark:border-green-800',
  POST: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400 border-blue-200 dark:border-blue-800',
  PUT: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400 border-yellow-200 dark:border-yellow-800',
  DELETE: 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400 border-red-200 dark:border-red-800',
  PATCH: 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400 border-purple-200 dark:border-purple-800',
  HEAD: 'bg-gray-100 text-gray-700 dark:bg-gray-900/30 dark:text-gray-400 border-gray-200 dark:border-gray-800',
  OPTIONS: 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400 border-cyan-200 dark:border-cyan-800',
};

const methodColorSelected: Record<HTTPMethodType, string> = {
  GET: 'bg-green-600 text-white dark:bg-green-500 border-green-600 dark:border-green-500',
  POST: 'bg-blue-600 text-white dark:bg-blue-500 border-blue-600 dark:border-blue-500',
  PUT: 'bg-yellow-600 text-white dark:bg-yellow-500 border-yellow-600 dark:border-yellow-500',
  DELETE: 'bg-red-600 text-white dark:bg-red-500 border-red-600 dark:border-red-500',
  PATCH: 'bg-purple-600 text-white dark:bg-purple-500 border-purple-600 dark:border-purple-500',
  HEAD: 'bg-gray-600 text-white dark:bg-gray-500 border-gray-600 dark:border-gray-500',
  OPTIONS: 'bg-cyan-600 text-white dark:bg-cyan-500 border-cyan-600 dark:border-cyan-500',
};

export function MethodSelector({ value, onChange }: MethodSelectorProps) {
  const handleClick = (method: HTTPMethodType) => {
    if (value === method) {
      onChange(undefined);
    } else {
      onChange(method);
    }
  };

  return (
    <div className="space-y-2">
      <label className="block text-sm font-medium text-gray-700 dark:text-dark-300">
        HTTP Method
      </label>
      <div className="flex flex-wrap gap-2">
        {HTTP_METHODS.map((method) => {
          const isSelected = value === method;
          return (
            <button
              key={method}
              type="button"
              onClick={() => handleClick(method)}
              className={`px-3 py-1.5 text-sm font-medium rounded-md border transition-colors ${
                isSelected ? methodColorSelected[method] : methodColors[method]
              } hover:opacity-80`}
            >
              {method}
            </button>
          );
        })}
      </div>
      <p className="text-xs text-gray-500 dark:text-dark-400">
        {value ? `Only match ${value} requests` : 'Match all HTTP methods (click to filter)'}
      </p>
    </div>
  );
}

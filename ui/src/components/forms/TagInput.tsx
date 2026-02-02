'use client';

import { useState, KeyboardEvent, FocusEvent } from 'react';
import { X } from 'lucide-react';

interface TagInputProps {
  label?: string;
  value: string[];
  onChange: (value: string[]) => void;
  placeholder?: string;
  error?: string;
  hint?: string;
}

export function TagInput({
  label,
  value,
  onChange,
  placeholder = 'Type to add',
  error,
  hint,
}: TagInputProps) {
  const [inputValue, setInputValue] = useState('');

  const addTag = () => {
    const newTag = inputValue.trim();
    if (newTag && !value.includes(newTag)) {
      onChange([...value, newTag]);
      setInputValue('');
    }
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      addTag();
    } else if (e.key === 'Backspace' && !inputValue && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  };

  const handleBlur = (e: FocusEvent<HTMLInputElement>) => {
    addTag();
  };

  const removeTag = (tagToRemove: string) => {
    onChange(value.filter((tag) => tag !== tagToRemove));
  };

  return (
    <div className="space-y-1">
      {label && (
        <label className="block text-sm font-medium text-gray-700 dark:text-dark-300">
          {label}
        </label>
      )}
      <div
        className={`flex flex-wrap gap-2 p-2 bg-white dark:bg-dark-800 border rounded-lg focus-within:ring-2 focus-within:ring-primary-500 focus-within:border-transparent transition-colors ${
          error ? 'border-red-500' : 'border-gray-300 dark:border-dark-600'
        }`}
      >
        {value.map((tag) => (
          <span
            key={tag}
            className="inline-flex items-center gap-1 px-2 py-1 bg-primary-100 dark:bg-primary-900/30 text-primary-700 dark:text-primary-400 rounded text-sm"
          >
            {tag}
            <button
              type="button"
              onClick={() => removeTag(tag)}
              className="hover:text-primary-900 dark:hover:text-primary-200"
            >
              <X className="w-3 h-3" />
            </button>
          </span>
        ))}
        <input
          type="text"
          value={inputValue}
          onChange={(e) => setInputValue(e.target.value)}
          onKeyDown={handleKeyDown}
          onBlur={handleBlur}
          placeholder={value.length === 0 ? placeholder : ''}
          className="flex-1 min-w-[120px] px-2 py-1 bg-transparent text-sm text-gray-900 dark:text-white placeholder-gray-400 dark:placeholder-dark-500 focus:outline-none"
        />
      </div>
      {error && <p className="text-sm text-red-500">{error}</p>}
      {hint && !error && (
        <p className="text-sm text-gray-500 dark:text-dark-400">{hint}</p>
      )}
    </div>
  );
}

interface KeyValueInputProps {
  label?: string;
  value: Record<string, string>;
  onChange: (value: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
  error?: string;
}

export function KeyValueInput({
  label,
  value,
  onChange,
  keyPlaceholder = 'Key',
  valuePlaceholder = 'Value',
  error,
}: KeyValueInputProps) {
  const [keyInput, setKeyInput] = useState('');
  const [valueInput, setValueInput] = useState('');

  const entries = Object.entries(value);

  const addEntry = () => {
    const key = keyInput.trim();
    const val = valueInput.trim();
    if (key && val) {
      onChange({ ...value, [key]: val });
      setKeyInput('');
      setValueInput('');
    }
  };

  const removeEntry = (keyToRemove: string) => {
    const newValue = { ...value };
    delete newValue[keyToRemove];
    onChange(newValue);
  };

  return (
    <div className="space-y-2">
      {label && (
        <label className="block text-sm font-medium text-gray-700 dark:text-dark-300">
          {label}
        </label>
      )}

      {/* Existing entries */}
      {entries.length > 0 && (
        <div className="space-y-2">
          {entries.map(([key, val]) => (
            <div
              key={key}
              className="flex items-center gap-2 p-2 bg-gray-50 dark:bg-dark-800 rounded-lg"
            >
              <span className="text-sm font-medium text-gray-700 dark:text-dark-300 min-w-[100px]">
                {key}:
              </span>
              <span className="flex-1 text-sm text-gray-600 dark:text-dark-400 truncate">
                {val}
              </span>
              <button
                type="button"
                onClick={() => removeEntry(key)}
                className="p-1 text-gray-400 hover:text-red-500 dark:hover:text-red-400"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Add new entry */}
      <div className="flex gap-2">
        <input
          type="text"
          value={keyInput}
          onChange={(e) => setKeyInput(e.target.value)}
          placeholder={keyPlaceholder}
          className="flex-1 px-3 py-2 bg-white dark:bg-dark-800 border border-gray-300 dark:border-dark-600 rounded-lg text-sm text-gray-900 dark:text-white placeholder-gray-400 dark:placeholder-dark-500 focus:outline-none focus:ring-2 focus:ring-primary-500"
        />
        <input
          type="text"
          value={valueInput}
          onChange={(e) => setValueInput(e.target.value)}
          placeholder={valuePlaceholder}
          onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), addEntry())}
          className="flex-1 px-3 py-2 bg-white dark:bg-dark-800 border border-gray-300 dark:border-dark-600 rounded-lg text-sm text-gray-900 dark:text-white placeholder-gray-400 dark:placeholder-dark-500 focus:outline-none focus:ring-2 focus:ring-primary-500"
        />
        <button
          type="button"
          onClick={addEntry}
          disabled={!keyInput.trim() || !valueInput.trim()}
          className="px-3 py-2 bg-primary-600 text-white rounded-lg text-sm font-medium hover:bg-primary-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          Add
        </button>
      </div>

      {error && <p className="text-sm text-red-500">{error}</p>}
    </div>
  );
}

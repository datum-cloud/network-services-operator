'use client';

import { useState } from 'react';
import { Copy, Check, Download, FileCode } from 'lucide-react';

interface YamlViewerProps {
  data: unknown;
  title?: string;
  className?: string;
}

function toYaml(obj: unknown, indent: number = 0): string {
  const spaces = '  '.repeat(indent);

  if (obj === null || obj === undefined) {
    return 'null';
  }

  if (typeof obj === 'string') {
    // Check if string needs quoting
    if (obj.includes('\n') || obj.includes(':') || obj.includes('#') || obj === '') {
      return `"${obj.replace(/"/g, '\\"')}"`;
    }
    return obj;
  }

  if (typeof obj === 'number' || typeof obj === 'boolean') {
    return String(obj);
  }

  if (Array.isArray(obj)) {
    if (obj.length === 0) return '[]';
    return obj
      .map((item) => {
        const value = toYaml(item, indent + 1);
        if (typeof item === 'object' && item !== null && !Array.isArray(item)) {
          return `${spaces}- ${value.trim().replace(/^  /, '')}`;
        }
        return `${spaces}- ${value}`;
      })
      .join('\n');
  }

  if (typeof obj === 'object') {
    const entries = Object.entries(obj);
    if (entries.length === 0) return '{}';
    return entries
      .map(([key, value]) => {
        if (value === undefined) return null;
        if (typeof value === 'object' && value !== null) {
          const nested = toYaml(value, indent + 1);
          if (Array.isArray(value) && value.length > 0) {
            return `${spaces}${key}:\n${nested}`;
          }
          if (!Array.isArray(value) && Object.keys(value).length > 0) {
            return `${spaces}${key}:\n${nested}`;
          }
          return `${spaces}${key}: ${nested}`;
        }
        return `${spaces}${key}: ${toYaml(value, indent)}`;
      })
      .filter(Boolean)
      .join('\n');
  }

  return String(obj);
}

export function YamlViewer({ data, title, className = '' }: YamlViewerProps) {
  const [copied, setCopied] = useState(false);

  const yamlContent = toYaml(data);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(yamlContent);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (err) {
      console.error('Failed to copy:', err);
    }
  };

  const handleDownload = () => {
    const blob = new Blob([yamlContent], { type: 'text/yaml' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${title || 'resource'}.yaml`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  return (
    <div className={`bg-dark-900 rounded-xl border border-dark-700 overflow-hidden ${className}`}>
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 bg-dark-800 border-b border-dark-700">
        <div className="flex items-center gap-2 text-dark-300">
          <FileCode className="w-4 h-4" />
          <span className="text-sm font-medium">{title || 'YAML'}</span>
        </div>
        <div className="flex items-center gap-1">
          <button
            onClick={handleCopy}
            className="p-2 rounded-lg text-dark-400 hover:text-white hover:bg-dark-700 transition-colors"
            aria-label="Copy to clipboard"
          >
            {copied ? (
              <Check className="w-4 h-4 text-green-400" />
            ) : (
              <Copy className="w-4 h-4" />
            )}
          </button>
          <button
            onClick={handleDownload}
            className="p-2 rounded-lg text-dark-400 hover:text-white hover:bg-dark-700 transition-colors"
            aria-label="Download YAML"
          >
            <Download className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="p-4 overflow-x-auto">
        <pre className="text-sm font-mono text-dark-200 whitespace-pre">
          <SyntaxHighlight yaml={yamlContent} />
        </pre>
      </div>
    </div>
  );
}

// Simple YAML syntax highlighting
function SyntaxHighlight({ yaml }: { yaml: string }) {
  const lines = yaml.split('\n');

  return (
    <>
      {lines.map((line, index) => {
        // Highlight keys
        const keyMatch = line.match(/^(\s*)([a-zA-Z0-9_-]+)(:)/);
        if (keyMatch) {
          const [, indent, key, colon] = keyMatch;
          const rest = line.slice(keyMatch[0].length);

          // Check if rest is a value
          const isStringValue = rest.trim().startsWith('"') || (!rest.includes(':') && rest.trim().length > 0);
          const isBoolOrNull = /^\s*(true|false|null)\s*$/.test(rest);
          const isNumber = /^\s*-?\d+\.?\d*\s*$/.test(rest);

          return (
            <div key={index}>
              {indent}
              <span className="text-primary-400">{key}</span>
              <span className="text-dark-400">{colon}</span>
              {isBoolOrNull ? (
                <span className="text-yellow-400">{rest}</span>
              ) : isNumber ? (
                <span className="text-green-400">{rest}</span>
              ) : isStringValue ? (
                <span className="text-orange-300">{rest}</span>
              ) : (
                rest
              )}
            </div>
          );
        }

        // Highlight array items
        const arrayMatch = line.match(/^(\s*)(- )(.*)/);
        if (arrayMatch) {
          const [, indent, dash, rest] = arrayMatch;
          return (
            <div key={index}>
              {indent}
              <span className="text-dark-400">{dash}</span>
              <span className="text-orange-300">{rest}</span>
            </div>
          );
        }

        return <div key={index}>{line}</div>;
      })}
    </>
  );
}

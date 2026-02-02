'use client';

import { X, Plus, Server, Link as LinkIcon } from 'lucide-react';
import { Input } from '@/components/forms/Input';
import { Select } from '@/components/forms/Select';
import { Button } from '@/components/common/Button';
import type { BackendFormState } from '@/lib/gateway-defaults';
import type { Connector } from '@/api/types';

interface BackendEditorProps {
  backends: BackendFormState[];
  ruleId: string;
  connectors?: Connector[];
  onAdd: (ruleId: string) => void;
  onRemove: (ruleId: string, backendId: string) => void;
  onUpdate: (ruleId: string, backendId: string, backend: Partial<BackendFormState>) => void;
  errors?: string[];
}

export function BackendEditor({
  backends,
  ruleId,
  connectors = [],
  onAdd,
  onRemove,
  onUpdate,
  errors = [],
}: BackendEditorProps) {
  const connectorOptions = [
    { value: '', label: 'Direct connection (no connector)' },
    ...connectors.map((c) => ({
      value: c.metadata.name,
      label: `${c.metadata.name} (${c.metadata.namespace || 'default'})`,
    })),
  ];

  // Check if backend URL is HTTPS with an IP address
  const needsTlsHostname = (endpoint: string): boolean => {
    try {
      const url = new URL(endpoint);
      if (url.protocol !== 'https:') return false;
      // Check if hostname is an IP address
      const ipv4Regex = /^(\d{1,3}\.){3}\d{1,3}$/;
      const ipv6Regex = /^\[[\da-fA-F:]+\]$/;
      return ipv4Regex.test(url.hostname) || ipv6Regex.test(url.hostname);
    } catch {
      return false;
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium text-gray-900 dark:text-white flex items-center gap-2">
          <Server className="w-4 h-4" />
          Backends ({backends.length})
        </h4>
        <Button
          type="button"
          variant="outline"
          size="sm"
          icon={<Plus className="w-4 h-4" />}
          onClick={() => onAdd(ruleId)}
        >
          Add Backend
        </Button>
      </div>

      {backends.length === 0 ? (
        <div className="text-center py-8 bg-gray-50 dark:bg-dark-800 rounded-lg">
          <Server className="w-8 h-8 mx-auto text-gray-400 dark:text-dark-500 mb-2" />
          <p className="text-sm text-gray-500 dark:text-dark-400">No backends configured</p>
          <p className="text-xs text-gray-400 dark:text-dark-500 mt-1">
            At least one backend is required
          </p>
        </div>
      ) : (
        <div className="space-y-4">
          {backends.map((backend, index) => (
            <div
              key={backend.id}
              className="p-4 bg-gray-50 dark:bg-dark-800 rounded-lg space-y-4"
            >
              <div className="flex items-start justify-between gap-4">
                <div className="flex-1 space-y-4">
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase">
                      Backend {index + 1}
                    </span>
                    {backend.weight !== undefined && (
                      <span className="text-xs px-2 py-0.5 bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded">
                        Weight: {backend.weight}
                      </span>
                    )}
                  </div>

                  <Input
                    label="Endpoint URL"
                    value={backend.endpoint}
                    onChange={(e) =>
                      onUpdate(ruleId, backend.id, { endpoint: e.target.value })
                    }
                    placeholder="http://my-service:8080 or https://api.example.com"
                    icon={<LinkIcon className="w-4 h-4" />}
                    hint="Full URL including protocol and port"
                    required
                  />

                  {needsTlsHostname(backend.endpoint) && (
                    <Input
                      label="TLS Hostname"
                      value={backend.tls?.hostname || ''}
                      onChange={(e) =>
                        onUpdate(ruleId, backend.id, {
                          tls: { hostname: e.target.value },
                        })
                      }
                      placeholder="api.example.com"
                      hint="Required for HTTPS backends with IP addresses (for SNI and certificate verification)"
                      required
                    />
                  )}

                  {connectors.length > 0 && (
                    <Select
                      label="Connector"
                      value={backend.connectorRef?.name || ''}
                      onChange={(e) =>
                        onUpdate(ruleId, backend.id, {
                          connectorRef: e.target.value
                            ? { name: e.target.value }
                            : undefined,
                        })
                      }
                      options={connectorOptions}
                      hint="Optional: Route through a connector for private backends"
                    />
                  )}

                  {backends.length > 1 && (
                    <Input
                      type="number"
                      label="Weight"
                      value={backend.weight?.toString() || ''}
                      onChange={(e) =>
                        onUpdate(ruleId, backend.id, {
                          weight: e.target.value ? parseInt(e.target.value, 10) : undefined,
                        })
                      }
                      placeholder="1"
                      min={0}
                      max={1000000}
                      hint="Relative weight for load balancing (0-1000000)"
                    />
                  )}
                </div>

                {backends.length > 1 && (
                  <button
                    type="button"
                    onClick={() => onRemove(ruleId, backend.id)}
                    className="p-2 text-gray-400 hover:text-red-500 dark:hover:text-red-400 transition-colors"
                    title="Remove backend"
                  >
                    <X className="w-4 h-4" />
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {errors.length > 0 && (
        <div className="text-sm text-red-500">
          {errors.map((error, idx) => (
            <p key={idx}>{error}</p>
          ))}
        </div>
      )}
    </div>
  );
}

'use client';

import { useState } from 'react';
import { Send, Clock, AlertCircle, CheckCircle2, ChevronDown, ChevronUp, Plus, Trash2 } from 'lucide-react';
import { Modal } from '@/components/common/Modal';
import { Button } from '@/components/common/Button';
import { useTestHTTPProxy } from '@/hooks/useApi';
import type { HTTPProxy, HTTPMethod, TestProxyRequest, TestProxyResponse } from '@/api/types';

interface TestProxyModalProps {
  isOpen: boolean;
  onClose: () => void;
  proxy: HTTPProxy;
}

const HTTP_METHODS: HTTPMethod[] = ['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'];

const methodColors: Record<HTTPMethod, string> = {
  GET: 'text-green-600 bg-green-50 dark:bg-green-900/20 dark:text-green-400',
  POST: 'text-blue-600 bg-blue-50 dark:bg-blue-900/20 dark:text-blue-400',
  PUT: 'text-yellow-600 bg-yellow-50 dark:bg-yellow-900/20 dark:text-yellow-400',
  DELETE: 'text-red-600 bg-red-50 dark:bg-red-900/20 dark:text-red-400',
  PATCH: 'text-purple-600 bg-purple-50 dark:bg-purple-900/20 dark:text-purple-400',
  HEAD: 'text-gray-600 bg-gray-50 dark:bg-gray-900/20 dark:text-gray-400',
  OPTIONS: 'text-gray-600 bg-gray-50 dark:bg-gray-900/20 dark:text-gray-400',
};

function getStatusColor(statusCode: number): string {
  if (statusCode >= 200 && statusCode < 300) {
    return 'text-green-600 bg-green-50 dark:bg-green-900/20 dark:text-green-400';
  } else if (statusCode >= 300 && statusCode < 400) {
    return 'text-blue-600 bg-blue-50 dark:bg-blue-900/20 dark:text-blue-400';
  } else if (statusCode >= 400 && statusCode < 500) {
    return 'text-yellow-600 bg-yellow-50 dark:bg-yellow-900/20 dark:text-yellow-400';
  } else if (statusCode >= 500) {
    return 'text-red-600 bg-red-50 dark:bg-red-900/20 dark:text-red-400';
  }
  return 'text-gray-600 bg-gray-50 dark:bg-gray-900/20 dark:text-gray-400';
}

export function TestProxyModal({ isOpen, onClose, proxy }: TestProxyModalProps) {
  const [method, setMethod] = useState<HTTPMethod>('GET');
  const [path, setPath] = useState('/');
  const [headers, setHeaders] = useState<Array<{ key: string; value: string }>>([]);
  const [body, setBody] = useState('');
  const [showHeaders, setShowHeaders] = useState(false);
  const [showBody, setShowBody] = useState(false);
  const [response, setResponse] = useState<TestProxyResponse | null>(null);
  const [showResponseHeaders, setShowResponseHeaders] = useState(false);

  const testMutation = useTestHTTPProxy();

  const handleSubmit = async () => {
    const request: TestProxyRequest = {
      method,
      path,
      headers: headers.reduce((acc, h) => {
        if (h.key.trim()) {
          acc[h.key] = h.value;
        }
        return acc;
      }, {} as Record<string, string>),
      body: ['POST', 'PUT', 'PATCH'].includes(method) ? body : undefined,
    };

    try {
      const result = await testMutation.mutateAsync({
        name: proxy.metadata.name,
        namespace: proxy.metadata.namespace || 'default',
        request,
      });
      setResponse(result);
    } catch (error) {
      setResponse({
        statusCode: 0,
        statusText: 'Error',
        headers: {},
        body: '',
        latencyMs: 0,
        timestamp: new Date().toISOString(),
        error: error instanceof Error ? error.message : 'Unknown error',
      });
    }
  };

  const addHeader = () => {
    setHeaders([...headers, { key: '', value: '' }]);
  };

  const updateHeader = (index: number, field: 'key' | 'value', value: string) => {
    const newHeaders = [...headers];
    newHeaders[index][field] = value;
    setHeaders(newHeaders);
  };

  const removeHeader = (index: number) => {
    setHeaders(headers.filter((_, i) => i !== index));
  };

  const targetHost = proxy.status?.addresses?.find(a => a.type === 'Hostname')?.value
    || proxy.status?.hostnames?.[0]
    || proxy.spec.hostnames?.[0]
    || 'unknown';

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title="Test Proxy"
      size="xl"
    >
      <div className="space-y-6">
        {/* Target Info */}
        <div className="p-3 bg-gray-50 dark:bg-dark-800 rounded-lg">
          <div className="text-xs font-medium text-gray-500 dark:text-dark-400 uppercase mb-1">Target</div>
          <div className="text-sm font-mono text-gray-900 dark:text-white">{targetHost}</div>
        </div>

        {/* Request Builder */}
        <div className="space-y-4">
          <h3 className="text-sm font-medium text-gray-900 dark:text-white">Request</h3>

          {/* Method and Path */}
          <div className="flex gap-2">
            <select
              value={method}
              onChange={(e) => setMethod(e.target.value as HTTPMethod)}
              className={`px-3 py-2 rounded-lg font-medium text-sm border-0 focus:ring-2 focus:ring-primary-500 ${methodColors[method]}`}
            >
              {HTTP_METHODS.map((m) => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
            <div className="flex-1 flex items-center bg-gray-50 dark:bg-dark-800 rounded-lg px-3">
              <span className="text-gray-400 text-sm">https://{targetHost}</span>
              <input
                type="text"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                placeholder="/"
                className="flex-1 bg-transparent border-0 text-sm text-gray-900 dark:text-white focus:ring-0 px-1"
              />
            </div>
          </div>

          {/* Headers Section */}
          <div className="border border-gray-200 dark:border-dark-700 rounded-lg overflow-hidden">
            <button
              type="button"
              onClick={() => setShowHeaders(!showHeaders)}
              className="w-full flex items-center justify-between px-4 py-2 bg-gray-50 dark:bg-dark-800 text-sm font-medium text-gray-700 dark:text-dark-200 hover:bg-gray-100 dark:hover:bg-dark-700"
            >
              <span>Headers {headers.length > 0 && `(${headers.length})`}</span>
              {showHeaders ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
            </button>
            {showHeaders && (
              <div className="p-4 space-y-2">
                {headers.map((header, index) => (
                  <div key={index} className="flex gap-2">
                    <input
                      type="text"
                      value={header.key}
                      onChange={(e) => updateHeader(index, 'key', e.target.value)}
                      placeholder="Header name"
                      className="flex-1 px-3 py-2 text-sm border border-gray-200 dark:border-dark-700 rounded-lg bg-white dark:bg-dark-900 text-gray-900 dark:text-white focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                    />
                    <input
                      type="text"
                      value={header.value}
                      onChange={(e) => updateHeader(index, 'value', e.target.value)}
                      placeholder="Value"
                      className="flex-1 px-3 py-2 text-sm border border-gray-200 dark:border-dark-700 rounded-lg bg-white dark:bg-dark-900 text-gray-900 dark:text-white focus:ring-2 focus:ring-primary-500 focus:border-transparent"
                    />
                    <button
                      type="button"
                      onClick={() => removeHeader(index)}
                      className="p-2 text-gray-400 hover:text-red-500 transition-colors"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                ))}
                <button
                  type="button"
                  onClick={addHeader}
                  className="flex items-center gap-1 text-sm text-primary-600 hover:text-primary-700 dark:text-primary-400"
                >
                  <Plus className="w-4 h-4" />
                  Add Header
                </button>
              </div>
            )}
          </div>

          {/* Body Section (for POST, PUT, PATCH) */}
          {['POST', 'PUT', 'PATCH'].includes(method) && (
            <div className="border border-gray-200 dark:border-dark-700 rounded-lg overflow-hidden">
              <button
                type="button"
                onClick={() => setShowBody(!showBody)}
                className="w-full flex items-center justify-between px-4 py-2 bg-gray-50 dark:bg-dark-800 text-sm font-medium text-gray-700 dark:text-dark-200 hover:bg-gray-100 dark:hover:bg-dark-700"
              >
                <span>Body</span>
                {showBody ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
              </button>
              {showBody && (
                <div className="p-4">
                  <textarea
                    value={body}
                    onChange={(e) => setBody(e.target.value)}
                    placeholder='{"key": "value"}'
                    rows={5}
                    className="w-full px-3 py-2 text-sm font-mono border border-gray-200 dark:border-dark-700 rounded-lg bg-white dark:bg-dark-900 text-gray-900 dark:text-white focus:ring-2 focus:ring-primary-500 focus:border-transparent resize-y"
                  />
                </div>
              )}
            </div>
          )}

          {/* Send Button */}
          <div className="flex justify-end">
            <Button
              onClick={handleSubmit}
              loading={testMutation.isPending}
              icon={<Send className="w-4 h-4" />}
            >
              Send Request
            </Button>
          </div>
        </div>

        {/* Response */}
        {response && (
          <div className="space-y-4">
            <h3 className="text-sm font-medium text-gray-900 dark:text-white">Response</h3>

            {response.error ? (
              <div className="p-4 bg-red-50 dark:bg-red-900/20 rounded-lg flex items-start gap-3">
                <AlertCircle className="w-5 h-5 text-red-500 flex-shrink-0 mt-0.5" />
                <div>
                  <div className="font-medium text-red-700 dark:text-red-400">Request Failed</div>
                  <div className="text-sm text-red-600 dark:text-red-300 mt-1">{response.error}</div>
                </div>
              </div>
            ) : (
              <div className="space-y-4">
                {/* Status and Latency */}
                <div className="flex items-center gap-4">
                  <div className={`inline-flex items-center gap-2 px-3 py-1.5 rounded-lg font-medium text-sm ${getStatusColor(response.statusCode)}`}>
                    {response.statusCode >= 200 && response.statusCode < 300 ? (
                      <CheckCircle2 className="w-4 h-4" />
                    ) : (
                      <AlertCircle className="w-4 h-4" />
                    )}
                    {response.statusCode} {response.statusText}
                  </div>
                  <div className="flex items-center gap-1 text-sm text-gray-500 dark:text-dark-400">
                    <Clock className="w-4 h-4" />
                    {response.latencyMs}ms
                  </div>
                </div>

                {/* Response Headers */}
                <div className="border border-gray-200 dark:border-dark-700 rounded-lg overflow-hidden">
                  <button
                    type="button"
                    onClick={() => setShowResponseHeaders(!showResponseHeaders)}
                    className="w-full flex items-center justify-between px-4 py-2 bg-gray-50 dark:bg-dark-800 text-sm font-medium text-gray-700 dark:text-dark-200 hover:bg-gray-100 dark:hover:bg-dark-700"
                  >
                    <span>Headers ({Object.keys(response.headers).length})</span>
                    {showResponseHeaders ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
                  </button>
                  {showResponseHeaders && (
                    <div className="p-4 space-y-1 max-h-48 overflow-y-auto">
                      {Object.entries(response.headers).map(([key, value]) => (
                        <div key={key} className="flex gap-2 text-sm">
                          <span className="font-medium text-gray-700 dark:text-dark-200">{key}:</span>
                          <span className="text-gray-600 dark:text-dark-300 break-all">{value}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>

                {/* Response Body */}
                <div className="border border-gray-200 dark:border-dark-700 rounded-lg overflow-hidden">
                  <div className="px-4 py-2 bg-gray-50 dark:bg-dark-800 text-sm font-medium text-gray-700 dark:text-dark-200">
                    Body
                  </div>
                  <pre className="p-4 text-sm font-mono text-gray-700 dark:text-dark-200 overflow-x-auto max-h-64 overflow-y-auto whitespace-pre-wrap break-words">
                    {response.body || '(empty)'}
                  </pre>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </Modal>
  );
}

import { MetricsError, metricsErrorMessage, queryRange, shouldRetryMetrics } from './metrics';
import { afterEach, describe, expect, it, vi } from 'vitest';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const WINDOW = { start: 0, end: 3_600_000, stepSeconds: 60 };

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('queryRange', () => {
  it('posts a step-aligned range query to the project-scoped queryapi', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        status: 'success',
        data: {
          resultType: 'matrix',
          result: [{ metric: { network: 'web-0-eth0' }, values: [[3600, '8'], [3660, 'NaN'], [3720, '16']] }],
        },
      })
    );
    vi.stubGlobal('fetch', fetchMock);

    const result = await queryRange(
      'demo',
      'up',
      { start: 3_630_000, end: 7_230_000, stepSeconds: 60 },
      'network'
    );

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(
      '/api/proxy/apis/resourcemanager.miloapis.com/v1alpha1/projects/demo/control-plane/apis/o11y.miloapis.com/v1alpha1/metrics/api/v1/query_range'
    );
    expect(init.method).toBe('POST');
    expect(init.headers['Content-Type']).toBe('application/x-www-form-urlencoded');
    expect(Object.fromEntries(new URLSearchParams(init.body))).toEqual({
      query: 'up',
      start: '3600',
      end: '7230',
      step: '60s',
    });
    expect(result).toEqual({
      start: 3_600_000,
      end: 7_230_000,
      stepMs: 60_000,
      series: [{ key: 'web-0-eth0', points: [[3_600_000, 8], [3_720_000, 16]] }],
    });
  });

  it('aligns the start to the step so refreshes share a grid', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ status: 'success', data: { result: [] } }));
    vi.stubGlobal('fetch', fetchMock);

    await queryRange('demo', 'up', { start: 1_000_000, end: 4_600_000, stepSeconds: 300 }, 'network');

    expect(Object.fromEntries(new URLSearchParams(fetchMock.mock.calls[0][1].body))).toMatchObject({
      start: '900',
      end: '4600',
      step: '300s',
    });
  });

  it('surfaces the status and error text of a failed request', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ message: 'forbidden' }, 403)));
    await expect(queryRange('demo', 'up', WINDOW, 'network')).rejects.toMatchObject({
      name: 'MetricsError',
      status: 403,
      message: 'forbidden',
    });
  });

  it('treats an error envelope as a bad query', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ status: 'error', error: 'parse error' })));
    await expect(queryRange('demo', 'up', WINDOW, 'network')).rejects.toMatchObject({ status: 400 });
  });
});

describe('errors', () => {
  it('does not retry what a retry cannot fix', () => {
    expect(shouldRetryMetrics(0, new MetricsError('x', 403))).toBe(false);
    expect(shouldRetryMetrics(0, new MetricsError('x', 500))).toBe(true);
    expect(shouldRetryMetrics(2, new MetricsError('x', 500))).toBe(false);
  });

  it('maps statuses to user-facing text', () => {
    expect(metricsErrorMessage(new MetricsError('x', 401))).toBe("You don't have permission to view metrics");
    expect(metricsErrorMessage(new MetricsError('x', 422))).toMatch(/Too much data/);
    expect(metricsErrorMessage(new MetricsError('x', 504))).toMatch(/Took too long/);
    expect(metricsErrorMessage(new Error('x'))).toBe('Unable to load metrics');
  });
});

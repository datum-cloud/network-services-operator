// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

const (
	O11yLogsQueryRangePath = "/apis/o11y.miloapis.com/v1alpha1/logs/loki/api/v1/query_range"
	DefaultLogsLimit       = 100
	MaxLogsLimit           = 500
	LogsFollowInterval     = 5 * time.Second
)

type LokiQueryRangeResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		ResultType string       `json:"resultType"`
		Result     []LokiStream `json:"result"`
	} `json:"data"`
}

type LokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

type LogEntry struct {
	Time   time.Time
	Labels map[string]string
	Line   string
}

type LogsQuery struct {
	Query     string
	Start     time.Time
	End       time.Time
	Limit     int
	Direction string
}

func QueryLogs(ctx context.Context, cfg *rest.Config, q LogsQuery) ([]LogEntry, error) {
	if q.Limit <= 0 {
		q.Limit = DefaultLogsLimit
	}
	if q.Limit > MaxLogsLimit {
		q.Limit = MaxLogsLimit
	}
	if q.Direction == "" {
		q.Direction = "backward"
	}
	if q.End.IsZero() {
		q.End = time.Now().UTC()
	}
	if q.Start.IsZero() {
		q.Start = q.End.Add(-30 * time.Minute)
	}

	params := url.Values{}
	params.Set("query", q.Query)
	params.Set("start", q.Start.UTC().Format(time.RFC3339Nano))
	params.Set("end", q.End.UTC().Format(time.RFC3339Nano))
	params.Set("limit", strconv.Itoa(q.Limit))
	params.Set("direction", q.Direction)

	u, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, NewCLIError(ExitUnavailable, fmt.Sprintf("invalid API host: %v", err)).WithCause(err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + O11yLogsQueryRangePath
	u.RawQuery = params.Encode()

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, NewCLIError(ExitUnavailable, fmt.Sprintf("building logs client: %v", err)).WithCause(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent())
	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, ClassifyError(fmt.Errorf("querying access logs: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, ClassifyError(fmt.Errorf("reading access logs: %w", err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, classifyHTTPStatus(resp.StatusCode, body)
	}

	var decoded LokiQueryRangeResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, NewCLIError(ExitUnavailable, "the logs API returned an unexpected response").
			WithCause(err)
	}
	if decoded.Status == "error" {
		msg := decoded.Error
		if msg == "" {
			msg = "log query failed"
		}
		return nil, NewCLIError(ExitInvalid, msg)
	}
	return FlattenLokiStreams(decoded), nil
}

func FlattenLokiStreams(resp LokiQueryRangeResponse) []LogEntry {
	var entries []LogEntry
	for _, stream := range resp.Data.Result {
		labels := pickAlbLogLabels(stream.Stream)
		for _, pair := range stream.Values {
			if len(pair) < 2 {
				continue
			}
			ts, err := parseLokiTimestamp(pair[0])
			if err != nil {
				continue
			}
			entries = append(entries, LogEntry{
				Time:   ts,
				Labels: labels,
				Line:   pair[1],
			})
		}
	}
	return entries
}

func pickAlbLogLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	allow := map[string]bool{
		"method": true, "path": true, "response_code": true, "duration": true,
		"authority": true, "requested_server_name": true, "x_forwarded_host": true,
		"referer": true, "request_id": true, "protocol": true, "user_agent": true,
		"upstream_host": true, "response_flags": true,
	}
	out := make(map[string]string, len(allow))
	for k, v := range in {
		if !allow[k] || v == "" {
			continue
		}
		if k == "duration" {
			v = formatDurationLabel(v)
		}
		out[k] = v
	}
	if host := logRequestHost(out); host != "" {
		out["host"] = host
	}
	return out
}

func logRequestHost(labels map[string]string) string {
	for _, key := range []string{"requested_server_name", "x_forwarded_host", "authority"} {
		if v := strings.TrimSpace(labels[key]); v != "" {
			return v
		}
	}
	return ""
}

func formatDurationLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return raw
		}
	}
	return raw + "ms"
}

func parseLokiTimestamp(raw string) (time.Time, error) {
	ns, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, ns).UTC(), nil
}

func classifyHTTPStatus(code int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(code)
	}
	switch code {
	case http.StatusUnauthorized:
		return NewCLIError(ExitForbidden, "not authenticated: "+msg).
			WithFix("your session has expired — re-run:\n       datumctl login")
	case http.StatusForbidden:
		return NewCLIError(ExitForbidden, "not authorized to view access logs: "+msg).
			WithFix("confirm you can open Logs for this load balancer in the cloud portal.")
	case http.StatusNotFound:
		return NewCLIError(ExitUnavailable, "access logs are not available in this project yet").
			WithFix("open Logs in the cloud portal for this load balancer; if that works, retry here.")
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return NewCLIError(ExitInvalid, "invalid log query: "+msg)
	default:
		if code >= 500 {
			return NewCLIError(ExitUnavailable, "the logs API is unavailable: "+msg)
		}
		return NewCLIError(ExitError, fmt.Sprintf("logs API returned %d: %s", code, msg))
	}
}

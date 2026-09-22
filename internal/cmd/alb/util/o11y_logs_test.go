// SPDX-License-Identifier: AGPL-3.0-only

package util

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

func TestFlattenLokiStreams(t *testing.T) {
	t.Parallel()

	entries := FlattenLokiStreams(LokiQueryRangeResponse{
		Status: "success",
		Data: struct {
			ResultType string       `json:"resultType"`
			Result     []LokiStream `json:"result"`
		}{
			ResultType: "streams",
			Result: []LokiStream{{
				Stream: map[string]string{
					"method":                "GET",
					"path":                  "/health",
					"response_code":         "200",
					"duration":              "12",
					"requested_server_name": "app.example.com",
					"k8s_pod_name":          "ignore-me",
				},
				Values: [][]string{
					{"1700000000000000000", ""},
				},
			}},
		},
	})
	require.Len(t, entries, 1)
	assert.Equal(t, "GET", entries[0].Labels["method"])
	assert.Equal(t, "200", entries[0].Labels["response_code"])
	assert.Equal(t, "12ms", entries[0].Labels["duration"])
	assert.Equal(t, "app.example.com", entries[0].Labels["host"])
	assert.NotContains(t, entries[0].Labels, "k8s_pod_name")
	assert.True(t, entries[0].Time.Equal(time.Unix(0, 1700000000000000000).UTC()))
}

func TestQueryLogs(t *testing.T) {
	t.Parallel()

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, O11yLogsQueryRangePath, r.URL.Path)
		gotQuery = r.URL.Query().Get("query")
		_ = json.NewEncoder(w).Encode(LokiQueryRangeResponse{
			Status: "success",
			Data: struct {
				ResultType string       `json:"resultType"`
				Result     []LokiStream `json:"result"`
			}{
				ResultType: "streams",
				Result: []LokiStream{{
					Stream: map[string]string{"method": "GET", "path": "/", "response_code": "200"},
					Values: [][]string{{"1700000000000000000", ""}},
				}},
			},
		})
	}))
	defer srv.Close()

	cfg := &rest.Config{Host: srv.URL}
	entries, err := QueryLogs(context.Background(), cfg, LogsQuery{
		Query: `{route_name=~"httproute/[^/]+/my-app/.*"}`,
		Start: time.Now().Add(-time.Hour),
		End:   time.Now(),
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, `{route_name=~"httproute/[^/]+/my-app/.*"}`, gotQuery)
	assert.Equal(t, "GET", entries[0].Labels["method"])
}

func TestQueryLogsForbidden(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := QueryLogs(context.Background(), &rest.Config{Host: srv.URL}, LogsQuery{
		Query: `{route_name=~"x"}`,
		Start: time.Now().Add(-time.Minute),
		End:   time.Now(),
	})
	require.Error(t, err)
	var cliErr *CLIError
	require.ErrorAs(t, err, &cliErr)
	assert.Equal(t, ExitForbidden, cliErr.Code())
}

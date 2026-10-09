// SPDX-License-Identifier: AGPL-3.0-only

package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func newTestLogger(t *testing.T, args ...string) (logr.Logger, *bytes.Buffer) {
	t.Helper()
	opts := zap.Options{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts.BindFlags(fs)
	require.NoError(t, fs.Parse(args))
	buf := &bytes.Buffer{}
	opts.DestWriter = buf
	return New(&opts), buf
}

func jsonLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "line is not JSON: %s", line)
		entries = append(entries, entry)
	}
	return entries
}

func TestDefaultsLogInfoAsJSONWithStacktraceOnlyOnError(t *testing.T) {
	logger, buf := newTestLogger(t)

	logger.V(1).Info("routine detail")
	logger.Info("something happened", "key", "value")
	logger.Error(errors.New("boom"), "something failed")

	entries := jsonLines(t, buf)
	require.Len(t, entries, 2)

	assert.Equal(t, "info", entries[0]["level"])
	assert.Equal(t, "something happened", entries[0]["msg"])
	assert.Equal(t, "value", entries[0]["key"])
	assert.NotContains(t, entries[0], "stacktrace")

	assert.Equal(t, "error", entries[1]["level"])
	assert.Equal(t, "something failed", entries[1]["msg"])
	assert.Equal(t, "boom", entries[1]["error"])
	assert.Contains(t, entries[1], "stacktrace")
}

func TestDefaultsDoNotSample(t *testing.T) {
	logger, buf := newTestLogger(t)

	const n = 200
	for range n {
		logger.Info("identical line")
	}

	assert.Len(t, jsonLines(t, buf), n)
}

func TestDefaultsLogKubernetesObjectsByReference(t *testing.T) {
	logger, buf := newTestLogger(t)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "creds"},
		Data:       map[string][]byte{"password": []byte("hunter2")},
	}
	logger.Info("saw object", "object", secret)

	assert.NotContains(t, buf.String(), "hunter2")
	assert.NotContains(t, buf.String(), "aHVudGVyMg==")
	entries := jsonLines(t, buf)
	require.Len(t, entries, 1)
	assert.Equal(t, map[string]any{"namespace": "ns", "name": "creds"}, entries[0]["object"])
}

func TestFlagsOverrideDefaults(t *testing.T) {
	t.Run("log level", func(t *testing.T) {
		logger, buf := newTestLogger(t, "--zap-log-level=debug")

		logger.V(1).Info("routine detail")

		entries := jsonLines(t, buf)
		require.Len(t, entries, 1)
		assert.Equal(t, "debug", entries[0]["level"])
	})

	t.Run("console encoder", func(t *testing.T) {
		logger, buf := newTestLogger(t, "--zap-encoder=console")

		logger.Info("something happened")

		out := buf.String()
		assert.Contains(t, out, "\tINFO\tsomething happened")
		assert.False(t, json.Valid(bytes.TrimSpace(buf.Bytes())))
	})

	t.Run("stacktrace level", func(t *testing.T) {
		logger, buf := newTestLogger(t, "--zap-stacktrace-level=info")

		logger.Info("something happened")

		entries := jsonLines(t, buf)
		require.Len(t, entries, 1)
		assert.Contains(t, entries[0], "stacktrace")
	})

	t.Run("development mode", func(t *testing.T) {
		logger, buf := newTestLogger(t, "--zap-devel=true")

		logger.V(1).Info("routine detail")

		out := buf.String()
		assert.Contains(t, out, "\tDEBUG\troutine detail")
		assert.False(t, json.Valid(bytes.TrimSpace(buf.Bytes())))
	})
}

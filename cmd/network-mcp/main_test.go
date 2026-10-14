// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"

	"go.datum.net/network-services-operator/internal/mcpserver"
)

func TestTheProjectComesFromTheHeader(t *testing.T) {
	base := &rest.Config{Host: "https://api.datum.net"}
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer abc")
	r.Header.Set(mcpserver.ProjectHeader, "from-the-header")

	deps, err := depsFromRequest(r, base)(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, deps.Reader)
	assert.Equal(t, "default", deps.Namespace)
}

func TestAToolCallWithoutCredentialsFails(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	_, err := depsFromRequest(r, &rest.Config{Host: "https://api.datum.net"})(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not forward the user's identity")
}

func TestEveryURLTheCapabilityDocumentPromisesIsServed(t *testing.T) {
	h, err := newKnowledgeHandler()
	require.NoError(t, err)

	paths := h.Paths()
	for _, want := range []string{
		mcpserver.KnowledgePath,
		"/runbooks/network-create.md",
		"/runbooks/network-service-create.md",
		"/runbooks/network-not-ready.md",
		"/runbooks/network-location-triage.md",
		"/runbooks/network-interface-triage.md",
		"/runbooks/network-service-not-serving.md",
		"/runbooks/network-deletion-stuck.md",
	} {
		assert.Contains(t, paths, want)
	}
	for _, p := range paths {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		assert.Equal(t, http.StatusOK, w.Code, p)
		assert.NotEmpty(t, w.Body.String(), p)
	}
}

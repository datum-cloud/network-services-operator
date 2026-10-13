// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.datum.net/network-services-operator/internal/mcpserver"
)

// TestEveryURLTheCapabilityDocumentPromisesIsServed is the point of embedding
// the documents rather than mounting them: a stripped image cannot silently
// answer 404 for knowledge the capability document says is there.
func TestEveryURLTheCapabilityDocumentPromisesIsServed(t *testing.T) {
	h, err := newKnowledgeHandler()
	require.NoError(t, err)

	paths := h.Paths()
	assert.Contains(t, paths, mcpserver.KnowledgePath)
	assert.Contains(t, paths, "/runbooks/alb-create.md")
	assert.GreaterOrEqual(t, len(paths), 11, "the knowledge document and ten skills")

	for _, p := range paths {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		assert.Equal(t, http.StatusOK, w.Code, "%s is named in the startup log but not served", p)
		assert.NotEmpty(t, w.Body.String())
	}
}

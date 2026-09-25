// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func handler(t *testing.T) *knowledgeHandler {
	t.Helper()
	h, err := newKnowledgeHandler()
	require.NoError(t, err)
	return h
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// TestEveryURLTheCapabilityDocumentPromisesIsServed is the point of embedding
// the documents rather than mounting them: a stripped image cannot silently
// answer 404 for knowledge the capability document says is there.
func TestEveryURLTheCapabilityDocumentPromisesIsServed(t *testing.T) {
	h := handler(t)

	w := get(t, h, knowledgePath)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")

	paths := h.paths()
	assert.Contains(t, paths, knowledgePath)
	assert.GreaterOrEqual(t, len(paths), 11, "the knowledge document and ten skills")

	for _, p := range paths {
		if p == knowledgePath {
			continue
		}
		w := get(t, h, p)
		assert.Equal(t, http.StatusOK, w.Code, "%s is named in the startup log but not served", p)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/markdown")
	}
}

// TestUnknownAndTraversingPathsAreNotFound pins that routing is an exact-match
// lookup rather than a path join against a directory, so there is nothing for
// "..", an absolute path, or an escaped separator to traverse to.
func TestUnknownAndTraversingPathsAreNotFound(t *testing.T) {
	h := handler(t)
	for _, p := range []string{
		"/runbooks/nope.md",
		"/runbooks/../../etc/passwd",
		"/runbooks/",
		"/runbooks/alb-create.md/../alb-create.md",
		"/llms-full.txt/../llms-full.txt",
	} {
		assert.Equal(t, http.StatusNotFound, get(t, h, p).Code, "%s should not resolve", p)
	}
}

func TestDocumentsAreReadOnly(t *testing.T) {
	h := handler(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, knowledgePath, nil))
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		assert.Contains(t, w.Header().Get("Allow"), "GET")
	}
}

// TestSkillURLsUseTheFrameworksPath pins the naming wart deliberately. The
// directory says skills and the URL says runbooks; the path is baked into
// shipped capability documents, so renaming it here would break them.
func TestSkillURLsUseTheFrameworksPath(t *testing.T) {
	for _, p := range handler(t).paths() {
		if p == knowledgePath {
			continue
		}
		assert.True(t, strings.HasPrefix(p, "/runbooks/"),
			"%s must be served under /runbooks/, which the capability documents already name", p)
	}
}

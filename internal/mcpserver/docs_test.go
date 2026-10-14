// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func handler(t *testing.T) *KnowledgeHandler {
	t.Helper()
	h, err := NewKnowledgeHandler(fstest.MapFS{
		"llms-full.txt":        {Data: []byte("knowledge")},
		"skills/a-create.md":   {Data: []byte("# create")},
		"skills/b-diagnose.md": {Data: []byte("# diagnose")},
		"skills/ignored.txt":   {Data: []byte("not markdown")},
	}, "llms-full.txt", "skills")
	require.NoError(t, err)
	return h
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestServesKnowledgeAndEverySkill(t *testing.T) {
	h := handler(t)

	w := get(t, h, KnowledgePath)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")

	assert.Equal(t, []string{KnowledgePath, "/runbooks/a-create.md", "/runbooks/b-diagnose.md"}, h.Paths())
	for _, p := range h.Paths()[1:] {
		w := get(t, h, p)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/markdown")
	}
}

func TestRefusesAnFSWithNoSkills(t *testing.T) {
	_, err := NewKnowledgeHandler(fstest.MapFS{
		"llms-full.txt": {Data: []byte("knowledge")},
		"skills/x.txt":  {Data: []byte("not markdown")},
	}, "llms-full.txt", "skills")
	assert.Error(t, err)
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
		"/runbooks/a-create.md/../a-create.md",
		"/runbooks/ignored.txt",
		"/llms-full.txt/../llms-full.txt",
	} {
		assert.Equal(t, http.StatusNotFound, get(t, h, p).Code, "%s should not resolve", p)
	}
}

func TestDocumentsAreReadOnly(t *testing.T) {
	h := handler(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, KnowledgePath, nil))
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		assert.Contains(t, w.Header().Get("Allow"), "GET")
	}
}

// TestSkillURLsUseTheFrameworksPath pins the naming wart deliberately. The
// directory says skills and the URL says runbooks; the path is baked into
// shipped capability documents, so renaming it here would break them.
func TestSkillURLsUseTheFrameworksPath(t *testing.T) {
	for _, p := range handler(t).Paths() {
		if p == KnowledgePath {
			continue
		}
		assert.True(t, strings.HasPrefix(p, "/runbooks/"),
			"%s must be served under /runbooks/, which the capability documents already name", p)
	}
}

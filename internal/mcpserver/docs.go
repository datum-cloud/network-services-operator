// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	// KnowledgePath serves the knowledge document.
	KnowledgePath = "/llms-full.txt"

	// RunbookPrefix serves the skills. The URL says "runbooks" while the repo
	// directory says "skills": the path is fixed by the capability documents the
	// assistant already ships, so it is not ours to rename.
	RunbookPrefix = "/runbooks/"

	textContentType     = "text/plain; charset=utf-8"
	markdownContentType = "text/markdown; charset=utf-8"
)

type document struct {
	body        []byte
	contentType string
}

// KnowledgeHandler serves a service's knowledge and skills over plain HTTP.
//
// Routing is an exact-match lookup in a table enumerated at startup, never a
// path join against a directory, so "..", an absolute path, or an escaped
// separator has nothing to traverse to.
//
// These documents are public and carry no auth check: the assistant fetches
// them before it holds any project context, and they are static text with no
// tenant data. /mcp is the credential-bearing surface.
type KnowledgeHandler struct {
	docs map[string]document
}

// NewKnowledgeHandler reads every embedded document into memory. Failing here
// is a build problem, not a runtime one, so the server refuses to start rather
// than serve a capability document's URLs as 404s.
func NewKnowledgeHandler(fsys fs.FS, knowledgeFile, skillsDir string) (*KnowledgeHandler, error) {
	docs := make(map[string]document)

	knowledge, err := fs.ReadFile(fsys, knowledgeFile)
	if err != nil {
		return nil, fmt.Errorf("reading embedded knowledge: %w", err)
	}
	docs[KnowledgePath] = document{body: knowledge, contentType: textContentType}

	entries, err := fs.ReadDir(fsys, skillsDir)
	if err != nil {
		return nil, fmt.Errorf("reading embedded skills: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body, err := fs.ReadFile(fsys, path.Join(skillsDir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading embedded skill %s: %w", entry.Name(), err)
		}
		docs[RunbookPrefix+entry.Name()] = document{body: body, contentType: markdownContentType}
	}

	if len(docs) < 2 {
		return nil, fmt.Errorf("no skills embedded from %s", skillsDir)
	}
	return &KnowledgeHandler{docs: docs}, nil
}

func (h *KnowledgeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	doc, ok := h.docs[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", doc.contentType)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(doc.body))
}

// Paths returns the served URL paths, sorted, for the startup log.
func (h *KnowledgeHandler) Paths() []string {
	out := make([]string, 0, len(h.docs))
	for p := range h.docs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentdocs "go.datum.net/network-services-operator/docs/agent"
)

// Patch's own limits, from its capability composition. Exceeding either means
// the document is silently truncated or dropped in a customer's conversation,
// which is not something a reader of this repo would otherwise discover.
const (
	maxKnowledgeBytes = 32 * 1024
	maxSkillBytes     = 64 * 1024
)

func TestKnowledgeFitsTheAssistantsCap(t *testing.T) {
	b, err := agentdocs.FS.ReadFile(agentdocs.KnowledgeFile)
	require.NoError(t, err)
	assert.Less(t, len(b), maxKnowledgeBytes,
		"the knowledge document is truncated at %d bytes when the assistant fetches it", maxKnowledgeBytes)
	assert.NotEmpty(t, b)
}

func skillFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := fs.ReadDir(agentdocs.FS, agentdocs.SkillsDir)
	require.NoError(t, err)

	out := map[string]string{}
	for _, e := range entries {
		b, err := agentdocs.FS.ReadFile(path.Join(agentdocs.SkillsDir, e.Name()))
		require.NoError(t, err)
		out[strings.TrimSuffix(e.Name(), ".md")] = string(b)
	}
	return out
}

func TestSkillsFitTheAssistantsCap(t *testing.T) {
	for name, body := range skillFiles(t) {
		assert.Less(t, len(body), maxSkillBytes, "skill %s is truncated when loaded", name)
		assert.NotEmpty(t, body, "skill %s is empty", name)
	}
}

// TestEverySkillTheCatalogNamesExists is the link between the two halves. A
// cause that points at a skill nobody published sends the assistant to fetch a
// 404, and it degrades silently: the turn carries on with no procedure.
func TestEverySkillTheCatalogNamesExists(t *testing.T) {
	published := skillFiles(t)
	for _, info := range AllReasons() {
		if info.Skill == "" {
			continue
		}
		_, ok := published[info.Skill]
		assert.True(t, ok, "%s on %s points at skill %q, which is not published",
			info.Reason, info.ConditionType, info.Skill)
	}
}

// TestEveryPublishedSkillIsReachable is the other direction. A skill nothing
// points at is one the assistant will rarely load, and is usually a rename that
// only got done on one side.
func TestEveryPublishedSkillIsReachable(t *testing.T) {
	named := map[string]bool{}
	for _, info := range AllReasons() {
		if info.Skill != "" {
			named[info.Skill] = true
		}
	}
	// These two are reached from the knowledge document rather than from a
	// cause: one is the entry point, the others cover states no condition
	// reports.
	for _, s := range []string{SkillNotServing, SkillEdgePropagation, SkillAccessLogTriage, SkillCreate, SkillProtectionTriage} {
		named[s] = true
	}

	for name := range skillFiles(t) {
		assert.True(t, named[name], "skill %q is published but nothing points at it", name)
	}
}

// TestPublishedDocsUseNoInternalVocabulary holds the knowledge document and the
// skills to the same bar as the catalog copy. These are read by an assistant
// and quoted to a customer, so a word that only exists inside the
// implementation reaches them just as directly.
//
// Only the internal list applies: these documents address the assistant, so
// they may say "condition" and "programmed" where copy shown to a customer may
// not.
func TestPublishedDocsUseNoInternalVocabulary(t *testing.T) {
	b, err := agentdocs.FS.ReadFile(agentdocs.KnowledgeFile)
	require.NoError(t, err)
	scanDoc(t, agentdocs.KnowledgeFile, string(b))

	for name, body := range skillFiles(t) {
		scanDoc(t, "skill "+name, body)
	}
}

// scanDoc scans a published document for internal vocabulary.
//
// Backticked spans are stripped first. That is the rule the whole denylist
// turns on: an API name may appear as a quoted identifier — `HTTPProxy`,
// `kind: Gateway` — because the reader needs it verbatim to match what they see
// in a manifest, but never as a bare word in a sentence. "The Gateway is not
// programmed" is banned; a table row mapping the product word to the stored
// kind is not.
func scanDoc(t *testing.T, where, text string) {
	t.Helper()
	stripped := prose(backticked.ReplaceAllString(text, " "),
		"example.com", "app.example.com", "www.example.com", "origin.example.com",
		"datumproxy.net", "storefront",
	)
	for _, term := range internalVocabulary {
		if m := term.pattern.FindString(stripped); m != "" {
			t.Errorf("%s uses %q outside backticks, which a customer never writes or reads: %s",
				where, m, term.why)
		}
	}
}

// backticked matches an inline code span or a fenced block.
var backticked = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")

// TestPublishedDocsOnlyNameToolsThatExist closes the hole behind a real
// incident: the skills told the assistant to call alb_traffic_summary, which
// was designed and then deliberately deferred, and nothing caught that the
// documents still promised it.
//
// The failure is worse than a missing feature. The assistant loads the skill,
// finds no such tool, and has to account for it — so it reports the skill as
// broken and files a capability gap, when the only thing actually wrong is that
// we told it something untrue.
func TestPublishedDocsOnlyNameToolsThatExist(t *testing.T) {
	registered := map[string]bool{}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterTools(s, depsFor(&fakeReader{}))
	for _, tool := range serverTools(t, s) {
		registered[tool.Name] = true
	}
	require.NotEmpty(t, registered)

	named := regexp.MustCompile(`\balb_[a-z_]+\b`)

	check := func(where, body string) {
		for _, m := range named.FindAllString(body, -1) {
			assert.True(t, registered[m],
				"%s tells the assistant to call %q, which this service does not publish; "+
					"either implement it or stop naming it", where, m)
		}
	}

	b, err := agentdocs.FS.ReadFile(agentdocs.KnowledgeFile)
	require.NoError(t, err)
	check(agentdocs.KnowledgeFile, string(b))

	for name, body := range skillFiles(t) {
		check("skill "+name, body)
	}
}

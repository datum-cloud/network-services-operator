// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	docs "go.datum.net/network-services-operator/docs/network-agent"
)

const claudePluginSkillsDir = "../../claude-plugin/skills"

type claudePluginSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string
}

func claudePluginSkills(t *testing.T) map[string]claudePluginSkill {
	t.Helper()
	entries, err := os.ReadDir(claudePluginSkillsDir)
	require.NoError(t, err)

	out := map[string]claudePluginSkill{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(claudePluginSkillsDir, e.Name(), "SKILL.md"))
		require.NoError(t, err, "plugin skill %s has no SKILL.md", e.Name())
		parts := strings.SplitN(string(b), "---\n", 3)
		require.Len(t, parts, 3, "plugin skill %s has no frontmatter", e.Name())
		var sk claudePluginSkill
		require.NoError(t, yaml.Unmarshal([]byte(parts[1]), &sk), "plugin skill %s", e.Name())
		sk.Body = parts[2]
		out[e.Name()] = sk
	}
	return out
}

// TestEveryPublishedSkillIsInTheClaudePlugin keeps the Claude Code plugin in
// step with what network_guide serves. The plugin's skills only point at these
// documents, so a skill published without one is never loaded from Claude
// Code, and one left behind after a rename loads nothing.
func TestEveryPublishedSkillIsInTheClaudePlugin(t *testing.T) {
	plugin := claudePluginSkills(t)

	entries, err := fs.ReadDir(docs.FS, docs.SkillsDir)
	require.NoError(t, err)
	published := map[string]bool{}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".md")
		published[name] = true

		sk, ok := plugin[name]
		if !assert.True(t, ok, "skill %s is published but has no plugin skill in claude-plugin/skills", name) {
			continue
		}
		assert.Equal(t, name, sk.Name, "plugin skill %s names itself differently", name)
		assert.NotEmpty(t, sk.Description, "plugin skill %s has no description", name)
		assert.Contains(t, sk.Body, "`network_guide` with `skill: \""+name+"\"`",
			"plugin skill %s does not load its procedure from network_guide", name)
	}

	for name, sk := range plugin {
		if strings.Contains(sk.Body, "`network_guide`") {
			assert.True(t, published[name], "plugin skill %s loads a procedure network_guide does not serve", name)
		}
	}
}

// SPDX-License-Identifier: AGPL-3.0-only

package networkagent

import (
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	docs "go.datum.net/network-services-operator/docs/network-agent"
)

const (
	maxKnowledgeBytes = 32 * 1024
	maxSkillBytes     = 64 * 1024
)

type bannedTerm struct {
	pattern *regexp.Regexp
	why     string
}

func ban(expr, why string) bannedTerm {
	return bannedTerm{pattern: regexp.MustCompile(`(?i)` + expr), why: why}
}

var internalVocabulary = []bannedTerm{
	ban(`\bnetwork ?contexts?\b`, "the stored kind for a network in one location. Say the network in that location"),
	ban(`\bnetwork ?bindings?\b`, "the stored kind for a workload's use of a network. Say the workload's attachment"),
	ban(`\bsubnet ?claims?\b`, "address request plumbing nobody writes"),
	ban(`\binterface ?claims?\b`, "the stored kind behind an instance's interface request. Say the instance's network interface"),
	ban(`\bipam\b`, "the address management system. Allowed only as the `ipam` field a customer writes"),
	ban(`\bpresence\b`, "the controller's word for a network in a location"),
	ban(`\bhub\b`, "an internal control plane"),
	ban(`\bcells?\b`, "an internal unit of infrastructure"),
	ban(`\bprojection\b`, "how an interface is copied between control planes"),
	ban(`\breconcil`, "controller-loop vocabulary"),
	ban(`\bthe operator\b`, "controller vocabulary"),
	ban(`\bgalactic\b`, "an internal component name"),
	ban(`\bdownstream\b`, "the operator's word for the cell-side cluster"),
	ban(`\bmulticluster\b`, "how the operator is built"),
	ban(`\brealizer\b`, "the data-plane component behind an interface"),
	ban(`\bholder\b`, "the API's word for whatever holds an interface. Say the instance"),
	ban(`\bwebhooks?\b`, "admission machinery; the customer reads that a change was refused"),
}

var customerFacingOnly = []bannedTerm{
	ban(`\bconditions?\b`, "the customer sees a workload that cannot reach anything, not a condition"),
	ban(`\bprogramm(ed|ing)\b`, "the platform's word for configuration reaching the data plane"),
}

var backticked = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")

func scan(t *testing.T, where, text string, lists ...[]bannedTerm) {
	t.Helper()
	text = backticked.ReplaceAllString(text, " ")
	for _, list := range lists {
		for _, term := range list {
			if m := term.pattern.FindString(text); m != "" {
				t.Errorf("%s uses %q, which a customer never writes or reads: %s", where, m, term.why)
			}
		}
	}
}

func TestCatalogCopyUsesNoInternalVocabulary(t *testing.T) {
	for _, info := range AllReasons() {
		where := info.Kind + "/" + info.ConditionType + "/" + info.Reason
		scan(t, where, info.Explanation+" "+info.Remediation, internalVocabulary, customerFacingOnly)
	}
	scan(t, "uncatalogued explanation", uncataloguedExplanation, internalVocabulary, customerFacingOnly)
}

func skillFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := fs.ReadDir(docs.FS, docs.SkillsDir)
	require.NoError(t, err)
	out := map[string]string{}
	for _, e := range entries {
		b, err := docs.FS.ReadFile(path.Join(docs.SkillsDir, e.Name()))
		require.NoError(t, err)
		out[strings.TrimSuffix(e.Name(), ".md")] = string(b)
	}
	return out
}

func knowledge(t *testing.T) string {
	t.Helper()
	b, err := docs.FS.ReadFile(docs.KnowledgeFile)
	require.NoError(t, err)
	return string(b)
}

func TestDocumentsFitTheAssistantsCaps(t *testing.T) {
	k := knowledge(t)
	assert.NotEmpty(t, k)
	assert.Less(t, len(k), maxKnowledgeBytes)
	for name, body := range skillFiles(t) {
		assert.NotEmpty(t, body, name)
		assert.Less(t, len(body), maxSkillBytes, name)
	}
}

func TestPublishedDocsUseNoInternalVocabulary(t *testing.T) {
	scan(t, docs.KnowledgeFile, knowledge(t), internalVocabulary)
	for name, body := range skillFiles(t) {
		scan(t, "skill "+name, body, internalVocabulary)
	}
}

var skills = []string{
	SkillNetworkCreate, SkillServiceCreate, SkillNetworkNotReady, SkillNetworkInLocation,
	SkillInterfaceTriage, SkillServiceNotServing, SkillNetworkDeletionStuck,
}

func TestEverySkillIsPublishedAndEveryPublishedSkillIsKnown(t *testing.T) {
	published := skillFiles(t)
	known := map[string]bool{}
	for _, s := range skills {
		known[s] = true
		assert.Contains(t, published, s, "skill %q is named in code but not published", s)
	}
	for _, info := range AllReasons() {
		if info.Skill != "" {
			assert.True(t, known[info.Skill], "%+v points at unknown skill %q", keyOf(info), info.Skill)
		}
	}
	for name := range published {
		assert.True(t, known[name], "skill %q is published but nothing in code names it", name)
	}
}

func TestTheKnowledgeDocumentNamesEverySkill(t *testing.T) {
	k := knowledge(t)
	for _, s := range skills {
		assert.Contains(t, k, "`"+s+"`", "the knowledge document never points at skill %q", s)
	}
}

func TestPublishedDocsOnlyNameToolsThatExist(t *testing.T) {
	registered := map[string]bool{}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterTools(s, depsFor(&fakeReader{}))
	for _, tool := range serverTools(t, s) {
		registered[tool.Name] = true
	}
	require.NotEmpty(t, registered)

	named := regexp.MustCompile(`\bnetwork_[a-z_]+\b`)
	check := func(where, body string) {
		for _, m := range named.FindAllString(body, -1) {
			assert.True(t, registered[m], "%s names %q, which this service does not publish", where, m)
		}
	}
	check(docs.KnowledgeFile, knowledge(t))
	for name, body := range skillFiles(t) {
		check("skill "+name, body)
	}
	for name := range registered {
		assert.Contains(t, knowledge(t), "`"+name+"`", "the knowledge document never mentions tool %q", name)
	}
}

type agentConfiguration struct {
	Spec struct {
		Knowledge struct {
			Sources []struct {
				URL string `json:"url"`
			} `json:"sources"`
		} `json:"knowledge"`
		Tools struct {
			MCPServers []struct {
				Endpoint     string `json:"endpoint"`
				ToolSelector struct {
					Include []string `json:"include"`
				} `json:"toolSelector"`
				Mutating []string `json:"mutating"`
			} `json:"mcpServers"`
		} `json:"tools"`
		Skills []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"skills"`
	} `json:"spec"`
}

func TestTheRegistrationMatchesWhatIsServed(t *testing.T) {
	b, err := os.ReadFile(repoPath("config", "components", "assistant-capability", "network-service-agent-configuration.yaml"))
	require.NoError(t, err)
	var cfg agentConfiguration
	require.NoError(t, yaml.Unmarshal(b, &cfg))

	require.Len(t, cfg.Spec.Tools.MCPServers, 1)
	server := cfg.Spec.Tools.MCPServers[0]
	base := strings.TrimSuffix(server.Endpoint, "/mcp")
	assert.Empty(t, server.Mutating)

	tools := serverTools(t, newServer())
	registered := make([]string, 0, len(tools))
	for _, tool := range tools {
		registered = append(registered, tool.Name)
	}
	assert.ElementsMatch(t, registered, server.ToolSelector.Include,
		"the approved tool list and the registered tools have drifted")

	require.Len(t, cfg.Spec.Knowledge.Sources, 1)
	assert.Equal(t, base+"/llms-full.txt", cfg.Spec.Knowledge.Sources[0].URL)

	registeredSkills := make([]string, 0, len(cfg.Spec.Skills))
	for _, s := range cfg.Spec.Skills {
		registeredSkills = append(registeredSkills, s.Name)
		assert.Equal(t, base+"/runbooks/"+s.Name+".md", s.Source)
	}
	assert.ElementsMatch(t, skills, registeredSkills, "the registered skills and the published ones have drifted")
}

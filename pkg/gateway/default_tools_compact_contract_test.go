package gateway

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultToolsAssertCompactContract(t *testing.T) {
	t.Helper()
	// All phrases are derived from SPEC.md compact output, not formatter output.
	// 79/80/81 are max-1/max/max+1 at the 80-character limit.
	cases := []struct{ name, input, want string }{
		{"empty", "", ""},
		{"one_character", "A", "A"},
		{"collapse_whitespace", "  Read\t a \n file.  Private second sentence.", "Read a file."},
		{"exclamation", "Run now! Private second sentence.", "Run now!"},
		{"question", "What now? Private second sentence.", "What now?"},
		{"no_sentence_boundary", "Read v1.2 data!now?yes", "Read v1.2 data!now?yes"},
		{"unicode_whitespace", "Read this.\nPrivate second sentence.", "Read this."},
		{"79_characters", strings.Repeat("x", 78) + ".", strings.Repeat("x", 78) + "."},
		{"80_characters", strings.Repeat("x", 79) + ".", strings.Repeat("x", 79) + "."},
		{"81_characters_word_boundary", strings.Repeat("a", 77) + " end", strings.Repeat("a", 77) + "…"},
		{"boundary_at_79", strings.Repeat("a", 79) + " tail", strings.Repeat("a", 79) + "…"},
		{"multiple_words", strings.Repeat("word ", 16) + "end.", strings.TrimSpace(strings.Repeat("word ", 16)) + "…"},
		{"characters_not_bytes", strings.Repeat("é", 79) + " fin", strings.Repeat("é", 79) + "…"},
		// With no word boundary before the cap, the only safe boundary is the start.
		{"single_oversized_word", strings.Repeat("x", 81), "…"},
	}
	for _, row := range cases {
		t.Run("short_description/"+row.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			metadata := []tools.Tool{defaultToolsMetadata{name: "read_file", description: row.input}}
			raw := defaultToolsInventory(t, t.TempDir(), cfg, metadata, map[string]any{"new_agent_type": "Main"})
			want := "preview: Main | kind: main\nread_file | allow | " + row.want
			assert.Equal(t, want, strings.TrimSuffix(raw, "\n"), "SPEC.md first-sentence, whitespace and 80-character rules")
		})
	}
	t.Run("assigned_mcp_only_including_deny", defaultToolsAssertCompactMCP)
	t.Run("system_headers", defaultToolsAssertCompactSystemHeaders)
}

func defaultToolsAssertCompactMCP(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	read := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "read_doc", Description: "Read a document. Never include the rest."})
	write := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "write_doc", Description: "Write a document. Never include the rest."})
	unassigned := tools.NewMCPTool(nil, "docs", &mcp.Tool{Name: "unassigned_doc", Description: "Must not be listed."})
	otherServer := tools.NewMCPTool(nil, "other", &mcp.Tool{Name: "read_doc", Description: "Must not be listed."})
	agent := config.AgentConfig{ID: "compact-subject", Name: "Compact Subject", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
	agent.Tools.MCP.Servers = []config.AgentMCPServerBinding{{ID: "docs", Tools: []string{"read_doc", "write_doc"}, ToolsSpecified: true}}
	agent.Tools.Builtin.Policies[write.Name()] = config.ToolPolicyDeny
	cfg.Sandbox.ToolPolicies["mcp_*"] = "allow"
	cfg.Sandbox.ToolPolicies[read.Name()] = "ask"
	store := agentstore.New(home)
	_, err := store.CreateState(agent.ID, &agent, "Help the user.")
	require.NoError(t, err)
	state, err := store.ReadState(agent.ID)
	require.NoError(t, err)
	metadata := []tools.Tool{otherServer, write, defaultToolsMetadata{name: "read_file", description: "Read a file."}, unassigned, read}
	raw := defaultToolsInventory(t, home, cfg, metadata, map[string]any{"id": agent.ID})
	header := fmt.Sprintf("agent: %s | name: %s | kind: main | revision: %s", agent.ID, agent.Name, state.Revision)
	// SPEC.md: only assigned live MCP tools join the builtin catalog. Deny is
	// a visible policy, not grounds for omitting an assigned tool.
	included := []tools.Tool{read, write, metadata[2]}
	policies := defaultToolsParseCompact(t, raw, header, included)
	assert.Equal(t, map[string]string{read.Name(): "ask", write.Name(): "deny", "read_file": "allow"}, policies)
	rows := []string{read.Name() + " | ask | Read a document.", write.Name() + " | deny | Write a document.", "read_file | allow | Read a file."}
	sort.Strings(rows)
	assert.Equal(t, header+"\n"+strings.Join(rows, "\n"), strings.TrimSuffix(raw, "\n"), "SPEC.md exact sorted rows; no parameters or verbose metadata")
}

func defaultToolsAssertCompactSystemHeaders(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	coreagent.SeedConfig(cfg)
	catalog, _ := buildCentralBuiltinRegistry(nil)
	for _, agent := range cfg.Agents.List {
		if agent.ID == "omnipus" || agent.ID == "judge" || agent.ID == "plansupervisor" {
			t.Run(agent.ID, func(t *testing.T) {
				store := agentstore.New(home)
				_, err := store.CreateState(agent.ID, &agent, "System instructions.")
				require.NoError(t, err)
				state, err := store.ReadState(agent.ID)
				require.NoError(t, err)
				header := fmt.Sprintf("agent: %s | name: %s | kind: system | revision: %s", agent.ID, agent.Name, state.Revision)
				raw := defaultToolsInventory(t, home, cfg, catalog.All(), map[string]any{"id": agent.ID})
				policies := defaultToolsParseCompact(t, raw, header, catalog.All())
				resolver, _ := defaultToolsPolicies(cfg, &agent)
				for name, policy := range policies {
					assert.Equal(t, tools.ResolveEffectivePolicy(resolver, name), policy, "system inventory/%s policy", name)
				}
			})
		}
	}
}

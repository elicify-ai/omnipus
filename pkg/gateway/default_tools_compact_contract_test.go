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
	// SPEC.md Q21=D: 39/40/41 are max-1/max/max+1 at the 40-character limit.
	cases := []struct{ name, input, want string }{
		{"empty", "", ""},
		{"one_character", "A", "A"},
		{"collapse_whitespace", "  Read\t a \n file.  Private second sentence.", "Read a file."},
		{"exclamation", "Run now! Private second sentence.", "Run now!"},
		{"question", "What now? Private second sentence.", "What now?"},
		{"no_sentence_boundary", "Read v1.2 data!now?yes", "Read v1.2 data!now?yes"},
		{"unicode_whitespace", "Read this.\nPrivate second sentence.", "Read this."},
		{"39_characters", strings.Repeat("x", 38) + ".", strings.Repeat("x", 38) + "."},
		{"40_characters", strings.Repeat("x", 39) + ".", strings.Repeat("x", 39) + "."},
		{"41_characters_word_boundary", strings.Repeat("a", 37) + " end", strings.Repeat("a", 37) + "…"},
		{"boundary_at_39", strings.Repeat("a", 39) + " tail", strings.Repeat("a", 39) + "…"},
		{"multiple_words", strings.Repeat("word ", 8) + "end.", strings.TrimSpace(strings.Repeat("word ", 8)) + "…"},
		{"characters_not_bytes", strings.Repeat("é", 39) + " fin", strings.Repeat("é", 39) + "…"},
		// With no word boundary before the cap, the only safe boundary is the start.
		{"single_oversized_word", strings.Repeat("x", 41), "…"},
	}
	for _, permission := range []struct{ name, policy string }{{"read_file", "allow"}, {"request_mount", "ask"}} {
		for _, row := range cases {
			t.Run(permission.policy+"/short_description/"+row.name, func(t *testing.T) {
				cfg := config.DefaultConfig()
				metadata := []tools.Tool{defaultToolsMetadata{name: permission.name, description: row.input}}
				raw := defaultToolsInventory(t, t.TempDir(), cfg, metadata, map[string]any{"new_agent_type": "Main"})
				identity, entries := defaultToolsDecode(t, raw)
				assert.Equal(t, map[string]any{"preview_of": "Main"}, identity)
				require.Len(t, entries, 1)
				require.NotNil(t, entries[0].Description)
				// r17 7.12.1: both Allow and Ask use the exact first-sentence/40-character contract.
				// The preview shows the stored value the new agent would carry for the
				// exact key, so override is either null or the same value; the
				// description rules are what this row pins.
				got := entries[0]
				if got.Override != nil {
					assert.Equal(t, permission.policy, *got.Override, "a preview override is the value the new agent would store")
				}
				got.Override = nil
				assert.Equal(t, defaultToolsEntry{Tool: permission.name, Effective: permission.policy, Description: &row.want}, got, "first-sentence, whitespace and 40-character rules")
			})
		}
	}
	for _, description := range []string{"", "Never expose this denied description.", strings.Repeat("private ", 20)} {
		t.Run("deny_has_no_description/"+fmt.Sprint(len(description)), func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Sandbox.ToolPolicies["read_file"] = "deny"
			metadata := []tools.Tool{defaultToolsMetadata{name: "read_file", description: description}}
			raw := defaultToolsInventory(t, t.TempDir(), cfg, metadata, map[string]any{"new_agent_type": "Main"})
			// r17 7.12.1: a deny entry carries tool and effective, no description key.
			// A preview's override is the value the new agent would store, so it is
			// not part of this row.
			identity, entries := defaultToolsDecode(t, raw)
			assert.Equal(t, map[string]any{"preview_of": "Main"}, identity)
			require.Len(t, entries, 1)
			entries[0].Override = nil
			assert.Equal(t, []defaultToolsEntry{{Tool: "read_file", Effective: "deny"}}, entries)
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
	identity := map[string]any{"id": agent.ID, "revision": state.Revision}
	// SPEC.md: only assigned live MCP tools join the builtin catalog. Deny is
	// a visible policy, not grounds for omitting an assigned tool.
	included := []tools.Tool{read, write, metadata[2]}
	policies := defaultToolsParseInventory(t, raw, identity, included)
	assert.Equal(t, map[string]string{read.Name(): "ask", write.Name(): "deny", "read_file": "allow"}, policies)
	_, entries := defaultToolsDecode(t, raw)
	ask := "Read a document."
	allow := "Read a file."
	assert.Equal(t, []defaultToolsEntry{
		{Tool: read.Name(), Effective: "ask", Description: &ask},
		{Tool: write.Name(), Effective: "deny", Override: ptrString("deny")},
		// NewCustomAgentToolsCfg stores an explicit allow for read_file, so the
		// exact-key override is "allow" (r17 7.12.1: override = stored value).
		{Tool: "read_file", Effective: "allow", Override: ptrString("allow"), Description: &allow},
	}, sortedEntries(entries), "exact sorted entries; no parameters or verbose metadata")
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
				identity := map[string]any{"id": agent.ID, "revision": state.Revision}
				raw := defaultToolsInventory(t, home, cfg, catalog.All(), map[string]any{"id": agent.ID})
				policies := defaultToolsParseInventory(t, raw, identity, catalog.All())
				resolver, _ := defaultToolsPolicies(cfg, &agent)
				for name, policy := range policies {
					assert.Equal(t, tools.ResolveEffectivePolicy(resolver, name), policy, "system inventory/%s policy", name)
				}
			})
		}
	}
}

func ptrString(v string) *string { return &v }

// sortedEntries returns the entries ordered by tool name (the reader already
// sorts; this keeps the assertion independent of that for the comparison).
func sortedEntries(in []defaultToolsEntry) []defaultToolsEntry {
	out := append([]defaultToolsEntry(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

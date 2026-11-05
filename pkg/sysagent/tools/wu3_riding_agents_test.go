package systools_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1075 / Agent.yaml::name: maxLength is 100 characters, not bytes.
func TestWU3AgentNamesEnforceContractBoundaries(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		for _, row := range []struct {
			label, name string
			accepted    bool
		}{
			{"empty", "", false}, {"min", "a", true}, {"min_plus_one", "ab", true},
			{"max_minus_one", strings.Repeat("a", 99), true},
			{"max", strings.Repeat("a", 100), true},
			{"max_plus_one", strings.Repeat("a", 101), false},
			{"unicode_max", strings.Repeat("界", 100), true},
			{"unicode_max_plus_one", strings.Repeat("界", 101), false},
		} {
			t.Run(operation+"_"+row.label, func(t *testing.T) {
				d, cfg := wu3Deps(t)
				var tool tools.Tool = systools.NewAgentCreateTool(d)
				args := wu3CreateArgs(row.name)
				id := ""
				var before map[string]any
				if operation == "update" {
					id = wu3CreateAgent(t, d, cfg, "Name boundary fixture")
					before = wu3AgentRead(t, d, id)
					args = map[string]any{"id": id, "revision": before["revision"], "name": row.name}
					tool = systools.NewAgentUpdateTool(d)
				}
				result := tool.Execute(context.Background(), args)
				if row.accepted {
					body := wu3Success(t, result)
					if operation == "create" {
						id = wu3String(t, body["id"])
					}
					assert.Equal(t, row.name, wu3AgentRead(t, d, id)["name"], "accepted name must round-trip exactly")
				} else {
					body := wu3Error(t, result, "INVALID_INPUT")
					if row.name != "" {
						assert.Contains(t, body["message"], "100", "length refusal must state the contract limit")
					}
					if operation == "update" {
						assert.Equal(t, before, wu3AgentRead(t, d, id), "refused rename must change no persisted field or revision")
					} else {
						entries, err := os.ReadDir(filepath.Join(d.Home, "entities", "agents"))
						if !errors.Is(err, os.ErrNotExist) {
							require.NoError(t, err)
							assert.Empty(t, entries, "refused creation must save no entity")
						}
					}
				}
			})
		}
	}
}

// #1064 A1: retirement guards the actual public catalog and creation grants.
func TestWU3PhantomMetadataWriterIsNotAdvertised(t *testing.T) {
	d, cfg := wu3Deps(t)
	coreagent.SeedConfig(cfg)
	for _, tool := range systools.AllTools(d) {
		assert.NotEqual(t, "write_agent_metadata", tool.Name())
	}
	_, exists := cfg.Sandbox.ToolPolicies["write_agent_metadata"]
	assert.False(t, exists, "retired phantom must not be a shipped ceiling entry")
	for _, ac := range cfg.Agents.List {
		if ac.Tools != nil {
			_, exists = ac.Tools.Builtin.Policies["write_agent_metadata"]
			assert.False(t, exists, "phantom grant on %s", ac.ID)
		}
	}
	// r17 7.12.1: the real catalog is read through get_agent tool_permissions.
	defaultToolsWireCatalog(t, d, append(systools.AllTools(d), tools.GeneralBuiltinMetadata()...))
	result := systools.NewAgentGetTool(d).Execute(context.Background(), map[string]any{"new_agent_type": "Main", "fields": []any{"tool_permissions"}})
	require.False(t, result.IsError, result.ForLLM)
	identity, entries := defaultToolsEntries(t, result.ForLLM)
	assert.Equal(t, map[string]any{"preview_of": "Main"}, identity)
	preview := map[string]string{}
	for _, entry := range entries {
		preview[entry["tool"].(string)], _ = entry["effective"].(string)
	}
	assert.NotContains(t, preview, "write_agent_metadata")
	assert.NotContains(t, preview, "read_agent_metadata", "r17 7.12: the retired reader is not in the catalog")
	assert.Contains(t, preview, "get_agent", "nonempty catalog instrument")
}

// #1064 + chief ruling Q2 (HANDOFFS, 2026-10-15T02:25Z): update_agent
// writes the active metadata, never a phantom. HEARTBEAT.md has no runtime
// reader; heartbeat settings belong to workspace members, so this input refuses.
func TestWU3UpdateAgentWritesEveryMetadataFile(t *testing.T) {
	d, cfg := wu3Deps(t)
	id := wu3CreateAgent(t, d, cfg, "Metadata fixture")
	before := wu3AgentRead(t, d, id)
	want := map[string]string{"soul": "WU3 new soul instructions", "agent_md": "# WU3 agent metadata\n\nExplicit operator instructions.\n"}
	args := map[string]any{"id": id, "revision": before["revision"]}
	for field, content := range want {
		args[field] = content
	}
	result := systools.NewAgentUpdateTool(d).Execute(context.Background(), args)
	body := wu3Success(t, result)
	assert.NotEqual(t, before["revision"], body["revision"], "metadata change advances the revision")
	for field := range want {
		read := wu3Success(t, systools.NewAgentGetTool(d).Execute(context.Background(), map[string]any{"id": id, "fields": []any{field}}))
		assert.Equal(t, want[field], read[field], "get_agent read of %s", field)
	}
	wu3Error(t, systools.NewAgentUpdateTool(d).Execute(context.Background(), args), "CONFLICT")

	// Use the CURRENT revision, so an unrelated stale-write refusal cannot
	// satisfy the heartbeat unknown-field oracle. The whole state must stay put.
	beforeHeartbeat := wu3AgentRead(t, d, id)
	heartbeatPath := filepath.Join(d.Home, "agents", id, "HEARTBEAT.md")
	heartbeatBefore, heartbeatBeforeErr := os.ReadFile(heartbeatPath)
	require.True(t, heartbeatBeforeErr == nil || errors.Is(heartbeatBeforeErr, os.ErrNotExist), "heartbeat fixture must be readable or absent")
	refusal := systools.NewAgentUpdateTool(d).Execute(context.Background(), map[string]any{
		"id": id, "revision": beforeHeartbeat["revision"], "heartbeat": "Must not be saved.",
	})
	errBody := wu3Error(t, refusal, "INVALID_INPUT")
	assert.Contains(t, errBody["message"], `unknown field "heartbeat"`, "Q2: refuse the inert field explicitly")
	assert.Equal(t, beforeHeartbeat, wu3AgentRead(t, d, id), "heartbeat refusal must not change state or revision")
	heartbeatAfter, heartbeatAfterErr := os.ReadFile(heartbeatPath)
	if heartbeatBeforeErr == nil {
		require.NoError(t, heartbeatAfterErr)
		assert.Equal(t, heartbeatBefore, heartbeatAfter, "heartbeat refusal must not rewrite HEARTBEAT.md")
	} else {
		assert.ErrorIs(t, heartbeatAfterErr, os.ErrNotExist, "heartbeat refusal must not create HEARTBEAT.md")
	}
	for field := range want {
		read := wu3Success(t, systools.NewAgentGetTool(d).Execute(context.Background(), map[string]any{"id": id, "fields": []any{field}}))
		assert.Equal(t, want[field], read[field], "heartbeat refusal must preserve metadata %s", field)
	}
}

// #1032 F-3: a missing agent is AGENT_NOT_FOUND, and an existing agent's
// absent instruction file is not a missing agent. r17 7.12.1 removed the
// HEARTBEAT.md read; a field the full object lacks is named in absent_fields.
func TestWU3MetadataReadDistinguishesMissingAgentFromMissingFile(t *testing.T) {
	d, cfg := wu3Deps(t)
	id := wu3CreateAgent(t, d, cfg, "Real metadata owner")
	tool := systools.NewAgentGetTool(d)
	known := wu3Success(t, tool.Execute(context.Background(), map[string]any{"id": id, "fields": []any{"agent_md"}}))
	assert.Equal(t, id, known["id"], "an existing agent answers")
	unknown := wu3Error(t, tool.Execute(context.Background(), map[string]any{"id": "missing-agent", "fields": []any{"agent_md"}}), "AGENT_NOT_FOUND")
	assert.Contains(t, unknown["message"], "missing-agent")
}

// #1032 F-2: upper-case input must never alias a lower-case identity merely
// because the host volume is case-insensitive. No new lower-case-only ID rule.
func TestWU3AgentReadDoesNotAliasIdentityByFilesystemCase(t *testing.T) {
	d, cfg := wu3Deps(t)
	id := wu3CaseAgent(t, d, cfg)
	upper := strings.ToUpper(id)
	wu3Error(t, systools.NewAgentGetTool(d).Execute(context.Background(), map[string]any{"id": upper, "fields": []any{"soul"}}), "AGENT_NOT_FOUND")
}

func TestWU3AgentReadSurfacesRejectUnsafeIDsConsistently(t *testing.T) {
	for _, id := range []string{"../../etc/passwd", "worker/../other", "worker\\other", "nul\x00agent"} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			d, _ := wu3Deps(t)
			wu3Error(t, systools.NewAgentGetTool(d).Execute(context.Background(), map[string]any{"id": id, "fields": []any{"soul"}}), "INVALID_INPUT")
		})
	}
}

// #1032 F-1/F-4, binding shape ruling Q4: exact display-name resolution, no fuzzy.
func TestWU3GetAgentResolvesExactNameWithoutCrossAgentResponse(t *testing.T) {
	d, cfg := wu3Deps(t)
	id := wu3CreateAgent(t, d, cfg, "General Purpose fixture")
	wu3CreateAgent(t, d, cfg, "Different agent")
	tool := systools.NewAgentGetTool(d)
	// Exact-name lookup still equals id lookup.
	byID, _ := defaultToolsExistingBody(t, d, id, map[string]any{"id": id})
	for attempt := 0; attempt < 6; attempt++ {
		byName, _ := defaultToolsExistingBody(t, d, id, map[string]any{"name": "General Purpose fixture"})
		assert.Equal(t, byID, byName, "exact name lookup must be identical to id lookup on every call")
	}
	wu3Error(t, tool.Execute(context.Background(), map[string]any{"name": "General", "fields": []any{"name"}}), "AGENT_NOT_FOUND")
	wu3Error(t, tool.Execute(context.Background(), map[string]any{"id": "General Purpose fixture", "fields": []any{"name"}}), "AGENT_NOT_FOUND")
}

func TestWU3GetAgentRejectsAmbiguousNameAndMixedSelectors(t *testing.T) {
	d, cfg := wu3Deps(t)
	first := wu3CreateAgent(t, d, cfg, "First agent")
	second := wu3CreateAgent(t, d, cfg, "Second agent")
	read := wu3AgentRead(t, d, second)
	wu3Success(t, systools.NewAgentUpdateTool(d).Execute(context.Background(), map[string]any{"id": second, "revision": read["revision"], "name": "First agent"}))
	tool := systools.NewAgentGetTool(d)
	wu3Error(t, tool.Execute(context.Background(), map[string]any{"name": "First agent", "fields": []any{"name"}}), "AMBIGUOUS")
	// r17 7.12.1: two selectors are refused; no selector at all reads the caller.
	for _, args := range []map[string]any{
		{"id": first, "name": "First agent"}, {"name": "First agent", "new_agent_type": "Main"}, {"id": first, "new_agent_type": "Main"},
	} {
		args["fields"] = []any{"name"}
		wu3Error(t, tool.Execute(context.Background(), args), "INVALID_INPUT")
	}
	wu3Error(t, tool.Execute(context.Background(), map[string]any{"fields": []any{"name"}}), "INVALID_INPUT") // no caller in context
}

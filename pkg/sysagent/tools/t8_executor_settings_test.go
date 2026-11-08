package systools_test

// Oracle: tool-parity r17 §7.6, §7.11 and scenarios 6.6, 8.1–8.7,
// 8.10–8.13, 9.15, 9.25, 12.1, 19.8–19.9. These exercise real tools and
// protected files. Approval/real-loop sink coverage is a separate obligation.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8ExternalFixture(t *testing.T) (*systools.Deps, *agentstore.Store) {
	t.Helper()
	d, cfg := newTestDeps(t)
	a := config.AgentConfig{ID: "r1", Name: "R1", Type: config.AgentTypeWorker, Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex", EnvOverrides: map[string]string{"A": "1", "B": "2", "C": "3"}}}}
	s := agentstore.New(d.Home)
	_, err := s.CreateState(a.ID, &a, "")
	require.NoError(t, err)
	cfg.Agents.List = append(cfg.Agents.List, a)
	defaultToolsWireCatalog(t, d, append(systools.AllTools(d), tools.GeneralBuiltinMetadata()...))
	return d, s
}
func t8ChangeMessage(t *testing.T, r *tools.ToolResult) string {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.ForLLM), &out))
	if e, ok := out["error"].(map[string]any); ok {
		if msg, ok := e["message"].(string); ok {
			return msg
		}
	}
	if msg, ok := out["message"].(string); ok {
		return msg
	}
	return r.ForLLM
}

// The spec defines the typed block but not its carrier key. Find that block
// structurally rather than inventing a wire-format envelope in the test.
func t8ResultObjectsNamed(value any, name string) []map[string]any {
	var found []map[string]any
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == name {
				if object, ok := child.(map[string]any); ok {
					found = append(found, object)
				}
			}
			found = append(found, t8ResultObjectsNamed(child, name)...)
		}
	case []any:
		for _, child := range v {
			found = append(found, t8ResultObjectsNamed(child, name)...)
		}
	}
	return found
}

func TestEnvOverrides_MergeRemoveMask_Tool(t *testing.T) {
	for _, tc := range []struct {
		id        string
		patch     map[string]any
		want      map[string]string
		resultKey string
		names     []string
	}{
		{"8.1", map[string]any{"env_overrides": map[string]any{"D": "4"}}, map[string]string{"A": "1", "B": "2", "C": "3", "D": "4"}, "added", []string{"D"}},
		{"8.2", map[string]any{"env_overrides": map[string]any{"A": "9"}}, map[string]string{"A": "9", "B": "2", "C": "3"}, "replaced", []string{"A"}},
		{"8.3", map[string]any{"env_overrides_remove": []any{"B"}}, map[string]string{"A": "1", "C": "3"}, "removed", []string{"B"}},
		{"8.4", map[string]any{"env_overrides_remove": []any{"Z"}}, map[string]string{"A": "1", "B": "2", "C": "3"}, "absent_removed", []string{"Z"}},
		{"8.5", map[string]any{"env_overrides": map[string]any{}}, map[string]string{"A": "1", "B": "2", "C": "3"}, "", nil},
	} {
		t.Run(tc.id, func(t *testing.T) {
			d, s := t8ExternalFixture(t)
			state, err := s.ReadState("r1")
			require.NoError(t, err)
			args := map[string]any{"id": "r1", "revision": state.Revision}
			for k, v := range tc.patch {
				args[k] = v
			}
			r := systools.NewAgentUpdateTool(d).Execute(tools.WithAgentID(context.Background(), "omnipus"), args)
			require.False(t, r.IsError, "%s env change: %s", tc.id, r.ForLLM)
			got, err := s.ReadState("r1")
			require.NoError(t, err)
			require.NotNil(t, got.Agent.Subagents.Executor)
			assert.Equal(t, tc.want, got.Agent.Subagents.Executor.EnvOverrides, "merged result, not replacement")
			var out map[string]any
			require.NoError(t, json.Unmarshal([]byte(r.ForLLM), &out))
			blocks := t8ResultObjectsNamed(out, "env")
			require.Len(t, blocks, 1, "exactly one typed executor env outcome, wherever the carrier places the block")
			want := map[string]any{"added": []any{}, "replaced": []any{}, "removed": []any{}, "absent_removed": []any{}}
			if tc.resultKey != "" {
				names := make([]any, len(tc.names))
				for i, name := range tc.names {
					names[i] = name
				}
				want[tc.resultKey] = names
			}
			assert.Equal(t, want, blocks[0], "all four outcome lists are exact; no environment value")
		})
	}
}
func TestEnvOverrides_ExactRefusals_NoWrite(t *testing.T) {
	for _, tc := range []struct {
		id   string
		args map[string]any
		want string
	}{
		{"8.6", map[string]any{"env_overrides": map[string]any{"A": "1"}, "env_overrides_remove": []any{"A"}}, `env_overrides and env_overrides_remove both name "A".`},
		{"8.10", map[string]any{"env_overrides": map[string]any{"OMNIPUS_MASTER_KEY": "x"}}, `env_overrides key "OMNIPUS_MASTER_KEY" is reserved: OMNIPUS_* is gateway-internal`},
		{"8.11", map[string]any{"env_overrides": map[string]any{"A": "••••••"}}, `env_overrides value for "A" is the masked placeholder; send the real value or leave the name out.`},
	} {
		t.Run(tc.id, func(t *testing.T) {
			d, s := t8ExternalFixture(t)
			state, err := s.ReadState("r1")
			require.NoError(t, err)
			path := filepath.Join(d.Home, "entities", "agents", "r1.json")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			tc.args["id"] = "r1"
			tc.args["revision"] = state.Revision
			r := systools.NewAgentUpdateTool(d).Execute(context.Background(), tc.args)
			require.True(t, r.IsError)
			assert.Equal(t, tc.want, t8ChangeMessage(t, r))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}
func TestGetAgent_ExternalMaskAndNoSoul(t *testing.T) {
	d, s := t8ExternalFixture(t)
	state, err := s.ReadState("r1")
	require.NoError(t, err)
	_, err = s.MutateState("r1", state.Revision, func(a *config.AgentConfig) error {
		a.Subagents.Executor.EnvOverrides = map[string]string{"API_TOKEN": "s3cr3t-value-123456"}
		return nil
	}, nil)
	require.NoError(t, err)
	r := systools.NewAgentGetTool(d).Execute(context.Background(), map[string]any{"id": "r1", "fields": []any{"all"}})
	require.False(t, r.IsError, "6.6/6.8/19.2: %s", r.ForLLM)
	assert.NotContains(t, r.ForLLM, "s3cr3t-value-123456")
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.ForLLM), &out))
	// Scenario 19.2: every one of the independently enumerated 29 names is
	// present OR explicitly absent, never silently lost and never both.
	valid := []string{"name", "description", "type", "locked", "figure", "role", "color", "voice", "model", "provider", "fallback_models", "model_chain", "model_params", "max_tool_iterations", "context_window_override", "memory_enabled", "soul", "agent_md", "skills", "stale_skill_grants", "tool_permissions", "stored_overrides", "connectors", "executor", "runnable", "program_warning", "activation_status", "editable_fields", "has_main_chat"}
	var partition []string
	for key := range out {
		if key != "id" && key != "revision" && key != "absent_fields" && key != "truncated_fields" {
			partition = append(partition, key)
		}
	}
	if missing, ok := out["absent_fields"].([]any); ok {
		for _, item := range missing {
			key, ok := item.(string)
			require.True(t, ok)
			assert.NotContains(t, out, key)
			partition = append(partition, key)
		}
	}
	assert.ElementsMatch(t, valid, partition, "all 29 fields accounted for, exactly once, no extra properties")
	assert.Equal(t, "r1", out["id"])
	executor, ok := out["executor"].(map[string]any)
	require.True(t, ok, "selected executor must be returned")
	assert.Equal(t, map[string]any{"API_TOKEN": "••••••"}, executor["env_overrides"])
	assert.NotContains(t, out, "soul")
	absent, ok := out["absent_fields"].([]any)
	require.True(t, ok)
	assert.Contains(t, absent, "soul")
	assert.Equal(t, false, out["runnable"], "unset Settings path cannot fall back to PATH or old agent fields")
	for _, key := range []string{"connectors", "stored_overrides", "has_main_chat", "program_warning"} {
		assert.Contains(t, out, key)
	}
}
func TestAgentSurfaces_CliPathAndSoulRefused_Registry(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
		field string
	}{
		{"9.15 cli_path", map[string]any{"cli_path": "/opt/t8/cli"}, "cli_path"},
		{"12.1 external soul", map[string]any{"soul": "SECRET-PERSONA"}, "soul"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newTestDeps(t)
			r := tools.NewToolRegistry()
			r.Register(systools.NewAgentCreateTool(d))
			args := map[string]any{"name": "external probe", "description": "T8 probe", "model": "test-model", "agent_type": "subagent_3p", "cli": "codex"}
			for k, v := range tc.extra {
				args[k] = v
			}
			got := r.Execute(context.Background(), "create_agent", args)
			require.True(t, got.IsError, "refuse %s: %s", tc.field, got.ForLLM)
			assert.Contains(t, t8ChangeMessage(t, got), tc.field)
			if tc.field == "cli_path" {
				assert.Contains(t, systools.NewAgentCreateTool(d).Description(), "Settings")
			}
		})
	}
}
func TestSetAgentPermissions_SelfRefusal_NoWrite(t *testing.T) {
	for _, id := range []string{"omnipus", "jim"} {
		t.Run(id, func(t *testing.T) {
			d, _ := newTestDeps(t)
			s := agentstore.New(d.Home)
			a := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyAsk}}}}
			state, err := s.CreateState(id, &a, "soul")
			require.NoError(t, err)
			path := filepath.Join(d.Home, "entities", "agents", id+".json")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			defaultToolsWireCatalog(t, d, append(systools.AllTools(d), tools.GeneralBuiltinMetadata()...))
			r := systools.NewAgentPermissionsTool(d).Execute(tools.WithAgentID(context.Background(), id), map[string]any{"id": id, "revision": state.Revision, "tool_policy_changes": map[string]any{"set": map[string]any{"bash": "deny"}}})
			require.True(t, r.IsError, "19.8/19.9 self change refused before mutation: %s", r.ForLLM)
			assert.Contains(t, r.ForLLM, "SELF_PERMISSION_CHANGE")
			assert.Equal(t, "An agent cannot change its own permissions. Ask a person or another agent that holds set_agent_permissions.", t8ChangeMessage(t, r))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

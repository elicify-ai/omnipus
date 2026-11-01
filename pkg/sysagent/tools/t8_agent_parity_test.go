package systools_test

// RED oracle: tool-parity-spec-r17 §7.12–7.13, D-8 and scenarios
// 6.1–6.8, 7.8, 15.1/15.9–15.11, 19.1–19.5/19.18, 20.1–20.10.
// Real protected agent store + real tool/registry. No LLM or network required.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8ReadFixture(t *testing.T) (*systools.Deps, *agentstore.Store) {
	t.Helper()
	d, cfg := newTestDeps(t)
	a := config.AgentConfig{ID: "r1", Name: "R1", Type: config.AgentTypeCustom,
		Model:             &config.AgentModelConfig{Primary: "primary-model", Provider: "openrouter"},
		FallbackModels:    config.FallbackModelSlice{{Provider: "openrouter", Model: "fallback-one"}, {Provider: "missing-provider", Model: "fallback-two"}},
		MaxToolIterations: 40, Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}}},
	}
	s := agentstore.New(d.Home)
	_, err := s.CreateState(a.ID, &a, "Only the selected soul")
	require.NoError(t, err)
	cfg.Agents.List = append(cfg.Agents.List, a)
	defaultToolsWireCatalog(t, d, append(systools.AllTools(d), tools.GeneralBuiltinMetadata()...))
	return d, s
}

func t8Read(t *testing.T, d *systools.Deps, args map[string]any) map[string]any {
	t.Helper()
	r := systools.NewAgentGetTool(d).Execute(tools.WithAgentID(context.Background(), "r1"), args)
	require.False(t, r.IsError, "%s", r.ForLLM)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.ForLLM), &out))
	return out
}
func t8ReadKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestGetAgent_FieldsSelectorsAndPreview(t *testing.T) {
	t.Run("19.1 exact projection", func(t *testing.T) {
		d, _ := t8ReadFixture(t)
		got := t8Read(t, d, map[string]any{"id": "r1", "fields": []any{"model_chain", "tool_permissions"}})
		assert.Equal(t, []string{"id", "model_chain", "revision", "tool_permissions"}, t8ReadKeys(got))
	})
	t.Run("19.3 no selector means caller", func(t *testing.T) {
		d, _ := t8ReadFixture(t)
		got := t8Read(t, d, map[string]any{"fields": []any{"soul"}})
		assert.Equal(t, []string{"id", "revision", "soul"}, t8ReadKeys(got))
		assert.Equal(t, "r1", got["id"])
		assert.Equal(t, "Only the selected soul", got["soul"])
	})
	t.Run("19.4 preview has no persisted identity", func(t *testing.T) {
		d, s := t8ReadFixture(t)
		before, err := os.ReadDir(filepath.Join(d.Home, "entities", "agents"))
		require.NoError(t, err)
		got := t8Read(t, d, map[string]any{"new_agent_type": "subagent_3p", "fields": []any{"tool_permissions"}})
		assert.Equal(t, []string{"preview_of", "tool_permissions"}, t8ReadKeys(got))
		assert.Equal(t, "subagent_3p", got["preview_of"])
		after, err := os.ReadDir(filepath.Join(d.Home, "entities", "agents"))
		require.NoError(t, err)
		assert.Len(t, after, len(before))
		_, err = s.ReadState("r1")
		require.NoError(t, err)
	})
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"19.4b preview refusal", map[string]any{"new_agent_type": "Main", "fields": []any{"soul"}}, `fields for a new-agent preview: pass ["all"] or one or more of: tool_permissions, connectors`},
		{"19.5 conflicting selectors", map[string]any{"id": "r1", "name": "R1", "fields": []any{"all"}}, "Supply at most one of: id, name, new_agent_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := t8ReadFixture(t)
			r := systools.NewAgentGetTool(d).Execute(context.Background(), tc.args)
			require.True(t, r.IsError, "%s", r.ForLLM)
			assert.Contains(t, r.ForLLM, tc.want)
		})
	}
}

func TestGetAgent_EmptySelectors(t *testing.T) {
	for _, args := range []map[string]any{
		{"id": "", "name": "R1", "fields": []any{"name"}},
		{"id": "", "new_agent_type": "Main", "fields": []any{"tool_permissions"}},
	} {
		t.Run("15.1", func(t *testing.T) {
			d, _ := t8ReadFixture(t)
			got := t8Read(t, d, args)
			if args["name"] != nil {
				assert.Equal(t, "r1", got["id"])
				assert.Equal(t, "R1", got["name"])
			} else {
				assert.Equal(t, "Main", got["preview_of"])
			}
		})
	}
}
func TestGetAgent_ExactId(t *testing.T) {
	d, _ := t8ReadFixture(t)
	r := systools.NewAgentGetTool(d).Execute(context.Background(), map[string]any{"id": "R1", "fields": []any{"all"}})
	require.True(t, r.IsError, "15.10 id comparison is exact")
	assert.Contains(t, r.ForLLM, "AGENT_NOT_FOUND")
}
func TestGetAgent_DetailAndModelChain(t *testing.T) {
	d, _ := t8ReadFixture(t)
	got := t8Read(t, d, map[string]any{"id": "r1", "fields": []any{"model_chain", "max_tool_iterations"}})
	assert.Equal(t, float64(40), got["max_tool_iterations"], "6.1 configured turn limit")
	chain, ok := got["model_chain"].([]any)
	require.True(t, ok, "BLOCKED: AgentDetail.model_chain not implemented — required by §7.5 AG-16 / 6.1–6.5")
	require.Len(t, chain, 3)
	for i, want := range []string{"primary-model", "fallback-one", "fallback-two"} {
		row, ok := chain[i].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, want, row["model"])
	}
	last := chain[2].(map[string]any)
	assert.Equal(t, false, last["provider_configured"], "6.2 missing provider")
	assert.Contains(t, last["warning"], "no configured provider")
}
func TestGetAgent_FieldValidation_ThroughRegistry(t *testing.T) {
	const valid = "name, description, type, locked, figure, role, color, voice, model, provider, fallback_models, model_chain, model_params, max_tool_iterations, context_window_override, memory_enabled, soul, agent_md, skills, tool_permissions, stored_overrides, connectors, executor, runnable, program_warning, activation_status, editable_fields, has_main_chat"
	for _, tc := range []struct {
		name    string
		fields  any
		present bool
		want    string
	}{
		{"20.1 missing", nil, false, `fields is required for agent: pass ["all"] or one or more of: ` + valid},
		{"20.2 empty", []any{}, true, `fields is required for agent: pass ["all"] or one or more of: ` + valid},
		{"20.2 string", "all", true, `fields is required for agent: pass ["all"] or one or more of: ` + valid},
		{"20.3 unknown", []any{"name", "nope"}, true, "unknown field \"nope\" for agent. Valid: all, " + valid},
		{"20.4 all combined", []any{"all", "name"}, true, `"all" cannot be combined with other field names`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := t8ReadFixture(t)
			reg := tools.NewToolRegistry()
			reg.Register(systools.NewAgentGetTool(d))
			args := map[string]any{"id": "r1"}
			if tc.present {
				args["fields"] = tc.fields
			}
			r := reg.Execute(context.Background(), "get_agent", args)
			require.True(t, r.IsError, "%s", r.ForLLM)
			assert.Equal(t, tc.want, r.ForLLM)
			assert.NotContains(t, r.ForLLM, "invalid arguments for tool")
		})
	}
}
func TestCreateAgent_HeartbeatRefused(t *testing.T) {
	d, _ := newTestDeps(t)
	r := tools.NewToolRegistry()
	r.Register(systools.NewAgentCreateTool(d))
	got := r.Execute(context.Background(), "create_agent", map[string]any{"name": "new worker", "description": "T8 worker", "model": "test-model", "agent_type": "Subagent", "soul": "soul", "heartbeat": "check the queue"})
	require.True(t, got.IsError)
	assert.Contains(t, got.ForLLM, "heartbeat", "15.9 unknown argument must name heartbeat")
}
func TestUpdateAgent_RefusesPermissionFields_ProductionRegistry(t *testing.T) {
	for _, field := range []string{"tool_policy_changes", "tools_cfg", "mcp_servers", "cli_path", "preset_args", "rate_limits"} {
		t.Run(field, func(t *testing.T) {
			d, s := t8ReadFixture(t)
			state, err := s.ReadState("r1")
			require.NoError(t, err)
			path := filepath.Join(d.Home, "entities", "agents", "r1.json")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			reg := tools.NewToolRegistry()
			reg.Register(systools.NewAgentUpdateTool(d))
			var value any = map[string]any{}
			if field == "cli_path" || field == "preset_args" {
				value = "/opt/t8/program"
			}
			if field == "mcp_servers" {
				value = []any{}
			}
			got := reg.Execute(context.Background(), "update_agent", map[string]any{"id": "r1", "revision": state.Revision, field: value})
			require.True(t, got.IsError, "7.8/8.13/9.15/9.25 %s", field)
			assert.Contains(t, got.ForLLM, field)
			var envelope map[string]any
			require.NoError(t, json.Unmarshal([]byte(got.ForLLM), &envelope), "decode the change-result carrier before inspecting its message")
			errBody, ok := envelope["error"].(map[string]any)
			require.True(t, ok, "refusal has an error object: %v", envelope)
			assert.Equal(t, `invalid arguments for tool "update_agent": unexpected property "`+field+`"`, errBody["message"])
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "unknown argument may not write")
		})
	}
}

func TestReadSchemas_Bounded(t *testing.T) {
	d, _ := newTestDeps(t)
	tool := systools.NewAgentGetTool(d)
	props := tool.Parameters()["properties"].(map[string]any)
	for _, key := range []string{"id", "name"} {
		p, ok := props[key].(map[string]any)
		require.True(t, ok, "15.11 missing selector %s", key)
		assert.NotNil(t, p["maxLength"], "bounded %s", key)
		if key == "id" {
			assert.EqualValues(t, 1, p["minLength"])
		}
	}
	// 15.10 checks runtime exactness separately; schema inspection is a contract
	// test, not evidence that the gateway/tool validates the bound at runtime.
	assert.NotContains(t, strings.Join(t8ReadKeys(props), ","), "cli_path")
}

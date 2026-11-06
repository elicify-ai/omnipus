package systools_test

// Independent r17 oracles: 6.3–6.5, 7.4, 8.12, 9.24, 9.28, 19.12.
import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8ModelChain_InheritanceLegacyAndUnknownCapability(t *testing.T) {
	for _, kind := range []string{"6.3 inherited", "6.4 legacy fallback", "6.5 capability unknown"} {
		t.Run(kind, func(t *testing.T) {
			d, s := t8ReadFixture(t)
			cfg := d.GetCfg()
			cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "openrouter", Model: "inherited-primary"}
			state, err := s.ReadState("r1")
			require.NoError(t, err)
			_, err = s.MutateState("r1", state.Revision, func(a *config.AgentConfig) error {
				a.Model = nil
				a.FallbackModels = nil
				if kind == "6.4 legacy fallback" {
					a.FallbackModels = config.FallbackModelSlice{{Model: "legacy-only-model"}}
				}
				for i := range cfg.Agents.List {
					if cfg.Agents.List[i].ID == a.ID {
						cfg.Agents.List[i] = *a
					}
				}
				return nil
			}, nil)
			require.NoError(t, err)
			out := t8Read(t, d, map[string]any{"id": "r1", "fields": []any{"model_chain"}})
			chain, ok := out["model_chain"].([]any)
			require.True(t, ok, "model_chain must be a real read result")
			require.NotEmpty(t, chain)
			primary, ok := chain[0].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "openrouter", primary["provider"])
			assert.Equal(t, "inherited-primary", primary["model"])
			if kind == "6.4 legacy fallback" {
				require.Len(t, chain, 2)
				row, isRow := chain[1].(map[string]any)
				require.True(t, isRow, "chain row is an object")
				assert.Equal(t, "legacy-only-model", row["model"])
				assert.Contains(t, row["warning"], "not paired with a provider — reported, never routed")
			}
			if kind == "6.5 capability unknown" {
				require.Contains(t, primary, "accepts_images")
				assert.Nil(t, primary["accepts_images"], "unknown is null, never guessed true or false")
			}
		})
	}
}

func TestT8AgentRevision_StaleAfterAgentMDOnlyChange(t *testing.T) {
	d, s := t8ReadFixture(t)
	v1, err := s.ReadState("r1")
	require.NoError(t, err)
	md := filepath.Join(d.Home, "agents", "r1", "AGENT.md")
	require.NoError(t, os.WriteFile(md, []byte("other writer changed only instructions"), 0o600))
	v2, err := s.ReadState("r1")
	require.NoError(t, err)
	assert.NotEqual(t, v1.Revision, v2.Revision, "7.3 instructions are part of the revision")
	entity := filepath.Join(d.Home, "entities", "agents", "r1.json")
	before, err := os.ReadFile(entity)
	require.NoError(t, err)
	reg := tools.NewToolRegistry()
	reg.Register(systools.NewAgentUpdateTool(d))
	r := reg.Execute(tools.WithAgentID(context.Background(), "omnipus"), "update_agent", map[string]any{"id": "r1", "revision": v1.Revision, "name": "must not save"})
	require.True(t, r.IsError, "7.4 an AGENT.md-only intervening edit makes the old revision stale")
	assert.Contains(t, r.ForLLM, "CONFLICT")
	after, err := os.ReadFile(entity)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	afterMD, err := os.ReadFile(md)
	require.NoError(t, err)
	assert.Equal(t, "other writer changed only instructions", string(afterMD))
}

func TestT8Settings_SetConfigCannotChangeProgramOrPreset(t *testing.T) {
	for _, key := range []string{"sandbox.third_party_agents.clis.codex.path", "sandbox.third_party_agents.clis.codex.preset_args"} {
		t.Run(key, func(t *testing.T) {
			d, path := newTestDepsWithRealSave(t)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			reg := tools.NewToolRegistry()
			reg.Register(systools.NewConfigSetTool(d))
			r := reg.Execute(tools.WithAgentID(context.Background(), "omnipus"), "set_config", map[string]any{"key": key, "value": "not-agent-owned"})
			require.True(t, r.IsError, "9.24 person-only Settings must not be writable by set_config")
			// r17 9.24 specifies refusal, not an error-code spelling.
			body := parseError(t, r.ForLLM)
			refusal, ok := body["error"].(map[string]any)
			require.True(t, ok)
			assert.Contains(t, refusal["message"], key, "the specific setting is refused, not a constant catch-all error")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			// Positive instrument: this is a functioning writable config store.
			control := reg.Execute(context.Background(), "set_config", map[string]any{"key": "gateway.port", "value": float64(4243)})
			require.False(t, control.IsError, "unrelated supported config edit: %s", control.ForLLM)
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.NotEqual(t, before, saved)
		})
	}
}

func TestT8AgentExecutor_NativeRefusalAndCLILock(t *testing.T) {
	t.Run("8.12 native Main has no runtime environment", func(t *testing.T) {
		d, s := t8ReadFixture(t)
		before, err := s.ReadState("r1")
		require.NoError(t, err)
		r := systools.NewAgentUpdateTool(d).Execute(tools.WithAgentID(context.Background(), "omnipus"), map[string]any{"id": "r1", "revision": before.Revision, "env_overrides": map[string]any{"A": "1"}})
		require.True(t, r.IsError)
		assert.Contains(t, t8ChangeMessage(t, r), "Only external-CLI workers have a runtime environment.")
		after, err := s.ReadState("r1")
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})
	t.Run("9.28 agent cannot change CLI", func(t *testing.T) {
		d, s := t8ExternalFixture(t)
		before, err := s.ReadState("r1")
		require.NoError(t, err)
		reg := tools.NewToolRegistry()
		reg.Register(systools.NewAgentUpdateTool(d))
		r := reg.Execute(tools.WithAgentID(context.Background(), "omnipus"), "update_agent", map[string]any{"id": "r1", "revision": before.Revision, "cli": "opencode"})
		require.True(t, r.IsError)
		assert.Contains(t, r.ForLLM, "cli")
		after, err := s.ReadState("r1")
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})
}

func TestT8ReadAgent_LiveConnectorWildcardAndStoredOverrides(t *testing.T) {
	d, store := t8ReadFixture(t)
	first := tools.NewMCPTool(nil, "S", &mcp.Tool{Name: "first", Description: "first live connector tool"})
	second := tools.NewMCPTool(nil, "S", &mcp.Tool{Name: "second", Description: "second live connector tool"})
	unassigned := tools.NewMCPTool(nil, "other", &mcp.Tool{Name: "hidden"})
	d.AgentConfigInventory = testInventory(nil, map[string][]string{"S": {first.Name(), second.Name()}, "other": {unassigned.Name()}})
	defaultToolsWireCatalog(t, d, append(systools.AllTools(d), first, second, unassigned))
	before, err := store.ReadState("r1")
	require.NoError(t, err)
	_, err = store.MutateState("r1", before.Revision, func(a *config.AgentConfig) error {
		a.Tools.MCP.Servers = []config.AgentMCPServerBinding{{ID: "S", ToolsSpecified: true, Tools: []string{"*"}}}
		a.Tools.Builtin.Policies = map[string]config.ToolPolicy{"mcp_s_*": config.ToolPolicyAsk, "mcp_gone_tool": config.ToolPolicyDeny}
		for i := range d.GetCfg().Agents.List {
			if d.GetCfg().Agents.List[i].ID == a.ID {
				d.GetCfg().Agents.List[i] = *a
			}
		}
		return nil
	}, nil)
	require.NoError(t, err)
	d.GetCfg().Sandbox.ToolPolicies["mcp_s_*"] = "allow"
	out := t8Read(t, d, map[string]any{"id": "r1", "fields": []any{"tool_permissions", "stored_overrides"}})
	assert.Equal(t, map[string]any{"mcp_s_*": "ask", "mcp_gone_tool": "deny"}, out["stored_overrides"], "19.18 raw stored entries are not expanded or pruned")
	rows, ok := out["tool_permissions"].([]any)
	require.True(t, ok)
	seen := map[string]bool{}
	for _, value := range rows {
		row, ok := value.(map[string]any)
		require.True(t, ok)
		name, ok := row["tool"].(string)
		require.True(t, ok)
		assert.NotEqual(t, unassigned.Name(), name, "unassigned connector never appears")
		if name == first.Name() || name == second.Name() {
			seen[name] = true
			assert.Equal(t, "ask", row["effective"])
			require.Contains(t, row, "override")
			assert.Nil(t, row["override"], "wildcard is not an exact per-tool override")
		}
	}
	assert.Equal(t, map[string]bool{first.Name(): true, second.Name(): true}, seen, "all live assigned tools are enumerated")
}

func TestT8Permissions_CreateCannotGrantManagementTool(t *testing.T) {
	d, store := t8ReadFixture(t)
	before, skipped, err := store.List()
	require.NoError(t, err)
	require.Empty(t, skipped)
	reg := tools.NewToolRegistry()
	reg.Register(systools.NewAgentCreateTool(d))
	args := map[string]any{"name": "T8 grant probe", "agent_type": "Subagent", "description": "bounded native worker", "soul": "a test worker", "model": "primary-model", "provider": "openrouter", "tool_policy_changes": map[string]any{"set": map[string]any{"set_agent_permissions": "ask"}}}
	r := reg.Execute(tools.WithAgentID(context.Background(), "omnipus"), "create_agent", args)
	require.True(t, r.IsError, "19.12 management grants are forbidden at creation")
	assert.Contains(t, r.ForLLM, "set_agent_permissions")
	assert.Contains(t, r.ForLLM, "management")
	after, skipped, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, skipped)
	assert.Equal(t, before, after, "refused creation has no persisted side effect")
	// Same valid worker with a permitted creation-time grant must succeed.
	// A constant refusal therefore cannot pass this test.
	args["tool_policy_changes"] = map[string]any{"set": map[string]any{"bash": "ask"}}
	control := reg.Execute(tools.WithAgentID(context.Background(), "omnipus"), "create_agent", args)
	require.False(t, control.IsError, "ordinary permitted creation: %s", control.ForLLM)
	created, skipped, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, skipped)
	assert.Len(t, created, len(before)+1)
	for _, a := range created {
		if a.Name == "T8 grant probe" {
			require.NotNil(t, a.Tools)
			assert.Equal(t, config.ToolPolicyAsk, a.Tools.Builtin.Policies["bash"])
		}
	}
}

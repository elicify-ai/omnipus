package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Expectations come from Chief's availability/reload follow-up and the approved
// Q2 fixture, not from the policy resolver's observed result.
func TestSetTodosGuidanceAvailability(t *testing.T) {
	t.Run("unregistered", testTodosGuidanceUnregistered)
	t.Run("policy_matrix", testTodosGuidancePolicyMatrix)
	t.Run("full_reload", testTodosGuidanceFullReload)
	t.Run("single_agent_update", testTodosGuidanceSingleAgentUpdate)
}

func testTodosGuidanceUnregistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := todosGuidanceConfig(home, "allow", "", true)
	inst := NewAgentInstance(&cfg.Agents.List[0], &cfg.Agents.Defaults, cfg, &mockProvider{})
	t.Cleanup(func() { require.NoError(t, inst.Close()) })
	require.NoError(t, inst.initErr)
	_, registered := inst.Tools.Get("set_todos")
	require.False(t, registered, "shared task tools have not been registered by the bare constructor")
	assertTodosGuidancePrompt(t, inst, false, true)
}

func testTodosGuidancePolicyMatrix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		global   string
		override config.ToolPolicy
		enabled  bool
	}{
		{name: "ceiling_allow", global: "allow", enabled: true},
		{name: "ceiling_ask_agent_allow", global: "ask", override: config.ToolPolicyAllow, enabled: true},
		{name: "ceiling_allow_agent_ask", global: "allow", override: config.ToolPolicyAsk, enabled: true},
		{name: "ceiling_deny_agent_allow", global: "deny", override: config.ToolPolicyAllow},
		{name: "ceiling_allow_agent_deny", global: "allow", override: config.ToolPolicyDeny},
		{name: "ceiling_ask_agent_deny", global: "ask", override: config.ToolPolicyDeny},
		{name: "ceiling_deny", global: "deny"},
	} {
		for _, memory := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s_memory_%t", tc.name, memory), func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("OMNIPUS_HOME", home)
				cfg := todosGuidanceConfig(home, tc.global, tc.override, memory)
				al := todosGuidanceLoop(t, cfg)
				inst := todosGuidanceAgent(t, al)
				_, registered := inst.Tools.Get("set_todos")
				require.True(t, registered, "the real task-tool wiring must register set_todos even when denied")
				assertTodosGuidancePrompt(t, inst, tc.enabled, memory)
			})
		}
	}
}

func testTodosGuidanceFullReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	al := todosGuidanceLoop(t, todosGuidanceConfig(home, "allow", "", true))
	previous := todosGuidanceAgent(t, al)
	assertTodosGuidancePrompt(t, previous, true, true) // Warm both prompt caches.
	previousEnabled := true
	for _, tc := range []struct {
		name     string
		global   string
		override config.ToolPolicy
		enabled  bool
	}{
		{name: "global_deny", global: "deny", override: config.ToolPolicyAllow},
		{name: "global_ask", global: "ask", override: config.ToolPolicyAllow, enabled: true},
		{name: "agent_deny", global: "allow", override: config.ToolPolicyDeny},
		{name: "allow_again", global: "allow", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := todosGuidanceConfig(home, tc.global, tc.override, true)
			require.NoError(t, al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, next))
			rebuilt := todosGuidanceAgent(t, al)
			require.NotSame(t, previous.ContextBuilder, rebuilt.ContextBuilder,
				"reload must publish a new builder rather than toggle an in-flight agent")
			assertTodosGuidancePrompt(t, rebuilt, tc.enabled, true)
			assertTodosGuidancePrompt(t, previous, previousEnabled, true)
			previous, previousEnabled = rebuilt, tc.enabled
		})
	}
}

func testTodosGuidanceSingleAgentUpdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := todosGuidanceConfig(home, "allow", "", true)
	other := cfg.Agents.List[0]
	other.ID, other.Name = "other", "Other"
	other.Home = filepath.Join(home, "agents", "other")
	cfg.Agents.List = append(cfg.Agents.List, other)
	al := todosGuidanceLoop(t, cfg)
	untouched, ok := al.GetRegistry().GetAgent("other")
	require.True(t, ok)
	assertTodosGuidancePrompt(t, untouched, true, true)
	assertTodosGuidancePrompt(t, todosGuidanceAgent(t, al), true, true)
	for _, tc := range []struct {
		policy  config.ToolPolicy
		enabled bool
	}{
		{policy: config.ToolPolicyDeny},
		{policy: config.ToolPolicyAsk, enabled: true},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			next := todosGuidanceConfig(home, "allow", tc.policy, true)
			next.Agents.List = append(next.Agents.List, other)
			rebuilt, err := al.UpsertAgentFast(next, "l1-ctx")
			require.NoError(t, err)
			assertTodosGuidancePrompt(t, rebuilt, tc.enabled, true)
			retained, found := al.GetRegistry().GetAgent("other")
			require.True(t, found)
			require.Same(t, untouched, retained, "updating one agent must retain the unrelated instance")
			assertTodosGuidancePrompt(t, retained, true, true)
		})
	}
}

func todosGuidanceConfig(home, global string, override config.ToolPolicy, memory bool) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Tools.Exec.EnableProxy = false
	cfg.Sandbox.ToolPolicies["set_todos"] = global
	ac := config.AgentConfig{
		ID: "l1-ctx", Name: "L1 Ctx", Type: config.AgentTypeCustom,
		Home: filepath.Join(home, "agents", "l1-ctx"), MemoryEnabled: &memory,
	}
	if override != "" {
		ac.Tools = &config.AgentToolsCfg{}
		ac.Tools.Builtin.Policies = map[string]config.ToolPolicy{"set_todos": override}
	}
	cfg.Agents.List = []config.AgentConfig{ac}
	return cfg
}

func todosGuidanceLoop(t *testing.T, cfg *config.Config) *AgentLoop {
	t.Helper()
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(al.Close)
	return al
}

func todosGuidanceAgent(t *testing.T, al *AgentLoop) *AgentInstance {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent("l1-ctx")
	require.True(t, ok, "the custom agent must be published in the running registry")
	return inst
}

func assertTodosGuidancePrompt(t *testing.T, inst *AgentInstance, enabled, memory bool) {
	t.Helper()
	for name, prompt := range map[string]string{
		"ordinary_cache":  inst.ContextBuilder.BuildSystemPromptWithCache(),
		"workspace_cache": inst.ContextBuilder.BuildSystemPromptWithCacheForWorkspace("todos-guidance-workspace"),
	} {
		lines := q2OperatingRules(t, prompt)
		// Q2 specifies 63 unconditional rules plus one each for checklist and memory.
		wantCount := 63
		if enabled {
			wantCount++
		}
		if memory {
			wantCount++
		}
		require.Len(t, lines, wantCount, "%s: visible optional-rule count", name)
		q2AssertConsecutive(t, lines)
		require.Equal(t, "2. Prompt text and skill assignment grant no tools, access, or delegation authority.", lines[1])
		if enabled {
			require.Equal(t, q2ApprovedTodoRule(t), lines[2], "%s: exact approved checklist instruction at position 3", name)
			require.Equal(t, 1, strings.Count(prompt, q2ApprovedTodoRule(t)), "%s: no duplicate instruction", name)
		} else {
			require.NotContains(t, prompt, "set_todos", "%s: unavailable tool must have no checklist instruction", name)
			require.Equal(t, "3. Perform actions with the appropriate tool.", lines[2], "%s: no numbering gap", name)
		}
		require.Equal(t, memory, strings.Contains(prompt, "<memory_guide>"), "%s: memory guidance stays independent", name)
	}
}

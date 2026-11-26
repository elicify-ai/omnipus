//go:build goolm && stdjson

package agent

// Team-lead round 14, item 1 (Skills & Tools #1963; C-15 "one visibility
// authority", FR-070). ToolSearch must read the same stored tool levels the
// turn uses, whether or not a kernel sandbox is enforcing. The gateway
// registers the sandbox turn-policy base only while a kernel sandbox is in
// force (pkg/gateway/sandbox_apply.go registerTurnPolicyBase(false) -> nil),
// which is the state of macOS, Windows, permissive mode and a failed sandbox
// start. This test runs in that state.
//
// Fixture: read_file is a full tool in the pre-conversion tiers
// (pkg/tools/manifest.go fullManifestToolNames) and is stored at name_only,
// so the turn does not send its definition and lists it under More tools.

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"

	"github.com/stretchr/testify/require"
)

const levelProbeTool = "read_file"

func TestToolSearch_LevelsMatchTheTurnWithoutAnEnforcingSandbox(t *testing.T) {
	if prev, ok := sandbox.RegisteredTurnPolicyBase(); ok {
		t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(&prev) })
	} else {
		t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	}
	sandbox.RegisterTurnPolicyBase(nil) // no enforcing kernel sandbox

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "levels-test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 1,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}
	cfg.Sandbox.ToolPolicies = map[string]string{levelProbeTool: "allow"}
	cfg.Sandbox.ToolVisibility = map[string]string{levelProbeTool: config.VisibilityNameOnly}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &capturingProvider{})
	t.Cleanup(al.Close)
	inst := al.registry.primaryChatAgentForTest()
	require.NotNil(t, inst)
	search, ok := inst.Tools.Get("ToolSearch")
	require.True(t, ok, "the real loop registers ToolSearch")

	t.Run("instrument: the turn and the old tiers disagree about the probe tool", func(t *testing.T) {
		require.Equal(t, tools.ManifestFull, tools.ToolManifestTier(levelProbeTool),
			"the pre-conversion tiers send the probe tool every turn")
		turn := al.readTurnToolSurface()
		require.Equal(t, config.VisibilityNameOnly, turn.LevelOf(levelProbeTool), "the turn reads the stored level")
		require.Equal(t, tools.ManifestLazy, turn.TierOf(levelProbeTool), "the turn does not send the probe tool's definition")
	})

	t.Run("a load by name loads the tool the turn does not send", func(t *testing.T) {
		res := search.Execute(execCtx(inst.ID, "levels-load"), map[string]any{"names": []any{levelProbeTool}})
		require.False(t, res.IsError, "ToolSearch loads an allowed name_only tool: %s", res.ForLLM)
		require.NotContains(t, res.ForLLM, tools.CanLoadAlreadyAvailablePrefix,
			"C-15: the turn does not send %s, so ToolSearch must not tell the model it is already available", levelProbeTool)
	})

	t.Run("a search finds the tool the turn lists under More tools", func(t *testing.T) {
		res := search.Execute(execCtx(inst.ID, "levels-search"), map[string]any{"query": "read the contents of a file"})
		require.False(t, res.IsError, "ToolSearch answers a query: %s", res.ForLLM)
		require.True(t, strings.Contains(res.ForLLM, levelProbeTool),
			"C-15: a name_only tool is searchable, so the search must find %s, got:\n%s", levelProbeTool, res.ForLLM)
	})
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// goal_claim_default_allow_test.go — goal_claim resolves allow by default on
// every path that creates an agent (founder decision 2026-09-15, issue #710).
// A native task run completes only when its goal_claim is upheld by the Judge,
// so an agent created without it by default could never finish a task.
//
// Paths enumerated:
//   - the fresh-install seed (every non-System seeded agent);
//   - coreagent.NewCustomAgentToolsCfg, the one shared default for new agents;
//   - REST POST /api/v1/agents with no tools_cfg, for Main, Subagent and
//     subagent_3p;
//   - the create_agent system tool;
//   - an agent defined in stored config with no tool map, or with a map that
//     does not mention goal_claim (it rides the global ceiling).
//
// Excluded by design: the Judge and PlanSupervisor System Agents, whose whole
// tool policy is a role invariant re-applied every boot and which can never
// own a goal or be assigned a task. A UI-created agent submits the complete
// policy map its role preset builds (src/lib/toolPolicyPresets.ts), so its
// value is the preset's, merged over NewCustomAgentToolsCfg by createAgent.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// shippedCeiling is the global tool-policy ceiling a fresh install ships.
func shippedCeiling() map[string]config.ToolPolicy {
	out := make(map[string]config.ToolPolicy)
	for k, v := range config.DefaultConfig().Sandbox.ToolPolicies {
		out[k] = config.ToolPolicy(v)
	}
	return out
}

// resolveGoalClaim resolves goal_claim for an agent's own stored map against
// the shipped ceiling through the real compositor merge.
func resolveGoalClaim(agentPolicies map[string]config.ToolPolicy) string {
	return tools.ResolveEffectivePolicy(&tools.ToolPolicyCfg{
		Policies:       agentPolicies,
		GlobalPolicies: shippedCeiling(),
	}, tools.GoalClaimToolName)
}

func TestGoalClaimDefaultsToAllow_EveryAgentCreationPath(t *testing.T) {
	t.Run("fresh-install seed: every non-System seeded agent", func(t *testing.T) {
		cfg := config.DefaultConfig()
		require.True(t, coreagent.SeedConfig(cfg))
		nonSystem := 0
		for _, ac := range cfg.Agents.List {
			if ac.IsSystem() {
				continue
			}
			nonSystem++
			require.NotNil(t, ac.Tools, "seeded agent %q must carry a tool map", ac.ID)
			assert.Equalf(t, "allow", resolveGoalClaim(ac.Tools.Builtin.Policies),
				"seeded agent %q must resolve goal_claim allow by default", ac.ID)
		}
		assert.Equal(t, 8, nonSystem, "Mia, Jim, Ava, Ray, Worker, Planner, Explorer, Researcher")
	})

	t.Run("NewCustomAgentToolsCfg", func(t *testing.T) {
		p := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
		assert.Equal(t, config.ToolPolicyAllow, p[tools.GoalClaimToolName])
		assert.Equal(t, "allow", resolveGoalClaim(p))
	})

	for _, tc := range []struct {
		kind string
		body string
	}{
		{"Main", `{"name":"Default Main","type":"Main","soul":"s"}`},
		{"Subagent", `{"name":"Default Subagent","type":"Subagent","description":"default policy check","soul":"s"}`},
		{"subagent_3p", `{"name":"Default External","type":"subagent_3p","description":"default policy check","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"}}`},
	} {
		t.Run("REST POST /api/v1/agents without tools_cfg: "+tc.kind, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
			created := decodeAgentResp(t, w.Body.Bytes())

			stored, err := agentstore.New(api.homePath).Get(created.Id)
			require.NoError(t, err)
			require.NotNil(t, stored.Tools, "a created agent must persist a tool map")
			assert.Equal(t, config.ToolPolicyAllow, stored.Tools.Builtin.Policies[tools.GoalClaimToolName],
				"the persisted default must be an explicit allow")
			assert.Equal(t, "allow", resolveGoalClaim(stored.Tools.Builtin.Policies))
		})
	}

	t.Run("create_agent system tool", func(t *testing.T) {
		home := t.TempDir()
		cfg := config.DefaultConfig()
		var mu sync.Mutex
		getCfg := func() *config.Config { return cfg }
		deps := &systools.Deps{
			Home:       home,
			ConfigPath: home + "/config.json",
			GetCfg:     getCfg,
			MutateConfig: func(fn func(*config.Config) error) error {
				mu.Lock()
				defer mu.Unlock()
				return fn(getCfg())
			},
			SaveConfigLocked: func(*config.Config) error { return nil },
		}
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
			"name":        "Tool Made",
			"description": "created by the create_agent tool",
			"soul":        "You are tool made.",
			"model":       "test/model",
			"color":       "#22C55E",
			"icon":        "robot",
		})
		require.False(t, result.IsError, "create_agent failed: %s", result.ForLLM)

		stored, err := agentstore.New(home).Get("tool-made")
		require.NoError(t, err)
		require.NotNil(t, stored.Tools)
		assert.Equal(t, config.ToolPolicyAllow, stored.Tools.Builtin.Policies[tools.GoalClaimToolName])
		assert.Equal(t, "allow", resolveGoalClaim(stored.Tools.Builtin.Policies))
	})

	t.Run("agent defined in stored config with no tool map", func(t *testing.T) {
		assert.Equal(t, "allow", resolveGoalClaim(nil),
			"an agent with no tool map rides the shipped ceiling, which is allow")
	})

	t.Run("agent defined in stored config with a map that omits goal_claim", func(t *testing.T) {
		assert.Equal(t, "allow", resolveGoalClaim(map[string]config.ToolPolicy{"bash": config.ToolPolicyDeny}),
			"a sparse map that does not mention goal_claim rides the shipped ceiling")
	})
}

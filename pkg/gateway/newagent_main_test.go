// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Founder-reported bug (2026-10-11): an agent created by Ava (the create_agent
// tool) inside a workspace joined the team but did not get its main chat, so the
// sidebar (which lists a member only when its workspace member_configs entry
// carries main_session_id) never showed it. The real tool is driven here with the
// production wiring order: the tool's publish hook refreshes the live roster first,
// and only then does EnsureWorkspaceMains run (its eligibility reads that roster).

package gateway

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newAgentCreateToolDeps wires systools.Deps the way gateway.go::wireSystemTools
// does for the parts that matter here: the live config accessors, the main
// provisioner, and a publish hook that (like production) is what first puts the
// new agent into the live roster.
func newAgentCreateToolDeps(env *u1Env) *systools.Deps {
	al := env.api.agentLoop
	return &systools.Deps{
		Home:                 env.home,
		ConfigPath:           filepath.Join(env.home, "config.json"),
		GetCfg:               al.GetConfig,
		MutateConfig:         al.MutateConfig,
		SaveConfigLocked:     func(*config.Config) error { return nil },
		EnsureWorkspaceMains: env.api.ensureWorkspaceMains,
		UpsertAgentFastFunc: func(string) error {
			return al.MutateConfig(func(cfg *config.Config) error {
				return populateAgentsListFromEntityStoreStrict(cfg, env.home)
			})
		},
	}
}

func TestNewAgent_CreatedByAvaInWorkspace_GetsItsMainListed(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JGWNEWAGENTWS00000000001"
	env.seedWorkspace(t, ws, false, "mia")

	res := systools.NewAgentCreateTool(newAgentCreateToolDeps(env)).Execute(
		tools.WithWorkspaceID(context.Background(), ws), map[string]any{
			"name": "Newbie", "description": "created in context", "soul": "You help.",
			"model": "test/model", "color": "#6E8BA8",
		})
	require.False(t, res.IsError, "create failed: %s", res.ForLLM)
	// New agents get a generated id (founder decision 2026-10-14, WU-3).
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &created))
	id := created.ID
	require.NotEmpty(t, id)

	got := env.getWorkspace(t, ws)
	team, _ := got["core_team"].([]any)
	require.Contains(t, team, id, "instrument check: the agent joined the team")

	mains, _ := got["member_configs"].(map[string]any)
	member, _ := mains[id].(map[string]any)
	assert.Equal(t, u1MainID(ws, id), member["main_session_id"],
		"the new member must be listed with its main (the sidebar's source); member_configs=%v", mains)
	_, err := env.store(t).GetMeta(u1MainID(ws, id))
	assert.NoError(t, err, "the new member's main session must exist after create_agent returns")
}

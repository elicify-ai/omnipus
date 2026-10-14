// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// TestSetAgentPermissions_TakesEffectInTheLiveRegistryWithoutRestart pins the
// omnipus-system-agent §6.5 rule that every grant lives in the executable
// per-agent registry "built at boot and on reload" for the permission split
// (FR-024): a set_agent_permissions change, published through the SAME hook
// production wires (sysagentFastUpsert), is what the very next lookup
// resolves — no gateway restart (issue #1296 shape). The before-lookup is the
// positive control that the instrument reads the live registry at all.
func TestSetAgentPermissions_TakesEffectInTheLiveRegistryWithoutRestart(t *testing.T) {
	api := buildExecutorTestAPI(t)
	al := api.agentLoop
	// Production boots with the shipped global ceiling in place; the tool's
	// known-tool check and the live resolver both read it.
	require.NoError(t, al.MutateConfig(func(cfg *config.Config) error {
		cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
		return nil
	}))
	publishPersistedTestAgent(t, api)

	before, ok := al.ResolveRegisteredToolPolicy("test-agent", "read_file")
	require.True(t, ok, "the test agent must be live in the registry")
	require.Equal(t, "allow", before, "positive control: the custom-agent seed allows read_file")

	state, err := agentstore.New(api.homePath).ReadState("test-agent")
	require.NoError(t, err)
	reloads := 0
	deps := &systools.Deps{
		Home:             api.homePath,
		ConfigPath:       filepath.Join(api.homePath, "config.json"),
		GetCfg:           al.GetConfig,
		MutateConfig:     al.MutateConfig,
		SaveConfigLocked: func(*config.Config) error { return nil },
		UpsertAgentFastFunc: sysagentFastUpsert(al, api.homePath, func() error {
			reloads++
			return nil
		}),
	}
	result := systools.NewAgentPermissionsTool(deps).Execute(context.Background(), map[string]any{
		"id": "test-agent", "revision": state.Revision,
		"tool_policy_changes": map[string]any{"set": map[string]any{"read_file": "deny"}},
	})
	require.False(t, result.IsError, "set_agent_permissions must succeed: %s", result.ForLLM)

	after, ok := al.ResolveRegisteredToolPolicy("test-agent", "read_file")
	require.True(t, ok)
	require.Equal(t, "deny", after, "the live registry must resolve the new policy on the next lookup, without a restart")
	require.Zero(t, reloads, "the fast publish path must succeed without falling back to a full reload")
}

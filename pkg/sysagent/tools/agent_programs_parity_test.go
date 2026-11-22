// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: the tool-level rows of the agent `programs` parity (FR-007):
// S-14 (set_agent_permissions sets programs after approval), S-15 (the
// existing self-change refusal covers programs too) and S-16 (update_agent
// refuses programs, naming set_agent_permissions). The REST rows live in
// pkg/gateway; the run-time rows with the env/git integration slices.
//
// Oracle: FR-007 ("set through ... set_agent_permissions.programs (self-
// change refused), refused by update_agent") and S-14–S-16's Then clauses.
// Expected values come from the spec, never from the tools.
//
// RED expectations: S-14 fails (programs is an unknown argument today);
// S-15 passes (the self-change refusal predates programs and must keep
// firing with programs in the args — a regression row); S-16 fails (the
// refusal does not name set_agent_permissions).

package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// s16ProgramsRefusal is S-16's oracle: the refusal names the one tool that
// may set programs.
const s16ProgramsRefusal = "set_agent_permissions"

func TestAgentPrograms_ParityRows(t *testing.T) {
	t.Run("S14_set_agent_permissions_sets_programs", func(t *testing.T) {
		deps, _ := newTestDeps(t)
		store := agentstore.New(deps.Home)
		require.NoError(t, store.Create("dev", &config.AgentConfig{ID: "dev", Name: "Dev"}))

		result := systools.NewAgentPermissionsTool(deps).Execute(context.Background(), map[string]any{
			"id":       "dev",
			"revision": currentAgentRevision(t, deps, "dev"),
			"programs": []any{"github"},
		})
		require.False(t, result.IsError,
			"S-14: programs is set through set_agent_permissions: %s", result.ForLLM)
		require.Contains(t, result.ForLLM, "programs", "the change reports the programs field")
	})

	t.Run("S15_self_change_refused_with_programs_too", func(t *testing.T) {
		deps, _ := newTestDeps(t)
		store := agentstore.New(deps.Home)
		require.NoError(t, store.Create("dev", &config.AgentConfig{ID: "dev", Name: "Dev"}))

		ctx := tools.WithAgentID(context.Background(), "dev")
		result := systools.NewAgentPermissionsTool(deps).Execute(ctx, map[string]any{
			"id":       "dev",
			"revision": currentAgentRevision(t, deps, "dev"),
			"programs": []any{"github"},
		})
		require.True(t, result.IsError, "the existing self-change refusal fires")
		require.Contains(t, result.ForLLM, "SELF_PERMISSION_CHANGE",
			"the refusal is the existing self-change refusal: %s", result.ForLLM)
		after, err := store.Get("dev")
		require.NoError(t, err)
		require.Nil(t, after.Tools, "unchanged (no capability write happened)")
	})

	t.Run("S16_update_agent_refuses_programs_naming_the_permissions_tool", func(t *testing.T) {
		deps, _ := newTestDeps(t)
		store := agentstore.New(deps.Home)
		require.NoError(t, store.Create("dev", &config.AgentConfig{ID: "dev", Name: "Dev"}))

		result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
			"id":       "dev",
			"revision": currentAgentRevision(t, deps, "dev"),
			"programs": []any{"github"},
		})
		require.True(t, result.IsError, "S-16: update_agent refuses a programs change")
		require.Contains(t, result.ForLLM, s16ProgramsRefusal,
			"the refusal names set_agent_permissions: %s", result.ForLLM)
		after, err := store.Get("dev")
		require.NoError(t, err)
		require.Equal(t, "Dev", after.Name, "unchanged")
	})
}

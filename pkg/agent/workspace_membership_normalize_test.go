// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// workspace_membership_normalize_test.go is a regression test for a bug
// escalated from pkg/gateway's identical fix
// (test_agent_loop_helper_test.go): testHarnessAgentIDs
// (test_helpers_test.go) used to seed ADR-046 P1 workspace CoreTeam
// membership under the RAW cfg.Agents.List[i].ID, but the real agent
// registry (registry.go:109) and AgentInstance (instance.go:160) both
// key/resolve every agent under routing.NormalizeAgentID(ID) — trimmed,
// lower-cased, sanitized. For a mixed-case config ID that mismatch meant
// workspace.FindForAgent's EXACT string-match CoreTeam lookup silently
// missed at real-turn time: resolveTurnWorkDirOrRefuse (the shared ADR-046
// P1 gate) refused the turn with only a WARN log ("turn refused: agent is
// not a member of any workspace"), never a test failure — so any test that
// believed it exercised a real turn for such an agent did not.
//
// This proves the fix end-to-end, through the exact path that was broken:
// mustNewAgentLoop -> ensureTestWorkspaceMembership -> testHarnessAgentIDs
// seeds the NORMALIZED id (not bypassing it the way registerAgent /
// seedTestWorkspaceMembershipForIDs-with-an-already-normalized-id do
// elsewhere in this package), and a real ProcessScheduled turn for the
// mixed-case-configured agent completes successfully instead of being
// silently refused.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestTestHarnessAgentIDs_MixedCaseAgent_NormalizedBeforeSeeding is the
// regression pin described above.
func TestTestHarnessAgentIDs_MixedCaseAgent_NormalizedBeforeSeeding(t *testing.T) {
	const rawAgentID = "MixedCaseAgent"
	normalized := routing.NormalizeAgentID(rawAgentID)
	require.NotEqual(t, rawAgentID, normalized,
		"sanity: the test id must actually need normalizing, or this test proves nothing")

	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	provider := testutil.NewScenario().WithText("scheduled turn ok")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              filepath.Join(home, "default-workspace"),
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: rawAgentID, Name: rawAgentID},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	// This is where the fix (and its loud guard) fires: mustNewAgentLoop ->
	// ensureTestWorkspaceMembership -> testHarnessAgentIDs(cfg). Before the
	// fix this call still succeeded (the bug is silent, not a panic/fatal
	// here) — it just seeded the wrong key.
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	// Sanity: the registry keys this agent under the normalized id (GetAgent
	// itself normalizes its lookup argument, per registry.go), and the
	// instance's own ID field is the normalized form too — never the raw
	// mixed-case config ID.
	agentInst, ok := al.registry.GetAgent(rawAgentID)
	require.True(t, ok, "agent must be registered")
	assert.Equal(t, normalized, agentInst.ID,
		"AgentInstance.ID must be the normalized form, per instance.go's NewAgentInstance")

	// The critical regression pin: workspace.FindForAgent's CoreTeam lookup is
	// an EXACT string match (pkg/workspace/find_for_agent.go) — check the
	// NORMALIZED id, the actual key resolveTurnWorkDirOrRefuse will look up at
	// real-turn time. Checking the RAW id here would be a tautology (nothing
	// in a correct seed ever writes the raw form once normalized).
	_, found := workspace.FindForAgent(home, normalized)
	require.True(t, found,
		"the normalized agent id must resolve to a workspace after mustNewAgentLoop's seeding — "+
			"if this fails, testHarnessAgentIDs has regressed back to seeding the raw config id")

	// The real proof: dispatch an ACTUAL turn as this agent (ProcessScheduled
	// owner-pins the run, bypassing routing/default-agent resolution) and
	// confirm it is NOT refused at the ADR-046 P1 gate. Pre-fix, this would
	// fail with an error wrapping ErrAgentNotWorkspaceMember — silently, with
	// only a WARN log in production (never a panic), which is exactly why the
	// bug could ship unnoticed.
	meta, err := al.GetSessionStore().NewScheduledSession(rawAgentID)
	require.NoError(t, err, "NewScheduledSession must succeed")

	reply, err := al.ProcessScheduled(
		context.Background(), rawAgentID, meta.ID, "do the thing", "scheduled", meta.ID,
	)
	require.NoError(t, err,
		"a real turn for a mixed-case-configured agent must not be silently refused as a workspace non-member")
	assert.Equal(t, "scheduled turn ok", reply)
}

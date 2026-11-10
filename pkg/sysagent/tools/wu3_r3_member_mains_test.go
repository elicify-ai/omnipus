package systools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mainsScope = "workspace record and delegation graph re-read from disk; graph validity and new edges' membership checked"

// CHECK R2-02 / pr-test R2-01 (mutant M17): a member main chat that cannot be
// prepared after a saved team change makes the outcome "saved but not
// working" with the provisioning failure as the reason — on create and update
// — and a successful provisioning is "working" (control).
func TestWU3MemberMainFailureIsSavedButNotWorking(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "mia", "jim")
	mains := &recordingMains{}
	deps.EnsureWorkspaceMains = mains.ensure

	ok := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{"name": "Fine", "core_team": []any{"mia"}})
	body := wu3Verdict(t, ok, "working", mainsScope, "a delegation between the members")
	assert.NotContains(t, body, "main_chat_warning")

	mains.err = errors.New("session storage is full")
	failed := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{"name": "Broken", "core_team": []any{"mia"}})
	body = wu3Verdict(t, failed, "saved but not working", mainsScope, "a delegation between the members")
	assert.Contains(t, body["reason"], "session storage is full")
	assert.Contains(t, body["main_chat_warning"], "session storage is full")

	wsID := wu3String(t, body["id"])
	state, err := workspace.ReadState(home, wsID)
	require.NoError(t, err)
	grown := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": wsID, "revision": state.Revision, "core_team": []any{"mia", "jim"},
	})
	body = wu3Verdict(t, grown, "saved but not working", mainsScope, "a delegation between the members")
	assert.Contains(t, body["reason"], "session storage is full")
	assert.Contains(t, body["main_chat_warning"], "session storage is full")
}

// code-reviewer R2-02: a team change whose read-back fails still provisions
// the members' mains (and reports a provisioning failure too); a later update
// that re-sends the same team repairs missing mains instead of returning a
// silent no-op. An update that does not name the team provisions nothing.
func TestWU3MemberMainsSurviveReadBackFailureAndSameTeamResendRepairs(t *testing.T) {
	deps, home := newTestDepsWithHomeAndAgents(t, "mia")
	mains := &recordingMains{err: errors.New("session storage is full")}
	deps.EnsureWorkspaceMains = mains.ensure

	systools.FailWorkspaceReadBackForTest(t)
	res := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{"name": "Research", "core_team": []any{"mia"}})
	body := wu3Outcome(t, res, "saved but not working")
	assert.Contains(t, body["reason"], "could not be read back")
	assert.Contains(t, body["reason"], "session storage is full", "the provisioning failure is reported as well")
	assert.Contains(t, body["main_chat_warning"], "session storage is full")
	require.Len(t, mains.calls, 1, "provisioning ran although the read-back failed")
	wsID := mains.calls[0]
	systools.RestoreWorkspaceReadBackForTest()

	state, err := workspace.ReadState(home, wsID)
	require.NoError(t, err)
	before := wu3FileInventory(t, home)
	resend := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": wsID, "revision": state.Revision, "core_team": []any{"mia"},
	})
	body = wu3Outcome(t, resend, "saved but not working")
	assert.Contains(t, body["main_chat_warning"], "session storage is full")
	assert.Equal(t, []string{wsID, wsID}, mains.calls, "re-sending the same team re-attempts the missing mains")
	assert.Equal(t, before, wu3FileInventory(t, home), "the re-send itself changes nothing on disk")

	mains.err = nil
	repaired := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": wsID, "revision": state.Revision, "core_team": []any{"mia"},
	})
	wu3Outcome(t, repaired, "working")
	assert.Len(t, mains.calls, 3)

	renamed := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": wsID, "revision": state.Revision, "name": "Research Lab",
	})
	require.False(t, renamed.IsError, renamed.ForLLM)
	assert.Len(t, mains.calls, 3, "an update that does not name the team provisions nothing")
}

// silent-failure-hunter W-R2-01: a native agent that is saved but not in the
// live roster (its activation failed or is pending) is admitted to a team, but
// the main provisioner only sees the live roster and skips it. That skip must
// be visible: "saved but not working" with a warning naming the agent.
func TestWU3InactiveNativeMemberMainIsReported(t *testing.T) {
	deps, _ := newTestDepsWithHomeAndAgents(t, "mia")
	mains := &recordingMains{}
	deps.EnsureWorkspaceMains = mains.ensure
	pending := &config.AgentConfig{ID: "pending-agent", Name: "Pending", Type: config.AgentTypeCustom}
	require.True(t, pending.IsChatTarget(), "fixture: a native chat-target agent")
	_, err := agentstore.New(deps.Home).CreateState(pending.ID, pending, "Help.")
	require.NoError(t, err)

	res := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Team", "core_team": []any{"mia", "pending-agent"},
	})
	body := wu3Outcome(t, res, "saved but not working")
	assert.Contains(t, body["main_chat_warning"], "pending-agent")
	assert.Contains(t, body["reason"], "pending-agent")
	assert.Equal(t, []any{"mia", "pending-agent"}, body["core_team"], "the team is saved")

	// Control: a live native member alone is "working".
	live := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{"name": "Live", "core_team": []any{"mia"}})
	wu3Outcome(t, live, "working")
}

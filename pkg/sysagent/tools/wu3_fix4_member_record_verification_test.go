package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK SF-R3-01 (silent-failure-hunter round 3, new finding): on a
// same-team update_workspace repair, a saved member whose persisted record is
// now corrupt or missing cannot be verified — but the read error must not be
// discarded. The repair must name that member in main_chat_warning and report
// outcome "saved but not working" with the verification issue in the reason,
// never "working". The workspace, its delegation graph and the unreadable
// record itself stay unchanged, and the other eligible members are still
// provisioned. Real agentstore/workspace reads throughout; recordingMains is
// only the provisioning boundary, modelling the production live-roster skip
// (no main for an id absent from the live roster, no error).
//
// Oracle: FR-002 (a member owns its main), FR-030 verdict vocabulary
// ("saved but not working"), SF-R3-01's required distinct reporting — an
// unreadable/missing record is not a deliberately skipped worker.
func TestWU3UnverifiableMemberRecordIsSavedButNotWorkingOnSameTeamResend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		broken func(t *testing.T, record string)
	}{
		{"corrupt member record", func(t *testing.T, record string) {
			require.NoError(t, os.WriteFile(record, []byte("{ this is not valid json"), 0o600))
		}},
		{"missing member record", func(t *testing.T, record string) {
			require.NoError(t, os.Remove(record))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, home := newTestDepsWithHomeAndAgents(t, "mia")
			mains := &recordingMains{}
			deps.EnsureWorkspaceMains = mains.ensure
			pending := &config.AgentConfig{ID: "pending-agent", Name: "Pending", Type: config.AgentTypeCustom}
			require.True(t, pending.IsChatTarget(), "fixture: a native chat-target agent")
			_, err := agentstore.New(home).CreateState(pending.ID, pending, "Help with the requested task.")
			require.NoError(t, err)

			// Control: while the record reads, the inactive member is named
			// and the save is "saved but not working".
			created := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(),
				map[string]any{"name": "Repair", "core_team": []any{"mia", "pending-agent"}})
			body := wu3Outcome(t, created, "saved but not working")
			assert.Contains(t, body["main_chat_warning"], "pending-agent", "a readable inactive member is named")
			wsID := wu3String(t, body["id"])
			require.Equal(t, []string{wsID}, mains.calls, "create provisions the team's workspace once")

			record := filepath.Join(home, "entities", "agents", "pending-agent.json")
			tc.broken(t, record)

			state, err := workspace.ReadState(home, wsID)
			require.NoError(t, err)
			before := wu3FileInventory(t, home)
			resend := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
				"id": wsID, "revision": state.Revision, "core_team": []any{"mia", "pending-agent"},
			})
			body = wu3Outcome(t, resend, "saved but not working")
			assert.Equal(t, false, body["changed"], "the repair itself writes nothing")
			warning, ok := body["main_chat_warning"].(string)
			require.True(t, ok, "the member whose record cannot be verified must be named in main_chat_warning: %s", resend.ForLLM)
			assert.Contains(t, warning, "pending-agent")
			reason, ok := body["reason"].(string)
			require.True(t, ok, "a saved-but-not-working result carries a reason: %s", resend.ForLLM)
			assert.Contains(t, reason, "pending-agent", "the reason names the member too")
			wu3fix4RequireReadOrVerifyContext(t, reason)
			assert.Equal(t, []any{"mia", "pending-agent"}, body["core_team"], "the team is unchanged")
			assert.Equal(t, []string{wsID, wsID}, mains.calls, "the repair still provisions the eligible members")
			assert.Equal(t, before, wu3FileInventory(t, home), "the workspace, its graph and the unreadable record stay unchanged")
		})
	}

	// Control: a readable inactive member keeps its named warning across the
	// same-team resend — the new verification must neither swallow it nor
	// turn the resend into "working".
	t.Run("readable inactive member still warns on the resend", func(t *testing.T) {
		deps, home := newTestDepsWithHomeAndAgents(t, "mia")
		mains := &recordingMains{}
		deps.EnsureWorkspaceMains = mains.ensure
		pending := &config.AgentConfig{ID: "pending-agent", Name: "Pending", Type: config.AgentTypeCustom}
		_, err := agentstore.New(home).CreateState(pending.ID, pending, "Help with the requested task.")
		require.NoError(t, err)
		created := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(),
			map[string]any{"name": "Steady", "core_team": []any{"mia", "pending-agent"}})
		body := wu3Outcome(t, created, "saved but not working")
		assert.Contains(t, body["main_chat_warning"], "pending-agent")
		wsID := wu3String(t, body["id"])
		state, err := workspace.ReadState(home, wsID)
		require.NoError(t, err)
		before := wu3FileInventory(t, home)
		resend := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
			"id": wsID, "revision": state.Revision, "core_team": []any{"mia", "pending-agent"},
		})
		body = wu3Outcome(t, resend, "saved but not working")
		assert.Contains(t, body["main_chat_warning"], "pending-agent", "the readable inactive member is still named")
		assert.Equal(t, before, wu3FileInventory(t, home), "the resend itself changes nothing on disk")
		assert.Equal(t, []string{wsID, wsID}, mains.calls, "the resend re-attempts provisioning")
	})

	// Control: a live member alone is "working" with no warning.
	t.Run("live member alone is working", func(t *testing.T) {
		deps, _ := newTestDepsWithHomeAndAgents(t, "mia")
		mains := &recordingMains{}
		deps.EnsureWorkspaceMains = mains.ensure
		created := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(),
			map[string]any{"name": "Live", "core_team": []any{"mia"}})
		body := wu3Outcome(t, created, "working")
		assert.NotContains(t, body, "main_chat_warning")
	})

	// Control: a deliberately non-chat worker missing from the live roster,
	// with a readable record, is intentionally ineligible — its readable
	// record proves it, so neither the create nor the same-team resend may
	// claim a failed main for it.
	t.Run("readable non-chat worker is not a failed main", func(t *testing.T) {
		deps, home := newTestDepsWithHomeAndAgents(t, "mia")
		mains := &recordingMains{}
		deps.EnsureWorkspaceMains = mains.ensure
		worker := &config.AgentConfig{ID: "task-worker", Name: "Task worker", Type: config.AgentTypeWorker}
		require.False(t, worker.IsChatTarget(), "fixture: a deliberately non-chat worker")
		_, err := agentstore.New(home).CreateState(worker.ID, worker, "Worker soul.")
		require.NoError(t, err)
		created := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(),
			map[string]any{"name": "Crew", "core_team": []any{"mia", "task-worker"}})
		body := wu3Outcome(t, created, "working")
		assert.NotContains(t, body, "main_chat_warning")
		wsID := wu3String(t, body["id"])
		state, err := workspace.ReadState(home, wsID)
		require.NoError(t, err)
		resend := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
			"id": wsID, "revision": state.Revision, "core_team": []any{"mia", "task-worker"},
		})
		body = wu3Outcome(t, resend, "working")
		assert.NotContains(t, body, "main_chat_warning", "a readable worker is intentionally ineligible, not a failed main")
	})
}

// wu3fix4RequireReadOrVerifyContext enforces the dispatch oracle "member +
// meaningful read/verify failure context" without pinning an exact error
// string: the report must say the member's record could not be read or
// verified — distinctly from the healthy "saved but not active" phrasing used
// when the record reads (that phrasing contains none of these markers).
func wu3fix4RequireReadOrVerifyContext(t *testing.T, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, marker := range []string{"read", "verif", "load", "parse", "corrupt", "not found", "no record", "record"} {
		if strings.Contains(lower, marker) {
			return
		}
	}
	t.Fatalf("the report must carry read/verify failure context for the member, got: %s", text)
}

//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t10NoticeFixture struct {
	al       *AgentLoop
	inbox    *session.MessageInboxStore
	task     *task.Task
	run      *activeRun
	main     map[string]string
	resolver *u6FakeMainResolver
}

func t10NoticeSetup(t *testing.T, creator, starter string, foreign bool) *t10NoticeFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, lc, inbox, _ := newDeliverTestLoop(t)
	te := al.taskExecutor
	te.SetLifecycleStore(lc)
	const ws = "t10-notice-workspace"
	members := []string{"worker", "starter"}
	if !foreign {
		members = append(members, "parent-agent")
	}
	provisionTestWorkspace(t, ws, members...)
	if foreign {
		provisionTestWorkspace(t, "t10-foreign", "parent-agent")
	}
	main := map[string]string{}
	resolver := &u6FakeMainResolver{elig: map[[2]string]string{}}
	for _, id := range []string{"worker", "parent-agent", "starter"} {
		w := ws
		if foreign && id == "parent-agent" {
			w = "t10-foreign"
		}
		sid, err := session.MainSessionID(w, id)
		require.NoError(t, err)
		main[id] = sid
		resolver.elig[[2]string{w, id}] = sid
	}
	te.SetMainSessionResolver(resolver)
	// A real task session and durable lifecycle edge are required by the real
	// deliverer; a nil-run onTaskComplete fixture cannot prove terminal delivery.
	child := "t10-notice-child"
	seedParentAndChild(t, lc, main["worker"], child)
	seedUnifiedSession(t, al, child)
	occurrence := int64(1793696400000)
	tk := &task.Task{ID: "t10-notice-task", Title: "T10 scheduled result", Prompt: "produce the report", AgentID: "worker", WorkspaceID: ws, Status: task.StatusInProgress, SessionID: child, CreatedBy: creator, Trigger: &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &occurrence}}}
	if creator != "" {
		require.NoError(t, te.store.CreateByAgent(tk, creator))
	} else {
		tk.CreatedBy = "parent-agent"
		require.NoError(t, te.store.Create(tk))
	} // Human username intentionally collides with an agent ID.
	recipients := te.captureRunRecipients(context.Background(), tk, &session.InitiatedBy{AgentID: starter})
	run := te.openRun(tk.ID, &occurrence, task.RunKindScheduled, child, recipients...)
	require.NotNil(t, run, "real stored run must open")
	require.LessOrEqual(t, len(run.recipients), 2)
	return &t10NoticeFixture{al, inbox, tk, run, main, resolver}
}
func (f *t10NoticeFixture) drain(t *testing.T, recipient string) []string {
	t.Helper()
	messages, _, _, err := f.inbox.Drain(recipient, "", "", 20)
	require.NoError(t, err)
	out := []string{}
	for _, m := range messages {
		if hb, e := m.AsSessionMessageHandback(); e == nil && hb.ResultSoFar != "" {
			out = append(out, hb.ResultSoFar)
			continue
		}
		failure, e := m.AsSessionMessageError()
		require.NoError(t, e)
		out = append(out, failure.Text)
	}
	return out
}
func (f *t10NoticeFixture) assertStoredRun(t *testing.T, status task.Status, result string) {
	t.Helper()
	rows, err := f.al.taskStore.ListRuns(f.task.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, f.run.runID, rows[0].RunID)
	assert.Equal(t, status, rows[0].Status)
	assert.Equal(t, result, rows[0].Result)
	assert.ElementsMatch(t, f.run.recipients, rows[0].RecipientSessionIDs, "creator fan-out must NEVER change captured recipients")
	assert.LessOrEqual(t, len(rows[0].RecipientSessionIDs), 2)
}

// Traces to: schedules-t10-spec-r6.md lines 1044–1049, 1645, 1651; TDD 20 stage 1.
// PASSING baseline is mandatory. This uses REAL OpenRun + CAS completion +
// stored run + real upward deliverer/inbox, not direct nil-run completion.
func TestTaskTerminalNotice_CharacterizationBeforeCreatorDelta(t *testing.T) {
	for _, tc := range []struct {
		name         string
		success      bool
		conflict     string
		want         task.Status
		word, result string
	}{
		{"done", true, "", task.StatusDone, "done", "complete result A"},
		{"failed", false, "", task.StatusFailed, "failed", "failure result B"},
		{"stop-wins-over-late-success", true, "terminal", task.StatusFailed, "stopped", "[reason:stopped_by_user] Cancelled by tester via Stop."},
		{"nonterminal-conflict-drops-notice", true, "nonterminal", task.StatusDone, "", "late success must not be delivered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := t10NoticeSetup(t, "parent-agent", "starter", false)
			assert.ElementsMatch(t, []string{f.main["starter"], f.main["worker"]}, f.run.recipients)
			assert.NotContains(t, f.run.recipients, f.main["parent-agent"])
			if tc.conflict == "terminal" {
				simulateConcurrentStop(t, f.al.taskStore, f.task.ID)
			}
			if tc.conflict == "nonterminal" {
				status := task.StatusNext
				_, err := f.al.taskStore.Update(f.task.ID, task.Patch{Status: &status})
				require.NoError(t, err)
			}
			completion := "late success must not be delivered"
			if tc.conflict == "" {
				completion = tc.result
			}
			applied := f.al.taskExecutor.completeTaskWithResult(f.task, f.task.SessionID, task.StatusInProgress, tc.success, completion, f.run)
			assert.Equal(t, tc.conflict == "", applied)
			for _, recipient := range f.run.recipients {
				notices := f.drain(t, recipient)
				if tc.word == "" {
					assert.Empty(t, notices)
					continue
				}
				assert.Equal(t, []string{fmt.Sprintf("Task run %s: %q (run %s)\n\n%s", tc.word, f.task.Title, f.run.runID, tc.result)}, notices)
			}
			f.assertStoredRun(t, tc.want, tc.result)
			current, err := f.al.taskStore.Get(f.task.ID)
			require.NoError(t, err)
			if tc.conflict == "nonterminal" {
				assert.Equal(t, task.StatusNext, current.Status)
				assert.Empty(t, current.Result)
			} else {
				assert.Equal(t, tc.want, current.Status)
				assert.Equal(t, tc.result, current.Result)
			}
		})
	}
}

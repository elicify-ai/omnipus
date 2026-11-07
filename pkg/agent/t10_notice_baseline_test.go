//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Characterization required by TDD 20: run the real overlap/skip path.
func t10SkippedNoticeBaseline(t *testing.T) {
	f := t10NoticeSetup(t, "parent-agent", "starter", false)
	s := NewTaskTriggerScheduler(cron.NewCronService(filepath.Join(t.TempDir(), "cron", "jobs.json")), f.al.taskStore, f.al.taskExecutor)
	clk := newTriggerFakeClock()
	s.SetClock(clk)
	require.NoError(t, s.Start())
	defer func() { s.Stop(); s.WaitForLane() }()
	at := clk.Now().Truncate(time.Second).Add(time.Hour).UnixMilli()
	rule, tz := "FREQ=DAILY;COUNT=3", "UTC"
	trigger := &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &at, Tz: &tz}}
	fresh, err := f.al.taskStore.Update(f.task.ID, task.Patch{Trigger: &trigger})
	require.NoError(t, err)
	s.OnTaskUpserted(fresh)
	clk.Advance(time.Hour)
	s.RunDueJobs(clk.Now())
	s.WaitForLane()
	rows, err := f.al.taskStore.ListRuns(f.task.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2, "positive control: real scheduler recorded an overlap skip, not an unexecuted callback")
	skips := 0
	for _, r := range rows {
		if r.Status == task.StatusSkipped {
			skips++
			assert.Equal(t, &at, r.OccurrenceMs)
			assert.Empty(t, r.SessionID)
		}
	}
	assert.Equal(t, 1, skips)
	for _, address := range f.main {
		assert.Empty(t, f.drain(t, address), "skipped occurrence never delivers a completion notice")
	}
}

type t10NoticeFixture struct {
	al       *AgentLoop
	inbox    *session.MessageInboxStore
	task     *task.Task
	run      *activeRun
	main     map[string]string
	resolver *u6FakeMainResolver
}

func t10NoticeSetup(t *testing.T, creator, starter string, foreign bool, noRun ...bool) *t10NoticeFixture {
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
	var run *activeRun
	if len(noRun) == 0 || !noRun[0] {
		run = te.openRun(tk.ID, &occurrence, task.RunKindScheduled, child, recipients...)
		require.NotNil(t, run, "real stored run must open")
		require.LessOrEqual(t, len(run.recipients), 2)
	}
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
	t.Run("nil-run-terminal-callback-delivers-nothing", func(t *testing.T) {
		f := t10NoticeSetup(t, "parent-agent", "starter", false, true)
		assert.True(t, f.al.taskExecutor.completeTaskWithResult(f.task, f.task.SessionID, task.StatusInProgress, true, "nil-run result", nil))
		current, err := f.al.taskStore.Get(f.task.ID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusDone, current.Status)
		assert.Equal(t, "nil-run result", current.Result, "positive control: callback actually completed the task")
		rows, err := f.al.taskStore.ListRuns(f.task.ID)
		require.NoError(t, err)
		assert.Empty(t, rows)
		for _, address := range f.main {
			assert.Empty(t, f.drain(t, address), "nil-run callback owes no result notice")
		}
	})
	t.Run("real-skipped-occurrence-delivers-nothing", t10SkippedNoticeBaseline)
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

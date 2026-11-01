//go:build goolm && stdjson

package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 994–1055; TDD 22 stage 2, D-7.1–6/9/10/12.
// The separate stage-1 characterization must pass before this delta is judged.
// Every row uses the REAL persisted run, CAS completion and inbox-then-wake path.
func TestT10_CreatorNoticeFanOutAtRealRunCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, creator, starter string
		foreign, failed        bool
		conflict               string
		unresolvable           bool
		copies                 int
	}{
		{name: "D7-1-same-workspace-success", creator: "parent-agent", starter: "starter", copies: 1},
		{name: "D7-2-creator-is-assignee", creator: "worker", starter: "starter", copies: 1},
		{name: "D7-3-creator-is-starter", creator: "parent-agent", starter: "parent-agent", copies: 1},
		{name: "D7-4-person-username-collides-with-agent", creator: "", starter: "starter", copies: 0},
		{name: "D7-5-foreign-creator-no-push", creator: "parent-agent", starter: "worker", foreign: true, copies: 0},
		{name: "D7-6-failed-run", creator: "parent-agent", starter: "starter", failed: true, copies: 1},
		{name: "D7-9-authoritative-stopped-not-late-success", creator: "parent-agent", starter: "starter", conflict: "terminal", copies: 1},
		{name: "D7-10-nonterminal-conflict", creator: "parent-agent", starter: "starter", conflict: "nonterminal", copies: 0},
		{name: "D7-12-unresolvable-creator-main", creator: "parent-agent", starter: "starter", unresolvable: true, copies: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := t10NoticeSetup(t, tc.creator, tc.starter, tc.foreign)
			captured := append([]string{}, f.run.recipients...)
			if tc.creator != "worker" && tc.creator != tc.starter {
				assert.NotContains(t, captured, f.main["parent-agent"], "creator is never a third captured recipient")
			}
			losses := taskNoticeLosses.Load()
			if tc.unresolvable {
				f.resolver.errs = map[[2]string]error{{f.task.WorkspaceID, "parent-agent"}: errors.New("fixture creator-main read failure")}
			}
			expected, statusWord, completion := "full T10 result: alpha\nbeta", "done", "full T10 result: alpha\nbeta"
			status := task.StatusDone
			if tc.failed {
				status = task.StatusFailed
				statusWord = "failed"
				expected = "specific failure: report could not finish"
				completion = expected
			}
			if tc.conflict == "terminal" {
				simulateConcurrentStop(t, f.al.taskStore, f.task.ID)
				status = task.StatusFailed
				statusWord = "stopped"
				expected = "[reason:stopped_by_user] Cancelled by tester via Stop."
				completion = "REJECTED LATE SUCCESS"
			}
			if tc.conflict == "nonterminal" {
				next := task.StatusNext
				_, err := f.al.taskStore.Update(f.task.ID, task.Patch{Status: &next})
				require.NoError(t, err)
			}
			applied := f.al.taskExecutor.completeTaskWithResult(f.task, f.task.SessionID, task.StatusInProgress, !tc.failed, completion, f.run)
			assert.Equal(t, tc.conflict == "", applied)
			want := fmt.Sprintf("Task run %s: %q (run %s)\n\n%s", statusWord, f.task.Title, f.run.runID, expected)
			// Gather all distinct recipient addresses before asserting, so a failure
			// does not hide leakage to a different creator/starter/assignee address.
			got := map[string][]string{}
			for _, address := range f.main {
				if _, seen := got[address]; !seen {
					got[address] = f.drain(t, address)
				}
			}
			if tc.conflict == "nonterminal" {
				for _, notices := range got {
					assert.Empty(t, notices)
				}
				return
			}
			for _, address := range captured {
				assert.Equal(t, []string{want}, got[address], "captured recipients retain full authoritative content")
			}
			creatorAddress := f.main["parent-agent"]
			if tc.creator == "worker" {
				creatorAddress = f.main["worker"]
			}
			if tc.copies == 1 {
				assert.Equal(t, []string{want}, got[creatorAddress], "creator copy must equal the assignee's FULL notice exactly once")
			} else if tc.creator != tc.starter && tc.creator != "worker" {
				assert.Empty(t, got[creatorAddress], "no creator push or result content across the boundary")
			}
			f.assertStoredRun(t, status, expected)
			assert.Equal(t, captured, f.run.recipients)
			if tc.unresolvable {
				assert.Equal(t, losses+1, taskNoticeLosses.Load(), "owed creator notice loss must be recorded, not silently dropped")
				transcript, err := f.al.GetSessionStore().ReadTranscript(f.task.SessionID)
				require.NoError(t, err)
				assert.Contains(t, fmt.Sprint(transcript), "could not be delivered", "same visible notice-loss mechanism as captured recipients")
			}
			// An already-closed terminal signal re-enters the real CAS-conflict branch;
			// deterministic delivery identity must prevent a second notice anywhere.
			f.al.taskExecutor.completeTaskWithResult(f.task, f.task.SessionID, task.StatusInProgress, true, "ANOTHER REJECTED OUTCOME", f.run)
			for address, before := range got {
				// Drain is a non-destructive read of unacked messages. Comparing
				// the complete snapshot catches additional notices without falsely
				// treating the original unacked notice as a duplicate delivery.
				assert.Equal(t, before, f.drain(t, address), "completion retry must not add or replace any terminal notice")
			}
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 1032–1042, 1254–1258; TDD 22, D-7.7/8/11.
// These need the missing T8 scheduled fire/release backend, not a nil-run shortcut.
func TestT10_CreatorNoticeScheduledLifecycleDependencies(t *testing.T) {
	for _, tc := range []struct{ id, oracle string }{
		{"D7-7-skipped-no-run", "actual skipped occurrence opens no run and produces no recipient or creator notice"},
		{"D7-8-delivered-held-then-dispatched", "no notice while held; after dependency release and actual completion, same creator notice once"},
		{"D7-11-run-now-consumption", "real trigger-aware run-now commits at admission then completes and fans out once"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			switch tc.id {
			case "D7-7-skipped-no-run":
				t10SkippedNoticeBaseline(t)
			case "D7-8-delivered-held-then-dispatched":
				t10CreatorScheduledLifecycle(t, false)
			case "D7-11-run-now-consumption":
				t10CreatorScheduledLifecycle(t, true)
			default:
				t.Fatalf("unbound creator lifecycle %s", tc.id)
			}
		})
	}
}

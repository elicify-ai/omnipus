// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// failGoalRecordListReads exercises a real directory-scan failure, not a fake
// goal accessor. Removing the directory is insufficient: entity.Store.List
// treats a missing directory as an empty store. Corrupt JSON is also
// insufficient: List skips individual unreadable records. A regular file in
// place of the directory fails the scan independently of permission bits.
// Preserve the original directory so recovery can prove the goal still exists.
func failGoalRecordListReads(t *testing.T, store *goal.Store) (restore func(), cause *os.PathError) {
	t.Helper()
	dir := store.Dir()
	savedDir := dir + "-saved-for-read-fault"
	if err := os.Rename(dir, savedDir); err != nil {
		t.Fatalf("arrange goal read fault: preserve directory %q: %v", dir, err)
	}

	restored := false
	restore = func() {
		if restored {
			return
		}
		if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("restore goal read fault: remove replacement %q: %v", dir, err)
		}
		if err := os.Rename(savedDir, dir); err != nil {
			t.Fatalf("restore goal read fault: restore directory %q: %v", dir, err)
		}
		restored = true
	}
	t.Cleanup(restore)
	if err := os.WriteFile(dir, []byte("a regular file, not the goals directory\n"), 0o600); err != nil {
		t.Fatalf("arrange goal read fault: replace directory %q with file: %v", dir, err)
	}

	// Derive the platform's filesystem cause at the process edge. The oracle
	// is that this cause reaches the caller, not a hard-coded POSIX errno.
	_, edgeErr := os.ReadDir(dir)
	if !errors.As(edgeErr, &cause) || cause.Path != dir || cause.Err == nil {
		t.Fatalf("arrange goal read fault: ReadDir(%q) = %v, want a PathError on this exact directory", dir, edgeErr)
	}
	if errors.Is(edgeErr, os.ErrNotExist) || errors.Is(edgeErr, os.ErrPermission) {
		t.Fatalf("arrange goal read fault: got missing-path/permission error %v, want the file-in-place-of-directory fault", edgeErr)
	}

	// Reconstruct the concrete store just as the production lookup does.
	// Prove ListActive itself errors with the same path and cause before
	// testing any downstream result. A cached/empty success fails here.
	_, listErr := goal.NewStore(config.OmnipusHomeDir()).ListActive()
	var listPathErr *os.PathError
	if !errors.As(listErr, &listPathErr) || listPathErr.Path != dir || !errors.Is(listErr, cause.Err) {
		t.Fatalf("arrange goal read fault: real ListActive() = %v, want the actual directory-read failure %v", listErr, cause)
	}
	t.Logf("real ListActive read fault proven: %v", listErr)
	return restore, cause
}

// TestGoalRecordLookupReadFailureIsVisibleToClaimingAgent traces to the
// goal-record-read-error-visible RED dispatch: a failed goal read must reach
// the ultimate caller as a read failure, never as a legitimate no-goal result.
// Observe the registered goal_claim tool, not activeGoalForSession's signature.
// The no-goal control follows GoalClaimTool.Description's scope contract; the
// readable control follows its documented status/evidence/goal_id payload.
// Fault and recovery use the real session, concrete store and production wiring.
// Only the unused external model is replaced by a fail-on-call provider.
func TestGoalRecordLookupReadFailureIsVisibleToClaimingAgent(t *testing.T) {
	cases := []struct {
		name    string
		hasGoal bool
		fault   bool
	}{
		{name: "genuine_absence_is_reported_as_no_active_goal"},
		{name: "readable_active_goal_returns_the_submitted_claim", hasGoal: true},
		{name: "real_read_failure_is_not_reported_as_no_active_goal", hasGoal: true, fault: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newGoalLoopTestLoop(t, &noCallProvider{t: t}, nil)
			agentInst, ok := al.GetRegistry().GetAgent("native-agent")
			if !ok || agentInst == nil {
				t.Fatal("arrange: native agent is not registered")
			}
			_, sid := newGoalTestSession(t, al, agentInst.ID)
			claimTool, ok := agentInst.Tools.Get(tools.GoalClaimToolName)
			if !ok || claimTool == nil {
				t.Fatal("BLOCKED: registered goal_claim not implemented — required by the visible-read-failure RED dispatch")
			}
			ctx := tools.WithTranscriptSessionID(context.Background(), sid)
			const evidence = "verified the requested work"
			args := map[string]any{"status": tools.GoalClaimStatusMet, "evidence": evidence}
			store := goal.NewStore(config.OmnipusHomeDir())

			if !tc.hasGoal {
				active, err := store.ListActive()
				if err != nil || len(active) != 0 {
					t.Fatalf("arrange genuine absence: ListActive() = %v, %v; want an empty readable store", active, err)
				}
				result := claimTool.Execute(ctx, args)
				if result == nil || !result.IsError || !strings.Contains(strings.ToLower(result.ForLLM), "no active goal") {
					t.Fatalf("genuine absence: goal_claim result = %+v, want a no-active-goal refusal", result)
				}
				return
			}

			seeded := seedActiveGoalRecord(t, sid, "finish the requested work", nil, nil)
			before, err := store.GetActiveByOwner(generated.GoalOwnerKindSession, sid)
			if err != nil || before == nil || before.GoalID != seeded.GoalID || before.ActiveSessionID != sid {
				t.Fatalf("arrange active goal: GetActiveByOwner(%q) = %+v, %v; want seeded goal %q bound to this session", sid, before, err, seeded.GoalID)
			}

			if !tc.fault {
				result := claimTool.Execute(ctx, args)
				if result == nil || result.IsError {
					t.Fatalf("readable active goal: goal_claim result = %+v, want the submitted claim", result)
				}
				var got map[string]string
				if err := json.Unmarshal([]byte(result.ForLLM), &got); err != nil {
					t.Fatalf("readable active goal: decode claim payload %q: %v", result.ForLLM, err)
				}
				want := map[string]string{"status": tools.GoalClaimStatusMet, "evidence": evidence, "goal_id": seeded.GoalID}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("readable active goal: claim payload = %#v, want %#v", got, want)
				}
				return
			}

			restore, readCause := failGoalRecordListReads(t, store)
			result := claimTool.Execute(ctx, args)
			restore()
			after, err := store.GetActiveByOwner(generated.GoalOwnerKindSession, sid)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("recovery: original active goal changed or vanished after the read failure: before=%+v after=%+v error=%v", before, after, err)
			}
			t.Logf("recovery proven: original goal %q remains active and unchanged", seeded.GoalID)

			if result == nil {
				t.Fatalf("goal_claim returned no result for real ListActive read failure %v; want a visible read error", readCause)
			}
			if !result.IsError {
				t.Errorf("goal_claim reported success for real ListActive read failure %v: %+v", readCause, result)
			}
			var returnedCause *os.PathError
			if !errors.As(result.Err, &returnedCause) || returnedCause.Path != readCause.Path || !errors.Is(result.Err, readCause.Err) {
				t.Errorf("goal_claim did not preserve real ListActive read failure %v: Err=%v, ForLLM=%q; want the directory-read cause, not no-goal success", readCause, result.Err, result.ForLLM)
			}
			if strings.Contains(strings.ToLower(result.ForLLM), "no active goal") {
				t.Errorf("goal_claim falsely told the agent there is no active goal: %q; the real read failure is %v and goal %q still exists", result.ForLLM, readCause, seeded.GoalID)
			}
			if !strings.Contains(result.ForLLM, readCause.Err.Error()) {
				t.Errorf("goal_claim did not expose the read-failure cause to the agent: ForLLM=%q; want the actual filesystem cause %q from %v", result.ForLLM, readCause.Err.Error(), readCause)
			}
		})
	}
}

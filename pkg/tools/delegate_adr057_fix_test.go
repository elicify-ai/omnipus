// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FIX-6 (parallel fix wave, feature/plan-swimlane-board) — regression
// coverage for three defects found by review in pkg/tools/delegate.go:
//
//   - Defect 1 (BLOCKER): executeCancel reported unconditional success even
//     when killChildBackgroundShells found a real kill failure — both
//     counts were discarded after a log line. Fixed by returning
//     (killed, failed) from killChildBackgroundShells and folding a real
//     failure into the result message.
//   - Defect 2 (MAJOR): taskCap() was dead code (zero references outside
//     its own definition) — evictStaleTasksLocked enforced only TTL, so
//     SetTaskRetentionPolicy's cap argument had no effect at any value.
//     Compounding: listTaskCopies mutated the STORED record's
//     LastStatusRead on every bare list-all read, refreshing the eviction
//     clock on every task in the map (not just the one(s) the caller
//     cared about) and starving eviction indefinitely.
//   - Defect 3 (MAJOR): verifyCallerOwnsSession collapsed every ancestor
//     Load error into the same silent chain-end as the expected not-found
//     case, so a genuine I/O error (corrupt/truncated record, disk-full
//     partial write) was logged nowhere and surfaced only as an
//     indistinguishable "not owned by the calling session" denial.
//
// Per Rule 5/6 (see delegate_adr057_test.go's own note): this is a NEW
// file, and its unexported helpers/types are fix6-prefixed so a same-wave
// package-level name never collides with another concurrently-added file
// in pkg/tools.

package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// ---------------------------------------------------------------------
// Defect 1: executeCancel must surface a real background-shell kill
// failure instead of reporting unconditional success.
// ---------------------------------------------------------------------

// fix6NewLifecycleRecord builds a minimal, valid, non-terminal
// LifecycleRecord for a child session owned by parentDurableKey — the same
// shape delegate_adr057_unix_test.go's real-process precedent uses.
func fix6NewLifecycleRecord(sessionID, parentDurableKey string) *session.LifecycleRecord {
	return &session.LifecycleRecord{
		SessionID:        sessionID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeHuman,
		ParentDurableKey: parentDurableKey,
		WorkspaceID:      "ws-1",
		AgentID:          "worker",
	}
}

// TestKillChildBackgroundShells_ReturnsRealOutcome forces the real
// KillAndRelabel failure branch via the killProcessGroupFn test seam
// (session_kill_all_for_session_test.go's own precedent — a real fake PID
// always succeeds via ESRCH-as-success, so there is no way to reach a
// genuine kill failure with a real syscall in this sandbox). Before the
// fix, killChildBackgroundShells had no return value at all: both killed
// and failed were discarded after an internal log line.
func TestKillChildBackgroundShells_ReturnsRealOutcome(t *testing.T) {
	t.Run("forced kill failure: returns (0, 1), not silence", func(t *testing.T) {
		origKillFn := killProcessGroupFn
		t.Cleanup(func() { killProcessGroupFn = origKillFn })
		wantErr := errors.New("fix6: forced kill failure for test")
		killProcessGroupFn = func(pid int) error { return wantErr }

		sm := NewSessionManager()
		const ownerSessionID = "fix6-shell-owner-fail"
		sm.Add(&ProcessSession{
			ID: "fix6-shell-1", OwnerSessionID: ownerSessionID, Status: StatusRunning,
			PID: fakeUnusedPIDBase + 901, StartTime: time.Now().Unix(),
		})

		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionManager(sm)

		killed, failed, walkIncomplete := tool.killChildBackgroundShells(ownerSessionID)
		if failed != 1 {
			t.Errorf("BLOCKER (defect 1): expected the real kill failure to be returned as failed=1, got %d — "+
				"previously both counts were discarded after a log line", failed)
		}
		if killed != 0 {
			t.Errorf("expected killed=0 alongside the failure, got %d", killed)
		}
		if walkIncomplete {
			t.Errorf("expected walkIncomplete=false — no lifecycle store is configured on this tool, " +
				"which is the documented degrade-gracefully (not error) path")
		}
	})

	t.Run("positive control: a real successful kill returns (1, 0)", func(t *testing.T) {
		sm := NewSessionManager()
		const ownerSessionID = "fix6-shell-owner-ok"
		// killProcessGroupFn is NOT overridden here — a real fake PID far
		// outside any range this sandbox allocates succeeds via
		// ESRCH-as-success (killProcessGroup's own doc comment), proving
		// this isn't a function that always reports failure regardless of
		// outcome.
		sm.Add(&ProcessSession{
			ID: "fix6-shell-2", OwnerSessionID: ownerSessionID, Status: StatusRunning,
			PID: fakeUnusedPIDBase + 902, StartTime: time.Now().Unix(),
		})

		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionManager(sm)

		killed, failed, walkIncomplete := tool.killChildBackgroundShells(ownerSessionID)
		if killed != 1 {
			t.Errorf("expected killed=1 for a clean kill, got %d", killed)
		}
		if failed != 0 {
			t.Errorf("expected failed=0 for a clean kill, got %d", failed)
		}
		if walkIncomplete {
			t.Errorf("expected walkIncomplete=false — no lifecycle store is configured on this tool, " +
				"which is the documented degrade-gracefully (not error) path")
		}
	})
}

// TestDelegateCancel_SurfacesBackgroundShellKillFailure is the end-to-end
// proof for the BLOCKER: a delegate action="cancel" call whose turn-level
// cancel succeeds but whose background-shell kill genuinely fails must NOT
// return the same unconditional success text as a clean cancel.
func TestDelegateCancel_SurfacesBackgroundShellKillFailure(t *testing.T) {
	t.Run("hard cancel: turn cancel succeeds, shell kill fails -> result carries the warning", func(t *testing.T) {
		origKillFn := killProcessGroupFn
		t.Cleanup(func() { killProcessGroupFn = origKillFn })
		wantErr := errors.New("fix6: forced kill failure for test")
		killProcessGroupFn = func(pid int) error { return wantErr }

		sm := NewSessionManager()
		const childID = "fix6-cancel-child-fail"
		sm.Add(&ProcessSession{
			ID: "fix6-shell-3", OwnerSessionID: childID, Status: StatusRunning,
			PID: fakeUnusedPIDBase + 903, StartTime: time.Now().Unix(),
		})

		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionMessagingEnabled(func() bool { return true })
		tool.SetSessionManager(sm)
		lc := session.NewLifecycleStore(t.TempDir())
		tool.SetLifecycleStore(lc)
		if err := lc.Persist(fix6NewLifecycleRecord(childID, "fix6-cancel-parent-fail")); err != nil {
			t.Fatalf("seed lifecycle record failed: %v", err)
		}
		tool.SetCancelHooks(
			func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
			func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
		)

		callerCtx := WithTranscriptSessionID(context.Background(), "fix6-cancel-parent-fail")
		result := tool.Execute(callerCtx, map[string]any{"action": "cancel", "session_id": childID, "hard": true})
		if result.IsError {
			t.Fatalf("expected the turn-level hard cancel itself to still succeed (independent of the "+
				"shell-kill failure), got error result: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "hard-cancelled immediately") {
			t.Errorf("expected the turn-cancel success text to still be present, got: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "could not be killed") {
			t.Errorf("BLOCKER (defect 1): expected the cancel result to surface the background-shell kill "+
				"failure instead of reporting unconditional success, got: %s", result.ForLLM)
		}
	})

	t.Run("soft cancel: shell kill fails -> result carries the warning", func(t *testing.T) {
		origKillFn := killProcessGroupFn
		t.Cleanup(func() { killProcessGroupFn = origKillFn })
		wantErr := errors.New("fix6: forced kill failure for test")
		killProcessGroupFn = func(pid int) error { return wantErr }

		sm := NewSessionManager()
		const childID = "fix6-cancel-child-fail-soft"
		sm.Add(&ProcessSession{
			ID: "fix6-shell-4", OwnerSessionID: childID, Status: StatusRunning,
			PID: fakeUnusedPIDBase + 904, StartTime: time.Now().Unix(),
		})

		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionMessagingEnabled(func() bool { return true })
		tool.SetSessionManager(sm)
		lc := session.NewLifecycleStore(t.TempDir())
		tool.SetLifecycleStore(lc)
		if err := lc.Persist(fix6NewLifecycleRecord(childID, "fix6-cancel-parent-fail-soft")); err != nil {
			t.Fatalf("seed lifecycle record failed: %v", err)
		}
		tool.SetCancelHooks(
			func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
			func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
		)

		callerCtx := WithTranscriptSessionID(context.Background(), "fix6-cancel-parent-fail-soft")
		result := tool.Execute(callerCtx, map[string]any{"action": "cancel", "session_id": childID, "hard": false})
		if result.IsError {
			t.Fatalf("expected the turn-level soft cancel itself to still succeed, got error result: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "cooperatively cancelled") {
			t.Errorf("expected the turn-cancel success text to still be present, got: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "could not be killed") {
			t.Errorf("BLOCKER (defect 1): expected the cancel result to surface the background-shell kill "+
				"failure instead of reporting unconditional success, got: %s", result.ForLLM)
		}
	})

	// Positive control (Rule 4): the two subtests above prove the warning
	// fires on a real failure; this proves it does NOT fire when there is
	// nothing to kill — otherwise the warning could be an unconditional
	// suffix that happens to look conditional.
	t.Run("nothing to kill: no false warning is added", func(t *testing.T) {
		sm := NewSessionManager() // no ProcessSession registered under this owner at all

		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetSessionMessagingEnabled(func() bool { return true })
		tool.SetSessionManager(sm)
		lc := session.NewLifecycleStore(t.TempDir())
		tool.SetLifecycleStore(lc)
		const childID = "fix6-cancel-child-clean"
		if err := lc.Persist(fix6NewLifecycleRecord(childID, "fix6-cancel-parent-clean")); err != nil {
			t.Fatalf("seed lifecycle record failed: %v", err)
		}
		tool.SetCancelHooks(
			func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
			func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
		)

		callerCtx := WithTranscriptSessionID(context.Background(), "fix6-cancel-parent-clean")
		result := tool.Execute(callerCtx, map[string]any{"action": "cancel", "session_id": childID, "hard": true})
		if result.IsError {
			t.Fatalf("expected success, got error result: %s", result.ForLLM)
		}
		if strings.Contains(result.ForLLM, "could not be killed") {
			t.Errorf("must NOT add a kill-failure warning when there was nothing to kill, got: %s", result.ForLLM)
		}
	})
}

// ---------------------------------------------------------------------
// Defect 2: taskCap must actually bound t.tasks/t.sessionIndex, and
// listTaskCopies must not mutate stored state.
// ---------------------------------------------------------------------

// TestEvictStaleTasksLocked_CapEnforced_IndependentOfTTL isolates FR-087
// (the retention cap) from FR-045 (the TTL) — the pre-existing
// TestDelegateTaskMaps_BoundedAfterNCompletions (delegate_adr057_test.go)
// ages every task past its TTL before asserting the bound, so it could
// pass even with taskCap() completely unreferenced (as it, in fact, was —
// golangci-lint flagged it `unused`). Here the TTL is set to an hour and
// the whole test completes in a couple of simulated minutes, so ANY bound
// this test observes can only come from cap enforcement.
func TestEvictStaleTasksLocked_CapEnforced_IndependentOfTTL(t *testing.T) {
	tool, _ := u14PermissiveTool(t)
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)

	fakeNow := time.Now()
	tool.SetClock(func() time.Time { return fakeNow })

	const capN = 3
	tool.SetTaskRetentionPolicy(capN, time.Hour) // TTL far larger than this test's simulated span

	const n = 5 // n > capN
	for i := 0; i < n; i++ {
		ctx := WithAgentID(WithTranscriptSessionID(context.Background(), fmt.Sprintf("fix6-cap-parent-%d", i)), "fix6-agent")
		if r := tool.Execute(ctx, map[string]any{"task": "old", "async": false}); r.IsError {
			t.Fatalf("dispatch %d failed: %s", i, r.ForLLM)
		}
	}

	tool.mu.Lock()
	var sessionIDs []string
	for sid, taskID := range tool.sessionIndex {
		if st, ok := tool.tasks[taskID]; ok && st.Task == "old" {
			sessionIDs = append(sessionIDs, sid)
		}
	}
	beforeCount := len(tool.tasks)
	tool.mu.Unlock()
	if len(sessionIDs) == 0 {
		t.Fatal("precondition: expected at least one 'old' task to still be registered")
	}
	// Positive lower bound (Rule 4): registration alone (no TTL elapsed at
	// all — every task's LastStatusRead is still fresh) must already have
	// pushed the map past the cap, or the bounded assertion below would
	// pass vacuously even with cap enforcement still dead code.
	if beforeCount <= capN {
		t.Fatalf("precondition: expected t.tasks to have grown past the cap (%d) via registration alone "+
			"(TTL never elapsed), got %d — the fixture does not exercise the cap at all", capN, beforeCount)
	}

	// Actively poll ONE surviving task, giving it a strictly later
	// LastStatusRead than every other task — proves cap eviction removes
	// the LEAST-recently-read tasks first, the same "actively polled
	// survives" invariant BDD-52 already established for TTL-driven
	// eviction, now also holding for cap-driven eviction.
	survivorSID := sessionIDs[0]
	fakeNow = fakeNow.Add(time.Minute)
	if r := tool.Execute(context.Background(), map[string]any{"action": "status", "session_id": survivorSID}); r.IsError {
		t.Fatalf("status poll for survivor failed: %s", r.ForLLM)
	}

	// Run the exact same eviction pass every registration triggers
	// (FR-045/FR-087's bookkeeping-driven trigger), directly — no new task
	// is added here, so the resulting count is exactly what cap
	// enforcement leaves behind.
	tool.mu.Lock()
	tool.evictStaleTasksLocked()
	gotTasks := len(tool.tasks)
	gotIndex := len(tool.sessionIndex)
	tool.mu.Unlock()

	if gotTasks > capN {
		t.Errorf("FR-087: expected len(t.tasks) <= %d after eviction — none of these %d tasks had aged past "+
			"the 1-hour TTL, so this bound can ONLY come from cap enforcement — got %d", capN, n, gotTasks)
	}
	if gotIndex > capN {
		t.Errorf("FR-087: expected len(t.sessionIndex) <= %d after eviction, got %d", capN, gotIndex)
	}

	statusResult := tool.Execute(context.Background(), map[string]any{"action": "status", "session_id": survivorSID})
	if statusResult.IsError {
		t.Errorf("expected the actively-polled task to survive cap eviction (least-recently-read evicted "+
			"first), got error: %s", statusResult.ForLLM)
	}
}

// TestListTaskCopies_DoesNotMutateStoredLastStatusRead is a direct,
// surgical reproduction of the second half of defect 2: a bare list-all
// read (listTaskCopies, backing action:"status" with no task_id/
// session_id) must not reset ANY task's own eviction clock — only a
// targeted single-task read (getTaskCopy) legitimately does that.
func TestListTaskCopies_DoesNotMutateStoredLastStatusRead(t *testing.T) {
	tool, _ := u14PermissiveTool(t)
	fakeNow := time.Now()
	tool.SetClock(func() time.Time { return fakeNow })

	const wantStamp = int64(123456789000)
	tool.mu.Lock()
	tool.tasks["fix6-probe"] = &DelegateTaskState{
		ID: "fix6-probe", Status: "completed", Created: wantStamp, LastStatusRead: wantStamp,
	}
	tool.mu.Unlock()

	// Advance the clock so a mutating listTaskCopies would produce an
	// OBSERVABLY different stamp than wantStamp — a bug that happened to
	// read "now" as the same instant would otherwise pass vacuously.
	fakeNow = fakeNow.Add(time.Hour)

	copies := tool.listTaskCopies()
	if len(copies) != 1 {
		t.Fatalf("expected exactly 1 task copy, got %d", len(copies))
	}
	if copies[0].LastStatusRead != wantStamp {
		t.Errorf("returned copy's LastStatusRead changed from %d to %d — listTaskCopies must not fabricate "+
			"a new stamp", wantStamp, copies[0].LastStatusRead)
	}

	tool.mu.Lock()
	gotStored := tool.tasks["fix6-probe"].LastStatusRead
	tool.mu.Unlock()
	if gotStored != wantStamp {
		t.Errorf("BLOCKER-adjacent (defect 2): listTaskCopies must NOT mutate the STORED task's "+
			"LastStatusRead — a bare list-all action:\"status\" poll would otherwise refresh the eviction "+
			"clock on EVERY task in the map, including other conversations', starving eviction indefinitely "+
			"regardless of the configured retention policy. stored LastStatusRead changed from %d to %d",
			wantStamp, gotStored)
	}

	// Positive control: a SECOND call, after another clock advance, must
	// still leave the stored value untouched — proves this isn't a
	// first-call-only coincidence.
	fakeNow = fakeNow.Add(time.Hour)
	_ = tool.listTaskCopies()
	tool.mu.Lock()
	gotStored2 := tool.tasks["fix6-probe"].LastStatusRead
	tool.mu.Unlock()
	if gotStored2 != wantStamp {
		t.Errorf("second listTaskCopies call mutated stored LastStatusRead: got %d, want %d", gotStored2, wantStamp)
	}
}

// ---------------------------------------------------------------------
// Defect 3: verifyCallerOwnsSession must distinguish a genuine I/O error
// from the expected not-found chain-end, logging the former, while still
// denying ownership (fail-closed) either way.
// ---------------------------------------------------------------------

// fix6FaultyLifecycleStore wraps a real, on-disk *session.LifecycleStore
// and injects a fixed error for exactly one session id's Load call,
// leaving every other id — and every other method (Persist/Lock/Mutate,
// all promoted from the embedded real store) — backed by real state. This
// simulates a truncated/corrupt .jsonl or a disk-full partial write
// without depending on platform-specific filesystem fault injection, and
// keeps the rest of the ancestor-chain walk exercising REAL store-backed
// records rather than a hand-rolled fake of the whole interface.
type fix6FaultyLifecycleStore struct {
	*session.LifecycleStore
	failFor string
	failErr error
}

func (f *fix6FaultyLifecycleStore) Load(sessionID string) (*session.LifecycleRecord, error) {
	if sessionID == f.failFor {
		return nil, f.failErr
	}
	return f.LifecycleStore.Load(sessionID)
}

func TestVerifyCallerOwnsSession_LogsIOErrorDistinctFromNotFound(t *testing.T) {
	t.Run("genuine I/O error on ancestor Load: denies AND logs distinctly", func(t *testing.T) {
		realStore := session.NewLifecycleStore(t.TempDir())
		wantErr := errors.New("fix6: simulated disk read failure")
		wrapped := &fix6FaultyLifecycleStore{LifecycleStore: realStore, failFor: "fix6-ancestor-corrupt", failErr: wantErr}

		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetLifecycleStore(wrapped)

		target := fix6NewLifecycleRecord("fix6-target-io-error", "fix6-ancestor-corrupt")

		getLogs := captureLogs(t)
		ctx := WithTranscriptSessionID(context.Background(), "fix6-real-caller-io")
		err := tool.verifyCallerOwnsSession(ctx, target)
		if err == nil {
			t.Fatal("expected ownership denial when the ancestor chain hits a genuine I/O error — the walk " +
				"MUST stay fail-closed, not just diagnosable")
		}
		if !strings.Contains(err.Error(), "not owned by the calling session") {
			t.Errorf("expected the standard fail-closed denial message, got: %v", err)
		}
		logs := getLogs()
		if !strings.Contains(logs, "ancestor lifecycle record failed to load") {
			t.Errorf("BLOCKER-adjacent (defect 3): expected a Warn log distinguishing the I/O error from the "+
				"expected not-found chain-end, got logs: %q", logs)
		}
		if !strings.Contains(logs, "fix6-ancestor-corrupt") {
			t.Errorf("expected the log to name the ancestor session id that failed to load, got: %q", logs)
		}
		if !strings.Contains(logs, wantErr.Error()) {
			t.Errorf("expected the log to carry the real underlying error, got: %q", logs)
		}
	})

	// Positive control (Rule 4): proves the log above is a real, narrow
	// signal — not something that fires on every denial regardless of
	// cause. The expected not-found chain-end (ancestor never persisted,
	// e.g. it names the root chat) must deny WITHOUT the I/O-error log.
	t.Run("expected not-found chain-end: denies WITHOUT the I/O-error log", func(t *testing.T) {
		realStore := session.NewLifecycleStore(t.TempDir())
		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetLifecycleStore(realStore)

		target := fix6NewLifecycleRecord("fix6-target-not-found", "fix6-ancestor-never-persisted")

		getLogs := captureLogs(t)
		ctx := WithTranscriptSessionID(context.Background(), "fix6-real-caller-notfound")
		err := tool.verifyCallerOwnsSession(ctx, target)
		if err == nil {
			t.Fatal("expected ownership denial for an unmatched ancestor chain, got nil")
		}
		logs := getLogs()
		if strings.Contains(logs, "ancestor lifecycle record failed to load") {
			t.Errorf("must NOT log the I/O-error warning for the expected not-found chain-end case, got "+
				"logs: %q", logs)
		}
	})

	// Positive control: a real, matching ancestor still succeeds — proves
	// the fail-closed posture wasn't accidentally tightened into an
	// always-deny by this fix.
	t.Run("real ancestor match still succeeds, no log at all", func(t *testing.T) {
		realStore := session.NewLifecycleStore(t.TempDir())
		tool := NewDelegateTool("test-model", 0, 0)
		tool.SetLifecycleStore(realStore)

		target := fix6NewLifecycleRecord("fix6-target-match", "fix6-real-caller-match")

		getLogs := captureLogs(t)
		ctx := WithTranscriptSessionID(context.Background(), "fix6-real-caller-match")
		if err := tool.verifyCallerOwnsSession(ctx, target); err != nil {
			t.Fatalf("expected ownership to be granted for a direct parent match, got: %v", err)
		}
		if logs := getLogs(); strings.Contains(logs, "ancestor lifecycle record failed to load") {
			t.Errorf("must not log an I/O-error warning on a successful ownership match, got logs: %q", logs)
		}
	})
}

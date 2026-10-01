// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Oracles: ADR sub-agent control plane, cd20cf8b365e7bc8a010c731a8f6830e14397c23,
// D2/D4/D5/D7/T27. These are independent prerequisite tests, not a claim that
// delayed-effect/replacement isolation has been proved. The production baseline
// cannot perform the requested same-generation stopped RESUME. No test resets
// generations, fabricates stopped records, or installs a global test hook.

func TestExecutionIdentityT27_DurableIdentityBeforeQueuedAdmission(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, true)
	if got := f.al.steerAdmission().queueLen(); got != 1 {
		t.Fatalf("production admission queue length=%d, want the selected child alone", got)
	}
	if got := f.al.getActiveTurnState(f.childID); got != nil {
		t.Fatal("queued prerequisite is invalid: selected child already has a live turn")
	}
	rec := loadExecutionIdentityT27Record(t, f)
	if rec.State != session.LifecycleQueued {
		t.Fatalf("durable queued state=%q, want queued", rec.State)
	}
	requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)
}

func TestExecutionIdentityT27_DurableIdentityBeforeActiveAdmission(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, false)
	live := f.al.getActiveTurnState(f.childID)
	if live == nil || !f.al.steerAdmission().hasReservation(f.childID, f.generation) {
		t.Fatal("active prerequisite is invalid: real provider call has no live turn/admission reservation")
	}
	rec := loadExecutionIdentityT27Record(t, f)
	if rec.State != session.LifecycleRunning {
		t.Fatalf("durable active state=%q, want running before the held provider returns", rec.State)
	}
	requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)
}

func TestExecutionIdentityT27_NeverRanStopLandsStoppedWithCauseNote(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, true)
	cancelExecutionIdentityT27Child(t, f, "old-stop")
	if got := f.al.steerAdmission().queueLen(); got != 0 {
		t.Fatalf("queue after real old Stop=%d, want selected old admission removed", got)
	}
	rec := loadExecutionIdentityT27Record(t, f)
	snapshot := executionIdentityT27JournalTail(t, f)
	t.Run("nonterminal_stopped_and_fence_spent", func(t *testing.T) {
		if rec.State != session.LifecycleState("stopped") {
			t.Fatalf("BLOCKED: non-terminal stopped landing not implemented — required by D2/D7/T27; real Stop landed state=%q, terminal=%v", rec.State, rec.Terminal())
		}
		if rec.Terminal() || rec.Generation != f.generation || rec.Stop != nil {
			t.Fatalf("landed stop=(terminal=%v, generation=%d, fence=%+v), want (false, %d, nil)", rec.Terminal(), rec.Generation, rec.Stop, f.generation)
		}
	})
	t.Run("durable_cascade_cause_note", func(t *testing.T) {
		raw, exists := snapshot["stop_note"]
		if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			t.Fatal("BLOCKED: durable stop_note not implemented — required by ADR sub-agent control plane D2/D7/T27")
		}
		var note map[string]json.RawMessage
		if err := json.Unmarshal(raw, &note); err != nil {
			t.Fatalf("persisted stop_note cannot be decoded: %v", err)
		}
		for _, key := range []string{"at", "by", "seq", "cause"} { // D2 required note fields.
			if value, ok := note[key]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				t.Fatalf("stop_note.%s is missing/null; D2 requires a durable landed cause", key)
			}
		}
		var cause string
		// steer_cancel.go::(*SteerCanceller).cascade's own doc comment:
		// "StopCauseStop for the cascade's own direct target (sessionID —
		// the session named in the CancelSubtree/StopSubtree call),
		// StopCauseCascade for every OTHER id, which is reachable only
		// because it is sessionID's descendant." cancelExecutionIdentityT27Child
		// calls CancelSubtree(ctx, f.childID, ...) -- f.childID IS the
		// direct target here, not a swept-up descendant, so "stop" is the
		// correct landed cause. The original "want cascade" assertion was a
		// test-oracle bug (confirmed by reading the production doc comment
		// directly), not a production defect -- corrected here.
		if err := json.Unmarshal(note["cause"], &cause); err != nil || cause != "stop" {
			t.Fatalf("stop_note.cause=%q, error=%v, want stop (direct-target cause per D7/cascade())", cause, err)
		}
	})
}

func TestExecutionIdentityT27_ExplicitResumeKeepsGenerationAndReplacesRun(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, true)
	cancelExecutionIdentityT27Child(t, f, "old-stop")
	oldSnapshot := executionIdentityT27JournalTail(t, f)
	resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, "explicit replacement instruction")
	if err != nil || !resumed {
		t.Fatalf("real explicit RESUME=(%v, %v), want accepted replacement admission", resumed, err)
	}
	replacement := loadExecutionIdentityT27Record(t, f)
	if replacement.Generation != f.generation {
		t.Fatalf("BLOCKED: same-generation stopped RESUME not implemented — required by D2/D5/T27; old generation=%d, replacement=%d", f.generation, replacement.Generation)
	}
	if replacement.State != session.LifecycleQueued || replacement.Stop != nil || f.al.steerAdmission().queueLen() != 1 {
		t.Fatalf("replacement=(state=%q, fence=%+v, queue=%d), want (queued, nil, 1) behind the untouched busy worker", replacement.State, replacement.Stop, f.al.steerAdmission().queueLen())
	}
	newSnapshot := executionIdentityT27JournalTail(t, f)
	for _, key := range []string{"stop_note", "stop_effect"} {
		if raw, present := newSnapshot[key]; present && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			t.Fatalf("explicit RESUME retained current %s=%s, want cleared atomically (D2)", key, raw)
		}
	}
	oldIdentity := requireExecutionIdentityT27Identity(t, oldSnapshot, f.childID, f.generation)
	newIdentity := requireExecutionIdentityT27Identity(t, newSnapshot, f.childID, f.generation)
	if bytes.Equal(oldIdentity["run_id"], newIdentity["run_id"]) {
		t.Fatalf("replacement reused old run_id=%s, want a distinct admission identity (D2/T27)", oldIdentity["run_id"])
	}
	if !bytes.Equal(oldIdentity["boot_seq"], newIdentity["boot_seq"]) {
		t.Fatalf("same-process RESUME changed boot_seq from %s to %s", oldIdentity["boot_seq"], newIdentity["boot_seq"])
	}
}

func TestExecutionIdentityT27_CascadeReleasesLockBeforeRealLiveCancel(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, false)
	type lockObservation struct{ cascadeFree, lifecycleFree bool }
	observed := make(chan lockObservation, 1)
	var canceller *SteerCanceller
	// This ordinary construction-injected dependency probes the actual owning
	// locks and FORWARDS to production. It never supplies a fake cancel result.
	callback := func(ctx context.Context, id string, generation int) (GenerationCancelResult, error) {
		cascadeLock := canceller.cascadeLock(f.childID)
		cascadeFree := cascadeLock.TryLock()
		if cascadeFree {
			cascadeLock.Unlock()
		}
		lifecycleLock := f.al.GetSessionLifecycleStore().Lock(id)
		lifecycleFree := lifecycleLock.TryLock()
		if lifecycleFree {
			lifecycleLock.Unlock()
		}
		observed <- lockObservation{cascadeFree: cascadeFree, lifecycleFree: lifecycleFree}
		return f.al.SteerGenerationCancel(ctx, id, generation)
	}
	canceller = NewSteerCanceller(f.al.GetSessionLifecycleStore(), callback).
		SetRevivalStateWriter(f.al.WriteSteerRevivalState)
	f.al.SetSteerCanceller(canceller)
	cancelExecutionIdentityT27Child(t, f, "lock-probe-stop")
	waitExecutionIdentityT27Signal(t, f.childCall.cancelled, "real provider observed live cancellation")
	if err := f.childCall.ctx.Err(); err != context.Canceled {
		t.Fatalf("provider cancellation error=%v, want context.Canceled", err)
	}
	locks := <-observed // CancelSubtree returned after the forwarding callback.
	if !locks.lifecycleFree {
		t.Error("lifecycle lock held at the real live-cancel boundary; D2/T27 require it released")
	}
	if !locks.cascadeFree {
		t.Error("cascade lock held at the real live-cancel boundary; D2/D7/T27 require it released before live effects")
	}
}

// Positive control verifies a newer Stop remains effective on the ACTUAL
// production replacement. On the baseline that replacement bumps generation;
// this test deliberately does not certify same-generation T27 isolation.
func TestExecutionIdentityT27_NewerStopCancelsReplacementPositiveControl(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, true)
	cancelExecutionIdentityT27Child(t, f, "old-stop")
	const instruction = "replacement work for the newer Stop positive control"
	resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, instruction)
	if err != nil || !resumed {
		t.Fatalf("positive control real RESUME=(%v, %v), want accepted", resumed, err)
	}
	replacement := loadExecutionIdentityT27Record(t, f)
	if replacement.State != session.LifecycleQueued || f.al.steerAdmission().queueLen() != 1 {
		t.Fatalf("replacement not queued behind real busy worker: state=%q queue=%d", replacement.State, f.al.steerAdmission().queueLen())
	}
	close(f.busyCall.finish)
	call := nextExecutionIdentityT27ProviderCall(t, f.provider)
	live := f.al.getActiveTurnState(f.childID)
	if live == nil || !f.al.steerAdmission().hasReservation(f.childID, replacement.Generation) {
		t.Fatal("positive control provider was reached without the replacement's real live turn/admission")
	}
	instructionReached := false
	for _, message := range call.messages {
		if message.Role == "user" && message.Content == instruction {
			instructionReached = true
		}
	}
	if !instructionReached {
		t.Fatalf("replacement provider did not receive exact RESUME instruction %q", instruction)
	}
	cancelExecutionIdentityT27Child(t, f, "newer-stop")
	waitExecutionIdentityT27Signal(t, call.cancelled, "replacement provider cancelled by genuinely newer Stop")
	if err := call.ctx.Err(); err != context.Canceled || !live.hardAbortRequested() {
		t.Fatalf("newer Stop=(provider error=%v, hard-abort=%v), want (context.Canceled, true)", err, live.hardAbortRequested())
	}
	fenced := loadExecutionIdentityT27Record(t, f)
	if fenced.Generation != replacement.Generation || fenced.Stop == nil ||
		fenced.Stop.Generation != replacement.Generation || fenced.Stop.By.ID != "newer-stop" {
		t.Fatalf("newer Stop did not fence actual replacement generation=%d with its own actor: %+v", replacement.Generation, fenced)
	}
	close(call.finish)
	waitExecutionIdentityT27Turns(t, f.al)
	if f.al.getActiveTurnState(f.childID) != nil || f.al.steerAdmission().hasReservation(f.childID, replacement.Generation) || f.al.steerAdmission().queueLen() != 0 {
		t.Fatal("newer Stop left replacement live/queued or retained its admission after joined disposal")
	}
	t.Logf("newer Stop reached the real replacement provider and released its turn/admission; actual generation %d -> %d (same-generation acceptance is separate)", f.generation, replacement.Generation)
}

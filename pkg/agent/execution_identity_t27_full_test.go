// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// execution_identity_t27_full_test.go is the mandatory full-T27-coverage RED
// follow-up to the partial pack (fc6519908) and the three sub-scopes landed
// on top of it: durable execution-identity stamping before admission
// (1bb87c0c6), cascade-lock release before live-cancel effects (62f40f57b),
// and explicit human RESUME keeping generation while replacing the run
// (9c4010774). Those three are prerequisites, not proof: T27's own text
// requires same-generation stale-old-effect-versus-fresh-RESUME isolation at
// EVERY delayed effect boundary (queue removal, graceful interrupt, hard
// escalation, never-ran/late-turn finalization, the cascade's own
// second-pass/retry, and historical-notice dedup) — D2/D4/D5/D7's own text
// says the existing generation-bumping mechanism is NOT proof of safety for
// this new same-generation design, and that is exactly what every test below
// exercises through real, unexported, production AgentLoop methods: no
// fake store, no test-only hook, no mocked cancel/interrupt/finalize path.
//
// Oracles: ADR sub-agent control plane (docs/adr-cp-grill-r4 branch,
// 4cb1343b7), D2/D4/D5/D7/T27. Every "stale" effect below is a DIRECT call to
// the exact unexported method a delayed/retried production caller would
// invoke (steer_cancel.go::cascade's own GenerationCancelFunc field,
// admission.go::removeQueuedSession, steering.go::Interrupt, and
// steer_cancel.go::terminaliseNeverRanStop/reportSteeredSessionTerminalUpward)
// bound to the OLD generation — forcing the dangerous interleaving
// deterministically is the sanctioned T27 technique ("deliberately
// block/fail the real dependency first to prove the barrier reaches it"),
// not a substitute for it.
package agent

import (
	"bytes"
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// --- Item 1/2: both queue-removal callers, raced against a same-generation
// explicit RESUME replacement. admission.go::removeQueuedSession takes only
// a session id — "drops EVERY queued entry for sessionID, whatever
// generation each carries" by its own doc comment — so a stale invocation
// bound to the OLD (now-replaced) admission has nothing distinguishing it
// from a fresh one once RESUME keeps the SAME generation (D2/D5). Both
// callers share this one function: steer_cancel.go::SteerGenerationCancel
// (every cascade's per-reached-session live-cancel callback) and
// steer_delegate_cancel.go::cancelDelegatedSubtree's own extra direct call
// after the cascade returns.

func TestExecutionIdentityT27Full_StaleGenerationCancelCallbackSparesQueuedReplacement(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, true)
	cancelExecutionIdentityT27Child(t, f, "old-stop")
	resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, "replacement instruction A1")
	if err != nil || !resumed {
		t.Fatalf("real explicit RESUME=(%v, %v), want accepted replacement admission", resumed, err)
	}

	t.Run("control_resume_alone_is_healthy", func(t *testing.T) {
		replacement := loadExecutionIdentityT27Record(t, f)
		if got := f.al.steerAdmission().queueLen(); replacement.Generation != f.generation ||
			replacement.State != session.LifecycleQueued || got != 1 {
			t.Fatalf("replacement before any stale effect=(generation=%d, state=%q, queue=%d), want (%d, queued, 1)",
				replacement.Generation, replacement.State, got, f.generation)
		}
	})
	beforeIdentity := requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)

	// The stale delayed effect: a retried/late invocation of the EXACT
	// GenerationCancelFunc the cascade's fireLiveCancels calls per reached
	// session (steer_cancel.go::cancelStamped -> c.cancelTurn), bound to the
	// OLD stamp's generation. D7 releases the cascade lock before firing
	// this callback specifically so it CAN be arbitrarily delayed; T27
	// requires it be a safe no-op once a same-generation replacement exists.
	if _, err := f.al.SteerGenerationCancel(context.Background(), f.childID, f.generation); err != nil {
		t.Fatalf("stale SteerGenerationCancel callback returned an error: %v", err)
	}

	if got := f.al.steerAdmission().queueLen(); got != 1 {
		t.Fatalf("BLOCKED: queue-removal has no execution-identity check — required by D2/D4/T27; stale old-generation callback (caller A, used by every cascade) removed the same-generation replacement's queued admission (queue=%d, want 1)", got)
	}
	replacement := loadExecutionIdentityT27Record(t, f)
	if replacement.State != session.LifecycleQueued || replacement.Generation != f.generation {
		t.Fatalf("stale callback altered replacement durable state=(state=%q, generation=%d), want (queued, %d) untouched",
			replacement.State, replacement.Generation, f.generation)
	}
	afterIdentity := requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)
	if !bytes.Equal(beforeIdentity["run_id"], afterIdentity["run_id"]) {
		t.Fatalf("stale callback reminted the replacement's run_id from %s to %s", beforeIdentity["run_id"], afterIdentity["run_id"])
	}
}

func TestExecutionIdentityT27Full_StaleDirectQueueRemovalSparesQueuedReplacement(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, true)

	t.Run("control_direct_removal_works_on_an_unrelated_queued_session", func(t *testing.T) {
		otherParent := newTestSteeringSession(t, f.al, "ws-1")
		otherID, otherGen := launchSteeredChild(t, f.al, otherParent, "t27-full-control-queue", "unrelated control instruction")
		result := dispatchExecutionIdentityT27(t, f.al, otherID, otherGen, steer.DispatchQueued)
		if result.QueuePosition != 2 { // behind the harness's busy worker AND the selected child.
			t.Fatalf("control queue position=%d, want 2 (behind the real busy worker's slot and the selected child)", result.QueuePosition)
		}
		if removed := f.al.steerAdmission().removeQueuedSession(otherID); removed != 1 {
			t.Fatalf("real removeQueuedSession(unrelated control session)=%d, want 1 removed — the function's basic correctness, independent of the race under test", removed)
		}
		if got := f.al.steerAdmission().queueLen(); got != 1 { // only the selected child remains.
			t.Fatalf("queue after the control removal=%d, want 1 (selected child untouched)", got)
		}
	})

	cancelExecutionIdentityT27Child(t, f, "old-stop")
	resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, "replacement instruction A2")
	if err != nil || !resumed {
		t.Fatalf("real explicit RESUME=(%v, %v), want accepted replacement admission", resumed, err)
	}
	if got := f.al.steerAdmission().queueLen(); got != 1 {
		t.Fatalf("replacement before the stale direct removal: queue=%d, want 1", got)
	}
	beforeIdentity := requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)

	// The stale delayed effect: cancelDelegatedSubtree's OWN direct removal
	// loop (steer_delegate_cancel.go: "for _, id := range report.Reached {
	// gate.removeQueuedSession(id) }"), bound to an already-stale cascade
	// report for the OLD run — the SECOND of T27's two queue-removal
	// callers, independent of caller A above.
	removed := f.al.steerAdmission().removeQueuedSession(f.childID)

	if got := f.al.steerAdmission().queueLen(); got != 1 {
		t.Fatalf("BLOCKED: queue-removal has no execution-identity check — required by D2/D4/T27; cancelDelegatedSubtree's own direct removal call (removed=%d) dropped the same-generation replacement's queued admission (queue=%d, want 1)",
			removed, got)
	}
	afterIdentity := requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)
	if !bytes.Equal(beforeIdentity["run_id"], afterIdentity["run_id"]) {
		t.Fatalf("stale direct removal reminted the replacement's run_id from %s to %s", beforeIdentity["run_id"], afterIdentity["run_id"])
	}
}

func TestExecutionIdentityT27Full_BothQueueRemovalCallersTogetherSpareQueuedReplacement(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, true)
	cancelExecutionIdentityT27Child(t, f, "old-stop")
	resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, "replacement instruction A3")
	if err != nil || !resumed {
		t.Fatalf("real explicit RESUME=(%v, %v), want accepted replacement admission", resumed, err)
	}
	t.Run("control_resume_alone_is_healthy", func(t *testing.T) {
		if got := f.al.steerAdmission().queueLen(); got != 1 {
			t.Fatalf("replacement before either stale caller fires: queue=%d, want 1", got)
		}
	})
	beforeIdentity := requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)

	// Both T27 queue-removal callers replaying late, back to back — exactly
	// the sequence cancelDelegatedSubtree(hard=true) performs for every
	// reached session in ONE call: the cascade's own per-node callback
	// (caller A, via SteerGenerationCancel) THEN its own extra direct
	// removal loop (caller B). Both bound to the already-stale old report.
	if _, err := f.al.SteerGenerationCancel(context.Background(), f.childID, f.generation); err != nil {
		t.Fatalf("stale caller A (SteerGenerationCancel) returned an error: %v", err)
	}
	f.al.steerAdmission().removeQueuedSession(f.childID) // stale caller B.

	if got := f.al.steerAdmission().queueLen(); got != 1 {
		t.Fatalf("BLOCKED: both T27 queue-removal callers share one execution-identity-blind function — required by D2/D4/D7/T27; combined stale replay left queue=%d, want 1 (same-generation replacement spared)", got)
	}
	replacement := loadExecutionIdentityT27Record(t, f)
	if replacement.State != session.LifecycleQueued || replacement.Generation != f.generation {
		t.Fatalf("combined stale replay altered replacement durable state=(state=%q, generation=%d), want (queued, %d)",
			replacement.State, replacement.Generation, f.generation)
	}
	afterIdentity := requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)
	if !bytes.Equal(beforeIdentity["run_id"], afterIdentity["run_id"]) {
		t.Fatalf("combined stale replay reminted the replacement's run_id from %s to %s", beforeIdentity["run_id"], afterIdentity["run_id"])
	}
}

// --- Item 3/4: a delayed graceful interrupt or hard escalation, bound to
// the OLD run, racing a same-generation RESUME that has already dispatched
// a replacement into the FREED admission slot (so the replacement has its
// own real, live provider call to wrongly touch).

func TestExecutionIdentityT27Full_StaleCancelCallbackHardAbortsRunningReplacement(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, false) // no busy worker: the selected child is itself the real live turn.
	cancelExecutionIdentityT27Child(t, f, "old-stop")
	waitExecutionIdentityT27Signal(t, f.childCall.cancelled, "old real provider observed the live hard-abort")
	close(f.childCall.finish) // let the provider return ctx.Err() so the turn's own completion path can land it.
	waitExecutionIdentityT27RecordState(t, f, session.LifecycleStopped)

	resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, "replacement instruction B1")
	if err != nil || !resumed {
		t.Fatalf("real explicit RESUME=(%v, %v), want accepted replacement admission", resumed, err)
	}
	replacement := loadExecutionIdentityT27Record(t, f)
	replacementCall := nextExecutionIdentityT27ProviderCall(t, f.provider)
	live := f.al.getActiveTurnState(f.childID)
	if live == nil || !f.al.steerAdmission().hasReservation(f.childID, replacement.Generation) {
		t.Fatal("replacement never reached the real provider with its own live turn/admission reservation")
	}

	t.Run("control_replacement_is_healthy_before_the_stale_callback", func(t *testing.T) {
		if err := replacementCall.ctx.Err(); err != nil {
			t.Fatalf("replacement's real provider call already cancelled=%v, want nil before any stale effect fires", err)
		}
		if live.hardAbortRequested() {
			t.Fatal("replacement's live turn already shows a hard-abort request before any stale effect fires")
		}
	})

	// The stale delayed effect: the SAME GenerationCancelFunc the OLD hard
	// cascade's callback already invoked once, bound to the OLD generation
	// — e.g. a redundant/retried delivery of that one cancelTurn call. Same
	// generation as the replacement, which is T27's whole premise (explicit
	// same-generation RESUME) — through the real unexported method a
	// cascade's callback field actually calls, requestCancelForGeneration
	// included.
	if _, err := f.al.SteerGenerationCancel(context.Background(), f.childID, f.generation); err != nil {
		t.Fatalf("stale SteerGenerationCancel callback returned an error: %v", err)
	}

	if err := replacementCall.ctx.Err(); err != nil {
		t.Fatalf("BLOCKED: live hard-abort targeting has no execution-identity check — required by D2/T27; stale old-generation callback hard-aborted the same-generation REPLACEMENT's real live provider call: %v", err)
	}
	if live.hardAbortRequested() {
		t.Fatal("BLOCKED: live hard-abort targeting has no execution-identity check — required by D2/T27; stale old-generation callback marked the replacement's live turn hard-abort-requested")
	}
}

func TestExecutionIdentityT27Full_StaleGracefulInterruptTouchesRunningReplacement(t *testing.T) {
	f := newExecutionIdentityT27Harness(t, false)
	cancelExecutionIdentityT27Child(t, f, "old-stop")
	waitExecutionIdentityT27Signal(t, f.childCall.cancelled, "old real provider observed the live hard-abort")
	close(f.childCall.finish)
	waitExecutionIdentityT27RecordState(t, f, session.LifecycleStopped)

	resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, "replacement instruction B2")
	if err != nil || !resumed {
		t.Fatalf("real explicit RESUME=(%v, %v), want accepted replacement admission", resumed, err)
	}
	replacement := loadExecutionIdentityT27Record(t, f)
	replacementCall := nextExecutionIdentityT27ProviderCall(t, f.provider)
	live := f.al.getActiveTurnState(f.childID)
	if live == nil || !f.al.steerAdmission().hasReservation(f.childID, replacement.Generation) {
		t.Fatal("replacement never reached the real provider with its own live turn/admission reservation")
	}

	t.Run("control_replacement_is_healthy_before_the_stale_interrupt", func(t *testing.T) {
		if requested, hint := live.gracefulInterruptRequested(); requested {
			t.Fatalf("replacement's live turn already shows a graceful-interrupt request (hint=%q) before any stale effect fires", hint)
		}
	})

	// The stale delayed effect: cancelDelegatedSubtree's OWN cooperative
	// half (steer_delegate_cancel.go: "al.Interrupt(id, ScopeSelfOnly,
	// hint)"), fired for the OLD cascade's reached session — resolved by
	// resolveInterruptTargets purely by session id, with no generation or
	// execution-identity parameter anywhere in Interrupt's signature.
	descendants, ierr := f.al.Interrupt(f.childID, ScopeSelfOnly, "stale-old-graceful-hint")
	if ierr != nil {
		t.Fatalf("stale Interrupt call returned an error: %v", ierr)
	}

	if requested, hint := live.gracefulInterruptRequested(); requested {
		t.Fatalf("BLOCKED: Interrupt resolves targets by session id alone with no execution-identity check — required by D2/T27; stale old-generation graceful interrupt reached the same-generation REPLACEMENT's real live turn (descendants=%v, hint=%q)",
			descendants, hint)
	}
	if err := replacementCall.ctx.Err(); err != nil {
		t.Fatalf("stale graceful interrupt also cancelled the replacement's real provider ctx: %v", err)
	}
}

// --- Item 5: never-ran versus a turn that ran and is finalizing late, both
// racing a same-generation RESUME. The two subtests deliberately contrast:
// the never-ran finalizer (terminaliseNeverRanStop) happens to be identity
// SAFE against this exact retry, because reviveSameGeneration clears
// rec.Stop and that finalizer's own precondition requires rec.Stop != nil —
// a narrower accident of field clearing, not full (session_id, generation,
// boot_seq, run_id) targeting (D2). It does not generalise: the shared
// terminal-report writer (reportSteeredSessionTerminalUpward, used by BOTH
// the never-ran path and drainSteerQueue's dispatch-failure path) checks
// only cur.Generation inside its own Mutate and is NOT identity-safe against
// a retried call landing against a running, same-generation replacement.

func TestExecutionIdentityT27Full_NeverRanAndLateRunningFinalizationRetryUnderRace(t *testing.T) {
	t.Run("never_ran_finalization_retry_is_already_identity_safe", func(t *testing.T) {
		f := newExecutionIdentityT27Harness(t, true)
		cancelExecutionIdentityT27Child(t, f, "old-stop")
		resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
			steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, "replacement instruction C1")
		if err != nil || !resumed {
			t.Fatalf("real explicit RESUME=(%v, %v), want accepted replacement admission", resumed, err)
		}
		before := loadExecutionIdentityT27Record(t, f)
		beforeIdentity := requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)

		// A retried/duplicate invocation of the never-ran finalizer, bound
		// to the OLD generation — e.g. a redundant cascade callback firing
		// twice for the same stamped id.
		f.al.terminaliseNeverRanStop(context.Background(), f.childID, f.generation)

		after := loadExecutionIdentityT27Record(t, f)
		if after.State != before.State || after.Generation != before.Generation {
			t.Fatalf("retried never-ran finalizer altered the replacement: before=(state=%q,gen=%d) after=(state=%q,gen=%d)",
				before.State, before.Generation, after.State, after.Generation)
		}
		afterIdentity := requireExecutionIdentityT27Identity(t, executionIdentityT27JournalTail(t, f), f.childID, f.generation)
		if !bytes.Equal(beforeIdentity["run_id"], afterIdentity["run_id"]) {
			t.Fatalf("retried never-ran finalizer reminted the replacement's run_id from %s to %s", beforeIdentity["run_id"], afterIdentity["run_id"])
		}
	})

	t.Run("late_running_turn_finalization_retry_clobbers_running_replacement", func(t *testing.T) {
		f := newExecutionIdentityT27Harness(t, false)
		cancelExecutionIdentityT27Child(t, f, "old-stop")
		waitExecutionIdentityT27Signal(t, f.childCall.cancelled, "old real provider observed the live hard-abort")
		close(f.childCall.finish)
		waitExecutionIdentityT27RecordState(t, f, session.LifecycleStopped)

		resumed, err := f.al.ReviveStoppedSession(context.Background(), f.childID,
			steer.Principal{Kind: steer.PrincipalKindHuman, ID: "resume-owner"}, "replacement instruction C2")
		if err != nil || !resumed {
			t.Fatalf("real explicit RESUME=(%v, %v), want accepted replacement admission", resumed, err)
		}
		replacement := loadExecutionIdentityT27Record(t, f)
		replacementCall := nextExecutionIdentityT27ProviderCall(t, f.provider)
		live := f.al.getActiveTurnState(f.childID)
		if live == nil || !f.al.steerAdmission().hasReservation(f.childID, replacement.Generation) {
			t.Fatal("replacement never reached the real provider with its own live turn/admission reservation")
		}

		t.Run("control_replacement_is_healthy_before_the_stale_retry", func(t *testing.T) {
			if replacement.State != session.LifecycleRunning {
				t.Fatalf("replacement state before the stale retry=%q, want running", replacement.State)
			}
			if err := replacementCall.ctx.Err(); err != nil {
				t.Fatalf("replacement's real provider call already cancelled=%v, want nil", err)
			}
		})

		// The stale delayed effect: a retried/duplicate delivery of the OLD
		// run's own terminal report. reportSteeredSessionTerminalUpward's
		// own doc comment names TWO callers that can reach it for the same
		// generation (terminaliseNeverRanStop, Finding 5; drainSteerQueue's
		// dispatch-failure branch, Finding 6) — a retried/duplicate delivery
		// of either is a realistic at-least-once effect, bound to the OLD
		// generation.
		f.al.reportSteeredSessionTerminalUpward(context.Background(), f.childID, f.generation,
			session.LifecycleStopped, steer.OutcomeInterrupted, "stale retried terminal report")

		after := loadExecutionIdentityT27Record(t, f)
		if after.State == session.LifecycleStopped {
			t.Fatalf("BLOCKED: terminal-report finalization checks only generation inside its Mutate, not full execution identity — required by D2/T27; stale retried old-generation finalization landed the same-generation REPLACEMENT %q as stopped while its real provider call is still live (ctx err=%v)",
				f.childID, replacementCall.ctx.Err())
		}
		if err := replacementCall.ctx.Err(); err != nil {
			t.Fatalf("stale retried finalization also cancelled the replacement's real live provider call: %v", err)
		}
	})
}

// --- Item 7: a late notice about an old execution identity must not be
// mistaken for a report about the current (same-generation replacement's)
// generation (D6/D8's "already-committed old stop notices remain historical
// and deduplicate without changing the replacement").
//
// BLOCKED: no durable, cross-effect dedup mechanism exists for a Stop
// notice at all. The three delivery/dedup keys that actually exist in this
// codebase are: (1) steer_frames.go::deliverSubagentState's transcript-frame
// id "<originCallID>:<generation>:state:<state>" — no run_id/execution-
// identity component, and it delivers a PARENT-CHAT transcript frame, not a
// durable per-generation Stop notice; (2) steer_audience.go/steer_cancel.go's
// final-answer message id "<child>:<generation>:final" (D2's
// ListPendingFinalDeliveries path) — scoped to the completed-ANSWER outbox,
// not a Stop/stopped-child notice; (3) boot_sweep.go::SteerBootRecovery.Run's
// `noticed` map — an in-memory, single-Run-call operator-notice dedup, not a
// durable cross-boot (parent_id, child_id, child_generation, stop_seq) key.
// D6/D8's own "direct-parent notice" with that exact key has no
// implementation anywhere in pkg/agent or pkg/session on this HEAD (grep for
// stop_seq/StopSeq/notice-dedup finds no such mechanism). Constructing this
// test would require building the missing notice/dedup machinery inside the
// test itself — which is the production gap D6/D8 must close, not something
// a test can stand in for. Deferred to GREEN; re-attempt once that mechanism
// lands.
func TestExecutionIdentityT27Full_HistoricalNoticeDedupAfterSameGenReplacement(t *testing.T) {
	t.Fatal("BLOCKED: D6/D8's durable (parent_id, child_id, child_generation, stop_seq)-keyed stop-notice " +
		"dedup is not implemented anywhere in pkg/agent or pkg/session on this HEAD — required by D6/D8/T27 " +
		"('Already-committed old stop notices remain historical and deduplicate without changing the replacement'). " +
		"The only three delivery/dedup keys that exist (deliverSubagentState's transcript-frame id, the " +
		"<child>:<generation>:final outbox message id, and boot_sweep.go's in-memory per-Run `noticed` map) are " +
		"each scoped to a different concern and none carries run_id/execution identity or survives across a " +
		"same-generation RESUME. This cannot be tested without first building the missing notice mechanism, which " +
		"is GREEN's job, not a test's.")
}

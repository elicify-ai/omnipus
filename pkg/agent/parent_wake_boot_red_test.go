// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for parent-wake visibility, Tests 5 and 14–17.
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md
//   - PW-D6, PW-D14, PW-D21
//   - FR-014, FR-015, FR-016
//   - BDD native resume, failed resume, user Stop not resumed, external helper
//
// A native helper that boot finds running is dispatched again. Today's boot
// marks it stopped and does not dispatch, so the new assistant occurrence
// never runs. An external helper is not replaced. A refused resume keeps the
// restart sentence and adds "Resume did not start."
//
// The runtime, roster, launcher, dispatch and stores are real. The provider is
// held so a resumed helper cannot finish before its admission is observed.
// These existing tests assert admission, not executor occurrence counts; full
// PW-D21 side-effect proof remains a separate test/UAT obligation.
//
// Step 4 items 2–3 are deferred to CHECK.
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestResumeSkipsHistoricalInvocationNotCallId(t *testing.T) {
	// The historical pair (assistant occurrence A, call id call_0) already
	// succeeded once. Resume is the next model decision. A later occurrence B
	// may reuse call_0 and must be allowed to run. This existing assertion
	// observes dispatch, not an executor counter; the capable runtime below
	// must let production boot, not the fixture, admit that next decision.
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))
	h.seedCompletedToolHistory(t, child)
	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("boot recovery Run: %v", err)
	}
	rec, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != session.LifecycleRunning && rec.State != session.LifecycleQueued {
		t.Fatalf("native helper state after boot = %q, want a new dispatch — occurrence B reusing call_0 did not run, and a historical occurrence+call_0 must not be what boot re-invokes (PW-D21 / FR-014 / Test 5). Executor-level count of that pair is not observable on this harness until resume calls the tool; do not treat a transcript row as that count", rec.State)
	}
}

func TestBoot_NativeHelperResumesFromHistory(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))
	h.seedCompletedToolHistory(t, child)
	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("boot recovery Run: %v", err)
	}
	rec, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != session.LifecycleRunning && rec.State != session.LifecycleQueued {
		t.Fatalf("native helper state after boot = %q, want running or queued — a restart cut-off dispatches it again from saved history (PW-D6 / FR-014 / Test 14)", rec.State)
	}
}

func TestBoot_ResumeFailureStillWakesOnce(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	rec.AgentID = "missing-agent-pw"
	h.persist(t, rec)
	if err := h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("boot recovery Run: %v", err)
	}
	entries, err := h.sessions.ReadTranscript(parent)
	if err != nil {
		t.Fatalf("ReadTranscript parent: %v", err)
	}
	var restart, refused int
	for _, e := range entries {
		if e.SystemSubtype != "helper_event" {
			continue
		}
		if strings.Contains(e.Content, "Interrupted by a restart.") {
			restart++
		}
		if strings.Contains(e.Content, "Resume did not start.") {
			refused++
		}
	}
	if restart != 1 {
		t.Fatalf("restart rows = %d, want 1 immutable \"Interrupted by a restart.\" (FR-015 / Test 15)", restart)
	}
	if refused != 1 {
		t.Fatalf("resume_refused rows = %d, want 1 \"Resume did not start.\" — a failed admission is a second row and does not replace the first (FR-015)", refused)
	}
	after, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load child: %v", err)
	}
	if after.State == session.LifecycleRunning || after.State == session.LifecycleQueued {
		t.Fatalf("helper state = %q after a refused resume, want it not shown as running", after.State)
	}
}

func TestBoot_UserStopNotResumed(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleStopped)
	rec.StopNote = &session.StopNote{At: rec.CreatedAt, By: session.StopActorHumanUser("owner"), Cause: session.StopCauseStop, Seq: 1}
	h.persist(t, rec)
	before, err := h.sessions.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if err = h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("boot recovery Run: %v", err)
	}
	afterRec, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if afterRec.State == session.LifecycleRunning || afterRec.State == session.LifecycleQueued {
		t.Fatalf("user-stopped helper state = %q, want it not dispatched by a later boot (PW-D13 / FR-010 / Test 16)", afterRec.State)
	}
	afterSessions, err := h.sessions.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions after: %v", err)
	}
	if len(afterSessions) != len(before) {
		t.Fatalf("session count %d → %d — a user Stop must not create a replacement worker", len(before), len(afterSessions))
	}
}

func TestBoot_ExternalHelperNotReplaced(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	rec.Is3P = true
	h.persist(t, rec)
	before, err := h.sessions.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if err = h.recovery().Run(context.Background()); err != nil {
		t.Fatalf("boot recovery Run: %v", err)
	}
	afterSessions, err := h.sessions.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions after: %v", err)
	}
	if len(afterSessions) != len(before) {
		t.Fatalf("session count %d → %d, want no new worker for an external command-line helper (FR-016 / Test 17)", len(before), len(afterSessions))
	}
	after, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if after.State == session.LifecycleRunning || after.State == session.LifecycleQueued {
		t.Fatalf("external helper state = %q, want it not auto-resumed (FR-016)", after.State)
	}
	entries, err := h.sessions.ReadTranscript(parent)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	var restart int
	for _, e := range entries {
		if e.SystemSubtype == "helper_event" && strings.Contains(e.Content, "Interrupted by a restart.") {
			restart++
		}
	}
	if restart != 1 {
		t.Fatalf("parent restart rows = %d, want 1 — the external helper still tells the parent, once (FR-016)", restart)
	}
}

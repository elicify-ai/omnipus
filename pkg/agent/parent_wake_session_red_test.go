// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for parent-wake visibility, Tests 3, 4, 18, 19 and 32.
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md §9.4
//   - FR-017, FR-018, FR-019, FR-022
//   - PW-D8, PW-D12, PW-D17
//   - BDD unknown wake reason, creator notice, never-turned main, gone parent
//
// WakeSession is a new function. Until it exists every test here fails with
// BLOCKED, naming the spec. The assertions under the probe are the oracle
// for when the method is added; they are not copied from a run of today's code.
//
// Assumed signature, from §9.4's inputs (recipient, reason, canonical outcome
// id, body) plus the context every other wake method takes:
//
//	func (al *AgentLoop) WakeSession(ctx context.Context, recipientSessionID, reason, outcomeID, body string) error
//
// A (result, error) pair is also accepted. Any other shape fails BLOCKED so
// the pack is updated in one place rather than papering over a mismatch.
//
// Step 4 items 2–3 are deferred to CHECK.
package agent

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestWakeSession_RejectsUnknownReason(t *testing.T) {
	al, cleanup := newPWWakeTestLoop(t)
	defer cleanup()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "main", testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, reason := range []string{"idle_nudge", ""} {
		wakes := pwObserveNotifier(t, al)
		err := pwWakeSession(t, al, meta.ID, reason, "outcome-unknown", "body")
		if err == nil {
			t.Fatalf("WakeSession(reason %q) error = nil, want an error and no turn (FR-018 / PW-D10)", reason)
		}
		if got := wakes(); got != 0 {
			t.Fatalf("WakeSession(reason %q) published %d notifier events, want 0", reason, got)
		}
	}
}

func TestWakeSession_CreatorNoticeOneWebchatTurn(t *testing.T) {
	al, cleanup := newPWWakeTestLoop(t)
	defer cleanup()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "main", testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	wakes := pwObserveNotifier(t, al)
	if err = pwWakeSession(t, al, meta.ID, "creator_notice", "run-1", "the scheduled task finished"); err != nil {
		t.Fatalf("WakeSession(creator_notice): %v", err)
	}
	after, err := al.GetSessionStore().GetMeta(meta.ID)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if after.Channel != "main" {
		t.Fatalf("stored channel = %q, want main — creator_notice must not rewrite it (FR-001 / Test 32)", after.Channel)
	}
	if got := wakes(); got != 1 {
		t.Fatalf("webchat notifier events = %d, want 1 turn on webchat (PW-D8 / FR-017)", got)
	}
	pwDrivePublishedWake(t, al, meta.ID)
}

func TestWakeSession_NeverTurnedMain(t *testing.T) {
	al, cleanup := newPWWakeTestLoop(t)
	defer cleanup()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "main", testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err = al.GetSessionLifecycleStore().Load(meta.ID); err == nil {
		t.Fatal("setup: a never-turned main must not already have a lifecycle record")
	}
	wakes := pwObserveNotifier(t, al)
	if err = pwWakeSession(t, al, meta.ID, "finished", "child-1:1:final", "the helper finished"); err != nil {
		t.Fatalf("WakeSession(never-turned): %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(meta.ID)
	if err != nil || rec == nil {
		t.Fatalf("ordinary root record after wake = %v, err %v — an eligible main with no record is recorded, then woken once (FR-017 / Test 18)", rec, err)
	}
	if got := wakes(); got != 1 {
		t.Fatalf("notifier events = %d, want 1", got)
	}
	pwDrivePublishedWake(t, al, meta.ID)

	helper, err := al.GetSessionStore().NewSession(session.SessionTypeDelegate, "webchat", testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession helper: %v", err)
	}
	if err := pwWakeSession(t, al, helper.ID, "finished", "not-a-root", "no"); err == nil {
		t.Fatal("WakeSession on a helper id returned nil, want an error — a helper is not an ordinary root (Test 18)")
	}
}

func TestWakeSession_GoneParentNoSubstitute(t *testing.T) {
	al, cleanup := newPWWakeTestLoop(t)
	defer cleanup()
	before, err := al.GetSessionStore().ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	wakes := pwObserveNotifier(t, al)
	if err = pwWakeSession(t, al, "gone-parent-pw", "finished", "child:1:final", "the helper finished"); err == nil {
		t.Fatal("WakeSession(gone parent) error = nil, want an error and no substitute session (PW-D12 / FR-019)")
	}
	after, err := al.GetSessionStore().ListSessions()
	if err != nil {
		t.Fatalf("ListSessions after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("session count %d → %d, want no new session when the parent is gone", len(before), len(after))
	}
	if got := wakes(); got != 0 {
		t.Fatalf("notifier events = %d, want 0 — a gone parent is not replaced", got)
	}
}

func TestHelperEventId_RepeatIsOneRow(t *testing.T) {
	al, cleanup := newPWWakeTestLoop(t)
	defer cleanup()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "main", testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	const outcomeID = "child-repeat:1:final"
	if err := pwWakeSession(t, al, meta.ID, "finished", outcomeID, "first finish"); err != nil {
		t.Fatalf("first WakeSession: %v", err)
	}
	if err := pwWakeSession(t, al, meta.ID, "finished", outcomeID, "first finish"); err != nil {
		t.Fatalf("repeat WakeSession: %v", err)
	}
	rows := pwRowsContaining(t, al, meta.ID, "helper_event")
	if len(rows) != 1 {
		t.Fatalf("rows for %s = %d, want 1 — the same source id is one append (FR-022 / Test 4)", outcomeID, len(rows))
	}
}

// pwWakeSession calls AgentLoop.WakeSession. A missing method is a loud
// BLOCKED failure, never a skip.
func pwWakeSession(t *testing.T, al *AgentLoop, sessionID, reason, outcomeID, body string) error {
	t.Helper()
	m := reflect.ValueOf(al).MethodByName("WakeSession")
	if !m.IsValid() {
		t.Fatalf("BLOCKED: AgentLoop.WakeSession(ctx, recipientSessionID, reason, outcomeID, body string) error not implemented — required by parent-wake-visibility-spec.md §9.4 / FR-017")
	}
	mt := m.Type()
	if mt.NumIn() != 5 || (mt.NumOut() != 1 && mt.NumOut() != 2) {
		t.Fatalf("BLOCKED: AgentLoop.WakeSession has signature %s; RED pack assumes (context.Context, string, string, string, string) (error) or (result, error) — required by §9.4", mt)
	}
	outs := m.Call([]reflect.Value{
		reflect.ValueOf(context.Background()),
		reflect.ValueOf(sessionID),
		reflect.ValueOf(reason),
		reflect.ValueOf(outcomeID),
		reflect.ValueOf(body),
	})
	last := outs[mt.NumOut()-1]
	if !last.IsValid() || last.IsNil() {
		return nil
	}
	err, ok := last.Interface().(error)
	if !ok {
		t.Fatalf("BLOCKED: WakeSession's last result is %T, want error", last.Interface())
	}
	return err
}

// pwObserveNotifier counts notifier publications whose stream channel is webchat.
// The spec's idle wake streams on webchat; a bus publish on channel system is
// not this count.
func pwObserveNotifier(t *testing.T, al *AgentLoop) func() int {
	t.Helper()
	if al.asyncNotifier == nil {
		t.Fatal("setup: async notifier is not wired")
	}
	var n atomic.Int64
	al.asyncNotifier.registerObserver(func(ev AsyncNotifyEvent) {
		if ev.Channel == "webchat" {
			n.Add(1)
		}
	})
	return func() int { return int(n.Load()) }
}

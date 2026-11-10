// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the #890 follow-up: LifecycleRecord.Stopped() (pkg/session/
// lifecycle_edge.go) is defined today as ONLY the live, current-generation
// Stop fence (r.Stop != nil && r.Stop.Generation == r.Generation). It does
// NOT recognize a record that has already LANDED session.LifecycleStopped
// with its fence cleared — the normal outcome once a cancelled/timed-out
// turn finishes unwinding (pkg/session/lifecycle_bridge.go::TransitionSession
// clears rec.Stop the moment it lands LifecycleStopped). ADR-20260928-sub-
// agent-control-plane.md line ~636 documents the intended fix: "Stopped()
// checks landed state OR current fence".
//
// This file is RED for call site #1 of 6:
// pkg/tools/delegate_followup.go::executeSteer, line ~168:
//
//	if rec.Terminal() || rec.Stopped() { ... revive via steerReviver ... }
//
// For a landed-and-cleared record, rec.Terminal() is false (LifecycleStopped
// is deliberately non-terminal, pkg/session/lifecycle.go's
// terminalLifecycleStates) and rec.Stopped() is ALSO currently false (no
// live fence), so executeSteer takes the wrong branch: it silently delivers
// the steer message into a steering queue with no live turn left to drain
// it, instead of reviving the session — exactly the "false success" Finding
// 1 (delegate_followup.go's own doc comment) was written to close for the
// live-fence case, now reopened for the landed-and-cleared case.
//
// Production code is untouched here — RED only (elicify-test-writing skill,
// qa-lead RED duty).

package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// fakeReviverSink is fakeSteeringSink (delegate_adr053_test.go) plus
// ReviveStoppedSession, so it satisfies BOTH DelegateSteeringSink (the
// normal enqueue path) and steerReviver (the type assertion executeSteer
// makes at delegate_followup.go:172 when it decides the target is
// terminal-or-stopped). This lets the test tell, from the OUTSIDE, which
// branch executeSteer actually took: a delivered message on the plain
// queue (wrong, for a landed-and-cleared record) versus a recorded revive
// call (right).
type fakeReviverSink struct {
	mu                sync.Mutex
	delivered         []providers.Message
	scopes            []string
	reviveCalled      bool
	reviveSessionID   string
	reviveInstruction string
}

func (f *fakeReviverSink) EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, msg)
	f.scopes = append(f.scopes, scope)
	return "corr_test", nil
}

func (f *fakeReviverSink) ReviveStoppedSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviveCalled = true
	f.reviveSessionID = sessionID
	f.reviveInstruction = instruction
	return true, nil
}

func (f *fakeReviverSink) snapshot() (revived bool, sessionID, instruction string, deliveredCount int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reviveCalled, f.reviveSessionID, f.reviveInstruction, len(f.delivered)
}

// TestDelegateTool_Steer_LandedAndClearedStoppedChild_Revives is RED for
// delegate_followup.go::executeSteer's terminal-or-stopped branch (line
// ~168). Oracle: ADR-20260928-sub-agent-control-plane.md line ~636's
// intended Stopped() redefinition, plus the ADR-093/Finding-1 doc comments
// on executeSteer itself, which already say a stopped session must be
// REVIVED, never silently enqueued into a queue nothing drains. The
// expected value here — "the reviver is called, not the plain sink" — comes
// from that spec text, not from running the current (buggy) code.
func TestDelegateTool_Steer_LandedAndClearedStoppedChild_Revives(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	frs := &fakeReviverSink{}
	tool.SetSteeringSink(frs)

	// The exact "landed and cleared" shape lifecycle_bridge.go::
	// TransitionSession leaves once a stop lands: State == LifecycleStopped,
	// a persisted StopNote (persistLocked requires one), and Stop == nil
	// (TransitionSession clears the current-generation fence the moment it
	// lands the stopped state — see its own doc comment "landing clears the
	// fence but keeps the note").
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID:      "child-landed-stopped",
		Generation:     1,
		State:          session.LifecycleStopped,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1",
		AgentID:        "worker",
		StopNote: &session.StopNote{
			At:    time.Now(),
			By:    "human:tester",
			Seq:   1,
			Cause: session.StopCauseTimeout,
		},
	}); err != nil {
		t.Fatalf("seed landed-and-cleared stopped lifecycle record: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-landed-stopped", "text": "resume, please",
	})
	if result.IsError {
		t.Fatalf("delegate(steer) on a landed-and-cleared stopped child was refused: %s", result.ForLLM)
	}

	revived, sessionID, instruction, deliveredCount := frs.snapshot()
	if !revived {
		t.Fatalf("executeSteer did not call ReviveStoppedSession for a landed-and-cleared stopped child (delivered=%d messages via the plain steering queue instead) — "+
			"delegate_followup.go:168's `rec.Terminal() || rec.Stopped()` check does not recognize state=%q with the current-generation fence already cleared as stopped, "+
			"so the steer message was silently enqueued into a steering queue with no live turn left to drain it (ADR-20260928-sub-agent-control-plane.md line ~636: "+
			"Stopped() must check landed state OR current fence)", deliveredCount, session.LifecycleStopped)
	}
	if sessionID != "child-landed-stopped" {
		t.Fatalf("ReviveStoppedSession called with session %q, want %q", sessionID, "child-landed-stopped")
	}
	if instruction != "resume, please" {
		t.Fatalf("ReviveStoppedSession called with instruction %q, want %q", instruction, "resume, please")
	}
}

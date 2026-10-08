// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the 2026-10-04 founder contract ("a stopped helper is paused,
// not failed"): the IN-FLIGHT STOP FENCE row of steer/respond.
//
// LifecycleRecord.Stopped() (pkg/session/lifecycle_edge.go) is true both for
// a LANDED stop (state LifecycleStopped, fence cleared) and for a fence
// still IN FLIGHT (state running/queued, Stop stamped for the CURRENT
// generation). executeSteer (pkg/tools/delegate_followup.go) and
// deliverNative (pkg/tools/delegate_respond.go) branch on
// `rec.Terminal() || rec.Stopped()` off their plain Load — so both take the
// REVIVE branch for an in-flight fence too. Reviving there re-queues a
// generation the old, still-registered turn then refuses, stranding the
// record queued with nobody running it.
//
// The contract for the fence shape: the call returns a VISIBLE error telling
// the caller to retry once the stop has landed; it must NOT call
// ReviveStoppedSession and must NOT enqueue into the steering queue.
//
// (The landed-and-cleared shape is the separate, already-pinned row:
// TestDelegateTool_Steer_LandedAndClearedStoppedChild_Revives and
// TestDelegateTool_Respond_StoppedChild_ResumesSameConversationViaReviver.
// An OLDER-generation Stop marker is inert history, not this case — the
// dispatch says not to assert it unless already covered; it is not.)
//
// Production code is untouched here — RED only (elicify-test-writing skill,
// qa-lead RED duty).

package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// seedFencedChild persists a native child RUNNING with a stop fence stamped
// for its CURRENT generation — the exact shape SteerCanceller.StopTurns
// leaves while the cooperative stop is still unwinding: the old turn is
// still registered, the stop has not landed, no consumer has gone away.
func seedFencedChild(t *testing.T, lc *session.LifecycleStore, sessionID string) {
	t.Helper()
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID:      sessionID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1",
		AgentID:        "worker",
		Stop: &session.Stop{
			At:         time.Now(),
			Generation: 1,
			By:         session.Principal{Kind: session.PrincipalKindHuman, ID: "human:tester"},
		},
		// No StopNote: the stop has not LANDED — D2/CRIT-001 requires a note
		// only on a record landing LifecycleStopped.
	}); err != nil {
		t.Fatalf("seed in-flight-fence child %s: %v", sessionID, err)
	}
}

// TestDelegateTool_SteerAndRespond_InFlightStopFence_VisibleRetryErrorNoReviveNoEnqueue
// pins the in-flight-fence row for BOTH actions. Oracle: the founder
// contract of 2026-10-04 — a stop fence still in flight is neither queueable
// nor revivable; the caller is told to retry once the stop has landed.
// Expected values come from that contract, not from running the current
// (buggy) code.
func TestDelegateTool_SteerAndRespond_InFlightStopFence_VisibleRetryErrorNoReviveNoEnqueue(t *testing.T) {
	cases := []struct {
		name   string
		action string
		args   func(sessionID string) map[string]any
	}{
		{
			name:   "steer",
			action: "steer",
			args: func(sessionID string) map[string]any {
				return map[string]any{"action": "steer", "session_id": sessionID, "text": "one more thing"}
			},
		},
		{
			name:   "respond",
			action: "respond",
			args: func(sessionID string) map[string]any {
				return map[string]any{"action": "respond", "session_id": sessionID, "correlation_id": "corr-fence", "text": "yes, ship it"}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const sessionID = "child-inflight-fence"
			tool, lc, inbox, _ := newADR053TestTool(t)
			frs := &fakeReviverSink{}
			tool.SetSteeringSink(frs)
			seedFencedChild(t, lc, sessionID)
			seedOpenQuestion(t, inbox, "parent-1", sessionID, "corr-fence")

			ctx := WithTranscriptSessionID(context.Background(), "parent-1")
			result := tool.Execute(ctx, tc.args(sessionID))

			// 1. A visible error — never a success, never a queued/resumed ack.
			if !result.IsError {
				t.Fatalf("delegate(%s) on an in-flight stop fence reported success (%.80s) — the contract requires a visible error telling the caller to retry once the stop has landed", tc.action, result.ForLLM)
			}
			// 2. The error tells the caller to retry once the stop has landed.
			lower := strings.ToLower(result.ForLLM)
			if !strings.Contains(lower, "retry") {
				t.Errorf("delegate(%s) refusal = %.200s — the caller must be told to RETRY once the stop has landed", tc.action, result.ForLLM)
			}
			if !strings.Contains(lower, "stop") {
				t.Errorf("delegate(%s) refusal = %.200s — the refusal must name the stop the caller is waiting for", tc.action, result.ForLLM)
			}
			// 3. No revive: an early revive re-queues a generation the old,
			// still-registered turn refuses — the stranded-queued shape.
			revived, _, _, delivered := frs.snapshot()
			if revived {
				t.Errorf("delegate(%s) called ReviveStoppedSession for an in-flight stop fence — the old turn is still registered; reviving now strands the record queued with nobody running it", tc.action)
			}
			// 4. Nothing enqueued: a steering queue with no live consumer
			// would strand the message until the record is revived by chance.
			if delivered != 0 {
				t.Errorf("delegate(%s) enqueued %d message(s) into the steering queue of an in-flight-fence child — nothing may be queued for a stop that has not landed", tc.action, delivered)
			}
		})
	}
}

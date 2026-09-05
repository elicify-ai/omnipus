// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_status_criteria_wire_test.go — ADR-074 D5.2 / judgment-first FR-011
// (US-6, test 19's emission half): the `queued` pending-confirm goal_status
// emission carries the compiled criteria breakdown on
// GoalStatusChangedPayload.Criteria, and every other lifecycle emission
// carries none.
package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// waitGoalStatusPayloads polls the async event collector until at least one
// goal_status payload for sid reports the wanted state (event delivery rides
// a subscriber goroutine — reading immediately after the emitting call is a
// race, not a failure). Returns every payload observed so far.
func waitGoalStatusPayloads(t *testing.T, c *eventCollector, sid, wantState string) []GoalStatusChangedPayload {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		payloads := goalStatusPayloadsFor(c, sid)
		for _, p := range payloads {
			if p.State == wantState {
				return payloads
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no goal_status payload with state %q arrived (got %+v)", wantState, payloads)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestGoalStatus_QueuedEmissionCarriesCriteriaBreakdown drives a prose
// `/goal` through the two-phase compile (stubbed LLM) and asserts the
// `queued` frame payload itemizes the compiled criteria — the data the SPA's
// GoalThreadTailCards/GoalEchoCard confirmation surface renders — while the
// post-confirm `active` emission stays breakdown-free.
func TestGoalStatus_QueuedEmissionCarriesCriteriaBreakdown(t *testing.T) {
	al, agentInst, _, _, sid, opts := twoPhaseHarness(t,
		func(_ int, _ []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the launch post is written", "the changelog is complete"), nil
		}, nil)
	allowBashPolicy(agentInst) // [tests] compiles a bash-run check criterion

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	setGoal(t, al, agentInst, opts, "write the launch post [tests]")

	payloads := waitGoalStatusPayloads(t, c, sid, goalPillQueued)
	queued := payloads[len(payloads)-1]
	if queued.State != goalPillQueued {
		t.Fatalf("last emission state = %q, want %q", queued.State, goalPillQueued)
	}
	if len(queued.Criteria) != 3 {
		t.Fatalf("queued emission carries %d criteria, want 3 (2 LLM prose + 1 marker check): %+v",
			len(queued.Criteria), queued.Criteria)
	}
	var prose, check int
	var checkCmd string
	for _, cr := range queued.Criteria {
		switch cr.Kind {
		case task.KindProse:
			prose++
		case task.KindCheck:
			check++
			if cr.Check != nil {
				checkCmd = cr.Check.Command
			}
		}
		if cr.Text == "" {
			t.Errorf("criterion %s has empty text on the wire payload", cr.ID)
		}
	}
	if prose != 2 || check != 1 {
		t.Fatalf("breakdown = %d prose + %d check, want 2 prose + 1 check", prose, check)
	}
	// FR-113 substance: the marker check's literal command rides the payload
	// verbatim (the [tests] marker's documented deterministic expansion).
	if checkCmd != "go test ./..." {
		t.Errorf("check criterion command = %q, want the [tests] expansion verbatim", checkCmd)
	}

	// --- Confirm → the activation emission carries NO breakdown ---
	activatePendingGoal(t, al, agentInst, opts)
	payloads = waitGoalStatusPayloads(t, c, sid, goalPillActive)
	last := payloads[len(payloads)-1]
	if last.State != goalPillActive {
		t.Fatalf("post-confirm state = %q, want %q", last.State, goalPillActive)
	}
	if len(last.Criteria) != 0 {
		t.Errorf("active emission carries %d criteria, want 0 (breakdown rides the queued emission only)",
			len(last.Criteria))
	}
}

// Omnipus — goal_claim result-encoding failure path (silent-failure finding SF-7).
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// SF-7: when the result payload failed to encode, Execute logged at Error and
// returned a SUCCESS result whose body was the prose "goal_claim: status met
// recorded". Two readers were misled at once. The MODEL read "recorded",
// believed its claim had landed, and stopped working. The ENGINE reads that
// same transcript entry for the JSON payload that identifies the claim
// (JUDGE-FR-092/094) — it found prose, resolved no claim, scheduled no
// adjudication — and the goal then waited forever with nobody aware anything
// had failed. Nothing was recorded: the result IS the record.
//
// The failure branch is unreachable in production (json.Marshal cannot fail
// for this payload), which is precisely why it went unnoticed. goalClaimMarshal
// exists as the seam that makes it reachable from a test — see its doc comment.

package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// withFailingGoalClaimMarshal swaps the encoder for one that always fails, and
// restores the real one when the test ends.
func withFailingGoalClaimMarshal(t *testing.T, err error) {
	t.Helper()
	prev := goalClaimMarshal
	goalClaimMarshal = func(any) ([]byte, error) { return nil, err }
	t.Cleanup(func() { goalClaimMarshal = prev })
}

// TestGoalClaim_EncodeFailure_ReturnsFailureNotFakeSuccess is the SF-7 oracle.
// Note it does NOT merely assert IsError — it also asserts the body is not the
// old "recorded" sentence, because the exact defect was a truthful-looking
// success message.
func TestGoalClaim_EncodeFailure_ReturnsFailureNotFakeSuccess(t *testing.T) {
	const sessionID = "session_claim_encode_fail"

	for _, status := range []string{GoalClaimStatusMet, GoalClaimStatusBlocked, GoalClaimStatusWaitingOnUser} {
		t.Run(status, func(t *testing.T) {
			access := newFakeGoalRecordAccess()
			access.condition[sessionID] = "ship the feature"
			tool := newGoalClaimTool(access)

			boom := errors.New("json: unsupported value")
			withFailingGoalClaimMarshal(t, boom)

			args := map[string]any{"status": status}
			if status == GoalClaimStatusMet {
				args["evidence"] = "I ran the suite and it passed"
			}
			res := tool.Execute(setGoalCtx(sessionID, "mia"), args)

			if !res.IsError {
				t.Fatalf("a claim that could not be encoded must be an ERROR result, got success: %q", res.ForLLM)
			}
			if strings.Contains(res.ForLLM, "recorded") && !strings.Contains(res.ForLLM, "NOT be recorded") {
				t.Fatalf("the result must not tell the model its claim was recorded: %q", res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "goal_claim") {
				t.Fatalf("the failure must name the tool so the model knows what to retry: %q", res.ForLLM)
			}
			if !strings.Contains(strings.ToLower(res.ForLLM), "call goal_claim again") {
				t.Fatalf("the failure must tell the model the recovery action: %q", res.ForLLM)
			}
			if res.Err == nil || !errors.Is(res.Err, boom) {
				t.Fatalf("the result must carry the underlying encode error, got %v", res.Err)
			}
			// The body must NOT be parsable as a claim payload — the engine
			// must not be able to mistake this failure for a real claim.
			var parsed map[string]any
			if err := json.Unmarshal([]byte(res.ForLLM), &parsed); err == nil {
				if _, hasStatus := parsed["status"]; hasStatus {
					t.Fatalf("a failed claim must not produce a parsable claim payload: %q", res.ForLLM)
				}
			}
		})
	}
}

// TestGoalClaim_EncodeSuccess_ReturnsParsablePayload is the acceptance pairing
// (Binding Rule 4): with the real encoder in place the very same call produces
// the JSON payload the engine resolves the claim from. Without this, the fix
// above could be satisfied by making goal_claim always fail.
func TestGoalClaim_EncodeSuccess_ReturnsParsablePayload(t *testing.T) {
	const sessionID = "session_claim_encode_ok"
	access := newFakeGoalRecordAccess()
	access.condition[sessionID] = "ship the feature"
	access.goalID[sessionID] = "goal-abc"
	tool := newGoalClaimTool(access)

	res := tool.Execute(setGoalCtx(sessionID, "mia"), map[string]any{
		"status": GoalClaimStatusMet, "evidence": "I ran the suite and it passed",
	})
	if res.IsError {
		t.Fatalf("a well-formed met claim must succeed, got: %q", res.ForLLM)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &parsed); err != nil {
		t.Fatalf("the result body must be the JSON claim payload the engine parses, got %q: %v", res.ForLLM, err)
	}
	if parsed["status"] != GoalClaimStatusMet {
		t.Fatalf("payload status = %v, want %q", parsed["status"], GoalClaimStatusMet)
	}
	if parsed["evidence"] != "I ran the suite and it passed" {
		t.Fatalf("payload evidence = %v, want the caller's evidence", parsed["evidence"])
	}
	if parsed["goal_id"] != "goal-abc" {
		t.Fatalf("payload goal_id = %v, want %q", parsed["goal_id"], "goal-abc")
	}
}

// Omnipus — goal_claim tool tests (ADR-084 revision 9 §O, D12; JUDGE-FR-087,
// FR-088, FR-090).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// newGoalClaimTool wires a GoalClaimTool over access exactly the way
// newSetGoalTool (set_goal_test.go, same package) wires SetGoalTool — the
// two tools deliberately share GoalRecordAccess (see goal_claim.go's file
// doc comment), so they also deliberately share the same in-memory double
// (fakeGoalRecordAccess) and the same goalless/delegated-session ctx helper
// (setGoalCtx) rather than this file reinventing either.
func newGoalClaimTool(access GoalRecordAccess) *GoalClaimTool {
	return NewGoalClaimTool(func() GoalRecordAccess { return access })
}

// TestGoalClaimTool_SchemaAndEnum — JUDGE-FR-087: status enum is exactly
// [met, blocked, waiting_on_user]; evidence is a string; the description
// states that a met claim adjudicates after delivery and may disagree
// later.
func TestGoalClaimTool_SchemaAndEnum(t *testing.T) {
	tool := newGoalClaimTool(newFakeGoalRecordAccess())

	if got := tool.Name(); got != "goal_claim" {
		t.Fatalf("Name() = %q, want %q", got, "goal_claim")
	}
	if got := tool.Scope(); got != ScopeGeneral {
		t.Fatalf("Scope() = %q, want %q", got, ScopeGeneral)
	}

	params := tool.Parameters()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters()[\"properties\"] is not a map: %#v", params["properties"])
	}

	statusProp, ok := props["status"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters() has no \"status\" property: %#v", props)
	}
	enumRaw, ok := statusProp["enum"].([]string)
	if !ok {
		t.Fatalf("status.enum is not a []string: %#v", statusProp["enum"])
	}
	wantEnum := []string{"met", "blocked", "waiting_on_user"}
	if len(enumRaw) != len(wantEnum) {
		t.Fatalf("status.enum = %v, want exactly %v", enumRaw, wantEnum)
	}
	for i, v := range wantEnum {
		if enumRaw[i] != v {
			t.Fatalf("status.enum = %v, want exactly %v (index %d mismatch)", enumRaw, wantEnum, i)
		}
	}

	evidenceProp, ok := props["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters() has no \"evidence\" property: %#v", props)
	}
	if evidenceProp["type"] != "string" {
		t.Fatalf("evidence.type = %v, want \"string\"", evidenceProp["type"])
	}

	required, ok := params["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "status" {
		t.Fatalf("Parameters()[\"required\"] = %#v, want exactly [\"status\"]", params["required"])
	}

	desc := tool.Description()
	if !strings.Contains(desc, "after your reply is delivered") && !strings.Contains(desc, "AFTER your reply is delivered") {
		t.Fatalf("Description() must state a met claim adjudicates AFTER delivery: %q", desc)
	}
	if !strings.Contains(desc, "may disagree") {
		t.Fatalf("Description() must state a verdict may disagree with the claim: %q", desc)
	}
	if !strings.Contains(strings.ToLower(desc), "waiting_on_user") || !strings.Contains(strings.ToLower(desc), "blocked") {
		t.Fatalf("Description() must state waiting_on_user and blocked end the turn without a verdict: %q", desc)
	}
}

// TestGoalClaim_WhitespaceOnlyEvidenceIsEmpty — JUDGE-FR-088 (E-28): a
// status:met claim with "", "   " or "\n\t " as evidence is refused at the
// call as an empty-evidence violation, exactly like a wholly absent
// evidence argument — no claim is recorded (access.writes stays 0, since
// GoalClaimTool never writes anything on any path), and the refusal message
// names the violation.
func TestGoalClaim_WhitespaceOnlyEvidenceIsEmpty(t *testing.T) {
	cases := []struct {
		name     string
		evidence any
	}{
		{"absent", nil},
		{"empty string", ""},
		{"spaces only", "   "},
		{"mixed whitespace", "\n\t "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const sessionID = "session_goal_claim_whitespace"
			access := newFakeGoalRecordAccess()
			access.condition[sessionID] = "an active goal"
			tool := newGoalClaimTool(access)

			args := map[string]any{"status": "met"}
			if tc.evidence != nil {
				args["evidence"] = tc.evidence
			}
			res := tool.Execute(setGoalCtx(sessionID, "mia"), args)

			if !res.IsError {
				t.Fatalf("want a refusal for evidence %q, got success: %q", tc.evidence, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "evidence") {
				t.Fatalf("error should name the empty-evidence violation: %q", res.ForLLM)
			}
			if access.writes != 0 {
				t.Fatalf("must not write on an empty-evidence refusal, got %d writes", access.writes)
			}
		})
	}
}

// TestGoalClaim_RefusesOnDelegatedSubTurnAndGoallessSession — JUDGE-FR-090:
// mirrors set_goal's own precondition tests
// (TestSetGoalTool_ScopePreconditions) exactly — a delegated sub-turn and a
// goalless session are both refused, before any argument is even parsed,
// and neither refusal writes anything.
func TestGoalClaim_RefusesOnDelegatedSubTurnAndGoallessSession(t *testing.T) {
	t.Run("delegation depth > 0 refuses", func(t *testing.T) {
		const sessionID = "session_goal_claim_deleg"
		access := newFakeGoalRecordAccess()
		access.condition[sessionID] = "an active goal"
		tool := newGoalClaimTool(access)

		ctx := WithDelegationDepth(setGoalCtx(sessionID, "mia"), 1)
		res := tool.Execute(ctx, map[string]any{"status": "waiting_on_user"})
		if !res.IsError {
			t.Fatal("want a refusal at delegation depth > 0")
		}
		if !strings.Contains(res.ForLLM, "owner-session-only") {
			t.Fatalf("error should name the delegation refusal: %q", res.ForLLM)
		}
		if access.writes != 0 {
			t.Fatalf("must not write on a delegation-depth refusal, got %d writes", access.writes)
		}
	})

	t.Run("goalless session refuses", func(t *testing.T) {
		const sessionID = "session_goal_claim_none"
		access := newFakeGoalRecordAccess() // no condition set — goalless
		tool := newGoalClaimTool(access)

		res := tool.Execute(setGoalCtx(sessionID, "mia"), map[string]any{"status": "waiting_on_user"})
		if !res.IsError {
			t.Fatal("want a refusal on a goalless session")
		}
		if !strings.Contains(res.ForLLM, "no active goal") {
			t.Fatalf("error should name the goalless refusal: %q", res.ForLLM)
		}
		if access.writes != 0 {
			t.Fatalf("must not write on a goalless-session refusal, got %d writes", access.writes)
		}
	})

	t.Run("delegation checked before the goalless refusal, matching set_goal's order", func(t *testing.T) {
		// A delegated AND goalless session must still name the delegation
		// refusal — the same ordering set_goal.go's Execute uses (scope
		// preconditions checked in a fixed sequence, delegation first).
		const sessionID = "session_goal_claim_deleg_and_none"
		access := newFakeGoalRecordAccess() // goalless too
		tool := newGoalClaimTool(access)

		ctx := WithDelegationDepth(setGoalCtx(sessionID, "mia"), 2)
		res := tool.Execute(ctx, map[string]any{"status": "blocked"})
		if !res.IsError {
			t.Fatal("want a refusal")
		}
		if !strings.Contains(res.ForLLM, "owner-session-only") {
			t.Fatalf("delegation refusal must win over the goalless refusal: %q", res.ForLLM)
		}
	})
}

// TestGoalClaim_UnwiredStoreFailsClosed asserts the tool refuses, rather
// than panicking or silently succeeding, when constructed with a nil
// accessFn (never Execute()d in the metadata catalog, but a defensive
// closed failure otherwise) — the SetGoalTool precedent this tool mirrors.
func TestGoalClaim_UnwiredStoreFailsClosed(t *testing.T) {
	tool := NewGoalClaimTool(nil)
	res := tool.Execute(setGoalCtx("session_goal_claim_unwired", "mia"), map[string]any{"status": "met", "evidence": "done"})
	if !res.IsError {
		t.Fatal("want a refusal when no access seam is wired")
	}
}

// TestGoalClaim_NoSessionContextRefuses asserts a call with no transcript
// session id in ctx is refused rather than proceeding with an empty id.
func TestGoalClaim_NoSessionContextRefuses(t *testing.T) {
	access := newFakeGoalRecordAccess()
	tool := newGoalClaimTool(access)
	res := tool.Execute(WithAgentID(context.Background(), "mia"), map[string]any{"status": "met", "evidence": "done"})
	if !res.IsError {
		t.Fatal("want a refusal with no session context")
	}
	if access.writes != 0 {
		t.Fatalf("must not write with no session context, got %d writes", access.writes)
	}
}

// TestGoalClaim_InvalidStatusRejected asserts an unrecognised status value
// is rejected, naming the three valid values, and a missing status is
// rejected the same way.
func TestGoalClaim_InvalidStatusRejected(t *testing.T) {
	const sessionID = "session_goal_claim_bad_status"
	access := newFakeGoalRecordAccess()
	access.condition[sessionID] = "an active goal"
	tool := newGoalClaimTool(access)

	t.Run("unrecognised value", func(t *testing.T) {
		res := tool.Execute(setGoalCtx(sessionID, "mia"), map[string]any{"status": "done"})
		if !res.IsError {
			t.Fatal("want a rejection for an invalid status value")
		}
		if !strings.Contains(res.ForLLM, "met") || !strings.Contains(res.ForLLM, "blocked") || !strings.Contains(res.ForLLM, "waiting_on_user") {
			t.Fatalf("rejection should name the three valid values: %q", res.ForLLM)
		}
	})

	t.Run("missing status", func(t *testing.T) {
		res := tool.Execute(setGoalCtx(sessionID, "mia"), map[string]any{})
		if !res.IsError {
			t.Fatal("want a rejection when status is omitted")
		}
	})

	if access.writes != 0 {
		t.Fatalf("must not write on any status rejection, got %d writes", access.writes)
	}
}

// TestGoalClaim_MetWithEvidenceSucceedsAndReportsIt asserts a well-formed
// status:met claim succeeds, is not an error, and its result payload
// carries both the status and the evidence verbatim (trimmed) — the shape
// the engine reads off the transcript to resolve the claim (JUDGE-FR-092/
// FR-094). This is a Go-unit-level check of the tool's own result shape,
// not of what the engine subsequently does with it.
func TestGoalClaim_MetWithEvidenceSucceedsAndReportsIt(t *testing.T) {
	const sessionID = "session_goal_claim_met"
	access := newFakeGoalRecordAccess()
	access.goalID[sessionID] = "goal-abc123"
	access.condition[sessionID] = "ship the thing"
	tool := newGoalClaimTool(access)

	res := tool.Execute(setGoalCtx(sessionID, "mia"), map[string]any{
		"status":   "met",
		"evidence": "  ran the suite, all green  ",
	})
	if res.IsError {
		t.Fatalf("want success, got error: %q", res.ForLLM)
	}
	if access.writes != 0 {
		t.Fatalf("goal_claim must never write a goal record, got %d writes", access.writes)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("result payload is not valid JSON: %v (%q)", err, res.ForLLM)
	}
	if payload["status"] != "met" {
		t.Fatalf("payload[status] = %v, want \"met\"", payload["status"])
	}
	if payload["evidence"] != "ran the suite, all green" {
		t.Fatalf("payload[evidence] = %v, want the trimmed evidence text", payload["evidence"])
	}
	if payload["goal_id"] != "goal-abc123" {
		t.Fatalf("payload[goal_id] = %v, want %q", payload["goal_id"], "goal-abc123")
	}
}

// TestGoalClaim_BlockedAndWaitingOnUserNeedNoEvidence asserts blocked and
// waiting_on_user succeed with no evidence argument at all — FR-088's
// evidence requirement is scoped to status:met only.
func TestGoalClaim_BlockedAndWaitingOnUserNeedNoEvidence(t *testing.T) {
	for _, status := range []string{"blocked", "waiting_on_user"} {
		t.Run(status, func(t *testing.T) {
			sessionID := "session_goal_claim_" + status
			access := newFakeGoalRecordAccess()
			access.condition[sessionID] = "ship the thing"
			tool := newGoalClaimTool(access)

			res := tool.Execute(setGoalCtx(sessionID, "mia"), map[string]any{"status": status})
			if res.IsError {
				t.Fatalf("status:%s with no evidence should succeed, got error: %q", status, res.ForLLM)
			}

			var payload map[string]any
			if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
				t.Fatalf("result payload is not valid JSON: %v (%q)", err, res.ForLLM)
			}
			if payload["status"] != status {
				t.Fatalf("payload[status] = %v, want %q", payload["status"], status)
			}
			if _, present := payload["evidence"]; present {
				t.Fatalf("status:%s must not carry an evidence key, got payload %v", status, payload)
			}
		})
	}
}

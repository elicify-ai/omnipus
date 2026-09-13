// Omnipus — goal_claim Agent Tool
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — goal_claim (ADR-084 revision 9 §O "Completion is claimed
// by a tool call", D12; JUDGE-FR-087 – FR-091). This is the reliable claim
// channel that replaces DETECTION (parsing a worker's prose for markers)
// with ARRIVAL (a tool call the engine either did or did not receive): a
// working agent that believes its active goal is done, blocked, or waiting
// on the operator calls this tool instead of typing `GOAL_STATUS: met` in
// its reply. The prose markers keep working, UNCHANGED, as a fallback
// (FR-091) — this file does not touch, and must never touch,
// task_completion_signal.go's marker parser.
//
// This tool's own job stops at the call boundary: validating the scope
// preconditions (FR-090) and the status/evidence shape (FR-087, FR-088) and
// reporting the claim in its result. What the engine DOES with a recorded
// claim — resolving tool-vs-marker precedence (FR-092), dispatching a
// deferred adjudication after the reply is delivered (D13), refusing a
// second claim while one is already in flight (FR-101), clearing the
// bare-claim streak (FR-091) — is pkg/agent's job, reading this tool's
// result off the transcript. Nothing here calls into pkg/agent: pkg/tools
// cannot import it (pkg/agent already imports pkg/tools — the reverse
// import would cycle), which is exactly why the scope precondition below
// depends on a narrow, late-bound seam rather than the real goal machinery
// directly — reusing set_goal.go's own GoalRecordAccess (its read half),
// the same accessFn a wiring layer already implements over the real session
// store (pkg/agent/goal_record_wiring.go's agentLoopGoalRecordAccess), so a
// single implementation wires both tools with no adapter needed.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// GoalClaimToolName is the catalog name (allStaticToolNames member).
const GoalClaimToolName = "goal_claim"

// goalClaimMarshal encodes Execute's result payload. It is a TEST SEAM, not a
// configuration point: json.Marshal cannot fail for the map[string]string-
// shaped payload this tool builds, so its failure branch is unreachable in
// production and would otherwise be impossible to exercise. An untestable
// failure branch is exactly how SF-7 survived — a failure path that returned
// a SUCCESS result, which no test could catch because no test could reach it.
// Tests swap this (see goal_claim_encode_failure_test.go) to prove the branch
// reports a failure the model can act on.
var goalClaimMarshal = json.Marshal

// The goal_claim `status` enum (JUDGE-FR-087). GoalClaimStatusBlocked has no
// marker fallback and is reachable only through this tool (FR-093) — that
// asymmetry belongs to the engine wave that resolves a claim, not to this
// file, but the three values are declared here because this is where the
// wire enum is defined.
const (
	GoalClaimStatusMet           = "met"
	GoalClaimStatusBlocked       = "blocked"
	GoalClaimStatusWaitingOnUser = "waiting_on_user"
)

// GoalClaimTool implements the goal_claim tool (ADR-084 revision 9 §O, D12).
type GoalClaimTool struct {
	BaseTool
	// accessFn resolves the live GoalRecordAccess per call (late-bound, the
	// SetGoalTool.accessFn precedent — set_goal.go's own doc comment). A nil
	// fn or nil result fails CLOSED: this tool's only precondition check
	// that needs durable state (is there an active goal on this session?)
	// has nothing to consult without it.
	accessFn func() GoalRecordAccess
}

// NewGoalClaimTool constructs a GoalClaimTool. accessFn may be nil for the
// metadata catalog (never Execute()d there); Execute fails closed on a nil/
// unwired access seam, exactly like SetGoalTool.
func NewGoalClaimTool(accessFn func() GoalRecordAccess) *GoalClaimTool {
	return &GoalClaimTool{accessFn: accessFn}
}

func (t *GoalClaimTool) access() GoalRecordAccess {
	if t.accessFn == nil {
		return nil
	}
	return t.accessFn()
}

// Name implements Tool.
func (t *GoalClaimTool) Name() string { return GoalClaimToolName }

// Scope implements Tool. ScopeGeneral — every human-facing agent that can
// carry a goal (core roster, subagent tier that runs owner sessions, custom
// agents) can claim its OWN session's goal; the scope preconditions below
// (delegation depth, active-goal requirement) are the real per-call gate,
// not agent type — mirroring SetGoalTool.Scope's own reasoning exactly.
func (t *GoalClaimTool) Scope() ToolScope { return ScopeGeneral }

// Category implements Tool.
func (t *GoalClaimTool) Category() ToolCategory { return CategoryTasks }

// Description implements Tool.
func (t *GoalClaimTool) Description() string {
	return "Claim THIS session's active goal as done, blocked, or waiting on the operator (ADR-084). " +
		"status:met means you believe the goal's acceptance criteria are satisfied: it starts an " +
		"adjudication that runs AFTER your reply is delivered — the operator sees your response " +
		"immediately, a verdict arrives later on its own, and it may disagree with your claim. A met " +
		"claim requires a non-empty evidence argument (your own one-line statement of what you " +
		"verified) or the call is refused before anything is recorded — no partial credit for trying. " +
		"status:waiting_on_user and status:blocked both end the turn without any adjudication: " +
		"waiting_on_user means you need an answer from the operator to continue; blocked means you " +
		"cannot proceed and it is not something the operator can answer directly. This tool refuses on " +
		"a delegated sub-turn (it reports the OWNER session's completion only) and on a session with no " +
		"active goal."
}

// Parameters implements Tool.
func (t *GoalClaimTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type": "string",
				"enum": []string{GoalClaimStatusMet, GoalClaimStatusBlocked, GoalClaimStatusWaitingOnUser},
				"description": "met: you believe the goal's acceptance criteria are satisfied — starts a " +
					"background adjudication after your reply is delivered; requires evidence. blocked: you " +
					"cannot proceed and it is not a question the operator can answer — parks the goal, no " +
					"adjudication. waiting_on_user: you need an answer from the operator to continue — parks " +
					"the goal, no adjudication. Required — never omit this expecting a default.",
			},
			"evidence": map[string]any{
				"type": "string",
				"description": "Your own one-line statement of what you verified. Required and non-empty " +
					"when status is met (whitespace-only counts as empty and the call is refused); ignored " +
					"for blocked and waiting_on_user.",
			},
		},
		"required": []string{"status"},
	}
}

// Execute implements Tool. See the file doc comment for the seam contract
// and for what this tool does and does not decide.
func (t *GoalClaimTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	access := t.access()
	if access == nil {
		return ErrorResult("goal_claim: no goal-record store is wired on this deployment")
	}

	sessionID := ToolTranscriptSessionID(ctx)
	if sessionID == "" {
		return ErrorResult("goal_claim: no session context — this tool needs a real, store-backed session to claim a goal")
	}

	// --- Scope preconditions (ADR-084 JUDGE-FR-090) — checked BEFORE any
	// argument parsing, mirroring set_goal.go's own precondition ordering
	// exactly: a delegated or goalless caller gets the scope refusal rather
	// than a validation error that might read as "try again with a
	// corrected payload" when no payload could ever succeed here. These are
	// this tool's own preconditions, not tool policy — a policy denial is a
	// different thing (JUDGE-E-29), handled generically by the tool-policy
	// layer before Execute is ever reached. ---
	if depth := ToolDelegationDepth(ctx); depth > 0 {
		return ErrorResult("goal_claim is owner-session-only: a delegated sub-turn cannot claim the " +
			"parent session's goal (ADR-084 JUDGE-FR-090) — report your findings back to the parent instead")
	}

	goalID, goalCondition, _, err := access.ReadGoalState(sessionID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("goal_claim: could not read this session's goal state: %v", err)).WithError(err)
	}
	if strings.TrimSpace(goalCondition) == "" {
		return ErrorResult("goal_claim refuses: this session has no active goal — there is nothing to claim (ADR-084 JUDGE-FR-090)")
	}

	status, sErr := parseGoalClaimStatus(args)
	if sErr != nil {
		return ErrorResult(fmt.Sprintf("goal_claim rejected: %v", sErr))
	}

	// Emptiness is evaluated after Unicode whitespace trimming
	// (strings.TrimSpace already trims every rune unicode.IsSpace reports —
	// JUDGE-E-28), matching FR-026's rule for evidence_quote so the two
	// cannot disagree about what "empty" means.
	evidence := strings.TrimSpace(argString(args, "evidence"))

	// JUDGE-FR-088: a met claim with absent/empty/whitespace-only evidence
	// is refused AT THE CALL — an error result, nothing recorded, no round
	// consumed, no adjudication scheduled. This is what retires the G-4
	// bounce economics for the tool path: the correction happens inside the
	// turn, so the model can retry immediately instead of the engine having
	// to price a bare claim it can only detect after the turn has ended.
	if status == GoalClaimStatusMet && evidence == "" {
		return ErrorResult("goal_claim(status:met) rejected: evidence is required and must be non-empty — " +
			"state, in one line, what you verified (ADR-084 JUDGE-FR-088)")
	}

	logger.InfoCF("goal", "goal_claim call",
		map[string]any{"session_id": sessionID, "status": status, "evidence_present": evidence != ""})

	// The result payload is what the engine reads off the transcript to
	// resolve the claim (JUDGE-FR-092/094) — this tool never calls into
	// pkg/agent itself. evidence is carried only for a met claim: it is
	// destined to become the adjudication's ClaimText (FR-094), and
	// blocked/waiting_on_user never adjudicate, so there is nothing for a
	// stray evidence argument on those paths to mean.
	payload := map[string]any{"status": status}
	if status == GoalClaimStatusMet {
		payload["evidence"] = evidence
	}
	if goalID != "" {
		payload["goal_id"] = goalID
	}
	encoded, encErr := goalClaimMarshal(payload)
	if encErr != nil {
		// SF-7: this used to log at Error and return a SUCCESS result whose
		// body was the prose "goal_claim: status met recorded". That sentence
		// is a lie told to two readers at once. The MODEL reads "recorded",
		// believes its claim landed, and stops working. The ENGINE reads the
		// same transcript entry looking for the JSON payload above — finds
		// none, resolves no claim, schedules no adjudication — and the goal
		// waits forever with nobody aware anything failed.
		//
		// Nothing WAS recorded: this result IS the record (see the payload
		// comment above), so a payload that never serialized means the claim
		// does not exist. Say so, as a failure the model can act on: it can
		// call goal_claim again, and a retry is safe because the tool has
		// written nothing anywhere by this point.
		logger.ErrorCF("goal", "goal_claim: could not encode result payload",
			map[string]any{"session_id": sessionID, "status": status, "error": encErr.Error()})
		return ErrorResult(fmt.Sprintf(
			"goal_claim failed: your %s claim could NOT be recorded (result payload could not be "+
				"encoded: %v) — no adjudication has been scheduled and the goal is unchanged. "+
				"Call goal_claim again.", status, encErr)).WithError(encErr)
	}
	return NewToolResult(string(encoded))
}

// parseGoalClaimStatus reads and validates the required `status` arg.
func parseGoalClaimStatus(args map[string]any) (string, error) {
	raw, present := stringArg(args, "status")
	status := strings.TrimSpace(raw)
	if !present || status == "" {
		return "", fmt.Errorf("status is required (must be %q, %q, or %q)",
			GoalClaimStatusMet, GoalClaimStatusBlocked, GoalClaimStatusWaitingOnUser)
	}
	switch status {
	case GoalClaimStatusMet, GoalClaimStatusBlocked, GoalClaimStatusWaitingOnUser:
		return status, nil
	default:
		return "", fmt.Errorf("status %q is not %q, %q, or %q",
			status, GoalClaimStatusMet, GoalClaimStatusBlocked, GoalClaimStatusWaitingOnUser)
	}
}

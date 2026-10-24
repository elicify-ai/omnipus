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
// preconditions (FR-090), the status enum (FR-087), and direct children
// before reporting the claim in its result. `evidence` is required in the
// tool's INPUT SCHEMA for every status (ADR-084 §13, 2026-10-23, issue
// #1431) — provider-side validation enforces its presence, and this tool
// NEVER refuses a met claim over an empty or whitespace-only evidence line:
// the claim is recorded with the text as given and adjudicated like any
// other (FR-088 as amended). What the engine DOES
// with a recorded claim — resolving tool-vs-marker precedence (FR-092), dispatching a
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

// GoalClaimAccess is the optional claim-side read a GoalRecordAccess may also
// implement: the goal BOUND to the session, for either owner kind, and the
// session bound to a running task. goal_claim prefers ReadClaimableGoal over
// ReadGoalState, whose owner-keyed lookup cannot see a task-owned goal from
// the task run's session. A seam without it (test doubles) falls back to
// ReadGoalState for depth-zero callers; a task run at depth > 0 must have the
// bound-session lookup to prove it owns the session it is claiming for.
type GoalClaimAccess interface {
	ReadClaimableGoal(sessionID string) (goalID, goalCondition string, err error)
	ReadTaskSessionID(taskID string) (string, error)
}

// GoalClaimDirectChildrenAccess is a claim-only read of live direct children.
// It stays separate from GoalRecordAccess so set_goal and its callers do not
// gain a lifecycle-store requirement. The production goal access implements
// it; older in-memory test doubles may omit it.
type GoalClaimDirectChildrenAccess interface {
	DirectNonTerminalChildren(sessionID string) ([]string, error)
}

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
	return "Claim THIS session's active goal as met, blocked, or waiting_on_user (ADR-084). " +
		"Both status and evidence are required on every call; omitting evidence is rejected at dispatch. " +
		"Use met when you believe the acceptance criteria are satisfied. Prefer a one-line summary of " +
		"what you did and how you checked it. A met claim is rejected while any direct child sub-agent " +
		"is queued or actively running; wait for them to finish or cancel them, then claim again. " +
		"Otherwise, the met claim is recorded and judged after your reply is delivered, even with empty " +
		"or whitespace-only evidence. If no physical evidence is available, you can still claim met. " +
		"The Judge decides met or unmet against the acceptance criteria using the conversation and your " +
		"tool results, and may disagree with your claim. An unmet verdict uses one try. " +
		"blocked means you cannot proceed and an operator answer cannot resolve it; waiting_on_user means " +
		"you need an operator answer to continue. Both end the turn without adjudication; give the reason " +
		"as evidence. Only the owner session can claim its active goal."
}

// Parameters implements Tool.
func (t *GoalClaimTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type": "string",
				"enum": []string{GoalClaimStatusMet, GoalClaimStatusBlocked, GoalClaimStatusWaitingOnUser},
				"description": "Required. met: you believe the acceptance criteria are satisfied. If any direct child " +
					"sub-agent is queued or actively running, wait for them to finish or cancel them before claiming met. " +
					"Otherwise, the claim is recorded and judged after your reply is delivered, even with empty evidence. " +
					"An unmet verdict uses one try. blocked: you cannot proceed and an operator answer cannot resolve it. " +
					"waiting_on_user: you need an operator answer to continue. Both park the goal without adjudication. " +
					"Include evidence for every status.",
			},
			"evidence": map[string]any{
				"type": "string",
				"description": "Required string for every status; omitting it is rejected at dispatch. For met, " +
					"prefer one line saying what you did and how you checked it. Empty or whitespace-only evidence " +
					"is accepted: the Judge decides met or unmet against the acceptance criteria using the conversation " +
					"and your tool results. An unmet verdict uses one try. For blocked or waiting_on_user, " +
					"give the reason or the answer you need; this is shown to the operator.",
			},
		},
		// ADR-084 §13 (2026-10-23, issue #1431), decision line 1: `evidence` is
		// required in the input schema for EVERY status — provider-side
		// validation enforces its presence, and Execute never refuses on
		// emptiness (an empty evidence line on a met claim is recorded and
		// adjudicated like any other claim).
		"required": []string{"status", "evidence"},
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
	// A task run carries its task's delegation generation on the context
	// (every agent-created task has generation >= 1), so the depth alone
	// cannot tell a delegated sub-turn from a task's own worker. A task run's
	// turn names its running task; its goal is bound to the run's own session,
	// so the bound-session lookup below still refuses any turn that is not
	// that session's owner.
	const ownerSessionOnly = "goal_claim is owner-session-only: a delegated sub-turn cannot claim " +
		"this session's goal (ADR-084 JUDGE-FR-090) — report your findings back to the parent instead"
	// A steered helper launched with its own goal runs in its own session and
	// owns that goal: the marker names that session, so the exemption holds only
	// for a claim on the session the turn runs in.
	if depth := ToolDelegationDepth(ctx); depth > 0 && ToolOwnGoalSession(ctx) != sessionID {
		taskID := ToolRunningTaskID(ctx)
		if taskID == "" {
			return ErrorResult(ownerSessionOnly)
		}
		claimAccess, ok := access.(GoalClaimAccess)
		if !ok {
			return ErrorResult("goal_claim: running task's bound-session lookup is not wired on this deployment")
		}
		boundSessionID, taskErr := claimAccess.ReadTaskSessionID(taskID)
		if taskErr != nil {
			return ErrorResult(fmt.Sprintf("goal_claim: could not read running task %q: %v", taskID, taskErr)).WithError(taskErr)
		}
		if boundSessionID != sessionID {
			return ErrorResult(ownerSessionOnly)
		}
	}

	var goalID, goalCondition string
	var err error
	if claimAccess, ok := access.(GoalClaimAccess); ok {
		goalID, goalCondition, err = claimAccess.ReadClaimableGoal(sessionID)
	} else {
		goalID, goalCondition, _, err = access.ReadGoalState(sessionID)
	}
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

	// The argument is trimmed after Unicode whitespace trimming
	// (strings.TrimSpace already trims every rune unicode.IsSpace reports —
	// JUDGE-E-28), the same rule FR-026 applies to evidence_quote so the two
	// cannot disagree about what "empty" means. ADR-084 §13 (2026-10-23,
	// issue #1431): trimming now only NORMALISES what the Judge receives and
	// what blocked/waiting_on_user show the operator — it never refuses. The
	// schema requires `evidence` for every status; an empty or
	// whitespace-only evidence line on a met claim is recorded below with the
	// text as given and adjudicated like any other claim.
	evidence := strings.TrimSpace(argString(args, "evidence"))

	if status == GoalClaimStatusMet {
		if direct, ok := access.(GoalClaimDirectChildrenAccess); ok {
			// A child launched at the same instant as this read may publish after
			// the check. This claim/launch race is accepted; do not add a fence.
			children, childErr := direct.DirectNonTerminalChildren(sessionID)
			if childErr != nil {
				return ErrorResult(fmt.Sprintf("goal_claim: could not check direct sub-agents: %v", childErr)).WithError(childErr)
			}
			if len(children) != 0 {
				return ErrorResult(fmt.Sprintf("%d sub-agents are still running: %s. Wait for them to finish or cancel them, then claim again", len(children), strings.Join(children, ",")))
			}
		}
	}

	logger.InfoCF("goal", "goal_claim call",
		map[string]any{"session_id": sessionID, "status": status, "evidence_present": evidence != ""})

	// The result payload is what the engine reads off the transcript to
	// resolve the claim (JUDGE-FR-092/094) — this tool never calls into
	// pkg/agent itself. For a met claim evidence becomes the adjudication's
	// ClaimText (FR-094). For blocked/waiting_on_user, which never
	// adjudicate, the same one line is the REASON the operator is shown
	// (founder decision 2026-09-14, issue #710: a blocked task ends Failed
	// "Blocked: <reason>"); it is carried only when the caller gave one.
	payload := map[string]any{"status": status}
	if status == GoalClaimStatusMet || evidence != "" {
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

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// behavior_scan.go implements the ADR-052 rung-2 ("transcript / behavioral")
// evidence-ladder evaluator (spec FR-034, DS-7). It is a deterministic,
// NO-LLM scan over a session's recorded tool-call log — the session store
// already records each tool invocation per-entry (session.TranscriptEntry.
// ToolCalls, pkg/session/daypartition.go:319-331) — so a `behavior`-kind
// acceptance criterion ("called web_search 5x") can be resolved without a
// verifier dispatch at all (ADR-052 "Three-rung evidence ladder", rung 2;
// spec US-13 Acceptance 4).
//
// Ladder position: (1) machine-check, (2) behavior [this file], (3)
// subjective -> verifier. This file only ever produces a verdict for rung 2;
// it never invokes the verifier and never reads outside the entries it is
// handed.

// BehaviorScope, its two values, and the criterion payload are canonically
// defined in pkg/task (criterion.go — the same package that owns
// KindBehavior; ADR-052 FR-034). This scanner aliases them so its API reads
// naturally in pkg/agent while there is exactly ONE definition of the shape.
type BehaviorScope = task.BehaviorScope

const (
	// BehaviorScopeAttempt restricts counting to tool calls recorded at or
	// after the current attempt's start marker (see ScanBehaviorCriterionEntries's
	// attemptStart parameter) — a retried task's earlier, exhausted attempts
	// do not count toward the current one.
	BehaviorScopeAttempt = task.BehaviorScopeAttempt
	// BehaviorScopeTaskSession counts every tool call recorded anywhere in
	// the session, across all attempts. This is the FR-034 payload default.
	BehaviorScopeTaskSession = task.BehaviorScopeTaskSession
)

// IsValidBehaviorScope reports whether s is a known behavior criterion scope.
func IsValidBehaviorScope(s BehaviorScope) bool {
	return task.IsValidBehaviorScope(s)
}

// BehaviorCriterion is the payload shape of a `behavior`-kind acceptance
// criterion (ADR-052 rung 2 / FR-034): {tool, min_count, max_count?, scope}.
// Canonically task.CriterionBehavior (pkg/task/criterion.go, next to
// KindBehavior) — aliased here so the scanner's API keeps its natural local
// name with exactly ONE definition of the shape.
type BehaviorCriterion = task.CriterionBehavior

// validateBehaviorCriterion reports a non-nil error when c's shape violates
// the FR-034 payload contract: tool required; min_count >= 0 (when set);
// max_count (when set) >= the EFFECTIVE min_count; scope one of the two known
// values. ScanBehaviorCriterionEntries calls this itself and fails the
// criterion closed (unmet) rather than panicking or silently misinterpreting
// a malformed criterion. (pkg/task enforces the same contract at
// decode/store time; re-checking here keeps the scanner fail-closed even for
// callers that construct payloads directly.)
//
// Fix-wave finding #5: MinCount is now *int (pkg/task/criterion.go) — nil
// means "omitted, defaults to 1 via EffectiveMinCount()", an explicit zero
// stays zero. The negative-value check only applies when MinCount is set;
// the max>=min comparison always reads the EFFECTIVE value so a nil
// (not-yet-defaulted) MinCount still validates correctly.
func validateBehaviorCriterion(c BehaviorCriterion) error {
	if strings.TrimSpace(c.Tool) == "" {
		return fmt.Errorf("behavior criterion: tool is required")
	}
	if c.MinCount != nil && *c.MinCount < 0 {
		return fmt.Errorf("behavior criterion: min_count must be >= 0, got %d", *c.MinCount)
	}
	if c.MaxCount != nil && *c.MaxCount < c.EffectiveMinCount() {
		return fmt.Errorf(
			"behavior criterion: max_count (%d) must be >= min_count (%d)", *c.MaxCount, c.EffectiveMinCount(),
		)
	}
	if !IsValidBehaviorScope(c.Scope) {
		return fmt.Errorf(
			"behavior criterion: invalid scope %q (must be %q or %q)",
			c.Scope, BehaviorScopeAttempt, BehaviorScopeTaskSession,
		)
	}
	return nil
}

// BehaviorScanResult is the outcome of evaluating a BehaviorCriterion against
// a session's tool-call log.
type BehaviorScanResult struct {
	// Met is true when the observed successful-call count satisfies the
	// criterion's [min_count, max_count] band.
	Met bool
	// Observed is the number of SUCCESSFUL calls of Criterion.Tool counted
	// within the requested scope. Failed/errored/denied/pending calls are
	// never counted (DS-7 "failed-calls-not-counted").
	Observed int
	// UnknownTool is true when Criterion.Tool never appears in the session's
	// tool-call log AT ALL (under any status), anywhere in the session —
	// independent of scope. This is a fail-closed guard flag (DS-7
	// "unknown-kind guard"): it does not change Met (an absent tool is still
	// correctly evaluated as 0 observed calls), it flags the result as
	// likely referencing a misspelled/nonexistent tool name so a caller can
	// surface a stronger warning than an ordinary unmet count.
	UnknownTool bool
	// Reason is a human-readable explanation of the verdict, including the
	// observed count — suitable for surfacing directly to an operator or
	// feeding back to the worker as steering text.
	Reason string
}

// ScanBehaviorCriterionEntries is the pure, deterministic rung-2 evaluator
// (ADR-052 / FR-034 / DS-7). It counts SUCCESSFUL calls of criterion.Tool
// across entries, restricted to entries at-or-after attemptStart when
// criterion.Scope == BehaviorScopeAttempt (attemptStart is ignored for
// BehaviorScopeTaskSession — the whole session counts).
//
// attemptStart is the current attempt's start marker as a timestamp — the
// engine-side caller (runBehaviorScan, below, in THIS file) is the source of
// truth for when the current attempt began: it resolves the cutoff from the
// task's own Task.StartedAt (task scope only; goal scope has no per-attempt
// cutoff, see runBehaviorScan's doc comment). This function only consumes
// the cutoff, it does not discover it. Passing the zero time.Time when
// scope=="attempt" degenerates to "everything counts" (equivalent to
// task_session) — callers that track attempts should always pass a real
// cutoff.
//
// A malformed criterion (Validate() != nil) fails CLOSED: Met is always
// false, Observed is always 0, and Reason explains why — never a panic,
// never a silent pass.
func ScanBehaviorCriterionEntries(
	entries []session.TranscriptEntry, criterion BehaviorCriterion, attemptStart time.Time,
) BehaviorScanResult {
	if err := validateBehaviorCriterion(criterion); err != nil {
		return BehaviorScanResult{
			Met:    false,
			Reason: fmt.Sprintf("behavior criterion invalid, fail-closed unmet: %s", err),
		}
	}

	everSeen := 0
	observed := 0
	for _, entry := range entries {
		inScope := criterion.Scope == BehaviorScopeTaskSession || !entry.Timestamp.Before(attemptStart)
		for _, tc := range entry.ToolCalls {
			if tc.Tool != criterion.Tool {
				continue
			}
			everSeen++
			if tc.Status != "success" {
				continue // failed/errored/denied/pending calls never count (DS-7)
			}
			if inScope {
				observed++
			}
		}
	}

	// NOTE (fix-wave finding #5, minimal compile fix — see behavior_scan.go's
	// package-level owner for the full ScanBehaviorCriterionEntries function):
	// criterion.MinCount is now *int (pkg/task/criterion.go); EffectiveMinCount()
	// resolves nil to the FR-034 default of 1, matching pkg/task's own
	// validateCriterionBehavior normalization.
	met := observed >= criterion.EffectiveMinCount() && (criterion.MaxCount == nil || observed <= *criterion.MaxCount)
	reason := behaviorScanReason(criterion, observed, met)

	unknownTool := everSeen == 0
	if unknownTool {
		reason += fmt.Sprintf(
			" [unknown-tool guard: %q was never recorded in this session's tool-call log under any status — verify the tool name is correct]",
			criterion.Tool,
		)
	}

	return BehaviorScanResult{Met: met, Observed: observed, UnknownTool: unknownTool, Reason: reason}
}

// runBehaviorScan is JudgeCriteria's rung-2 dispatcher (ADR-052 FR-034): it
// resolves which session's tool-call log the criterion is scanned against —
// task scope: the task's own session (attempt cutoff = Task.StartedAt);
// chat /goal scope: GoalSessionID (whole session; goal rounds have no
// per-attempt cutoff) — reads the transcript, and runs the pure scanner.
// Plan-scope behavior criteria have no single session to scan and fail
// CLOSED with an explicit reason (a plan's DoD should express behavioral
// requirements on its member tasks instead).
func (al *AgentLoop) runBehaviorScan(in JudgeCriteriaInput, c task.AcceptanceCriterion) task.CriterionVerdict {
	failClosed := func(reason string) task.CriterionVerdict {
		return task.CriterionVerdict{CriterionID: c.ID, Met: false, Reason: reason}
	}
	if c.Behavior == nil {
		return failClosed("behavior criterion missing payload (fail-closed)")
	}
	var sessionID string
	var attemptStart time.Time
	switch {
	case in.TaskID != "":
		ts := GetTaskStore(al)
		if ts == nil {
			return failClosed("behavior criterion: no task store available (fail-closed)")
		}
		t, err := ts.Get(in.TaskID)
		if err != nil || t == nil {
			return failClosed("behavior criterion: task not readable (fail-closed)")
		}
		sessionID = t.SessionID
		if criterionScopeIsAttempt(*c.Behavior) && t.StartedAt != "" {
			if cutoff, perr := time.Parse(time.RFC3339, t.StartedAt); perr == nil {
				attemptStart = cutoff
			}
		}
	case in.GoalSessionID != "":
		sessionID = in.GoalSessionID
	default:
		return failClosed("behavior criterion has no session scope to scan — plan-level behavior criteria are not scannable; attach them to member tasks (fail-closed)")
	}
	if sessionID == "" || in.AssigneeAgentID == "" {
		return failClosed("behavior criterion: no session recorded to scan (fail-closed)")
	}
	store := al.GetAgentStore(in.AssigneeAgentID)
	if store == nil {
		return failClosed("behavior criterion: no session store for assignee (fail-closed)")
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		return failClosed(fmt.Sprintf("behavior criterion: could not read session transcript (fail-closed): %s", err))
	}
	res := ScanBehaviorCriterionEntries(entries, *c.Behavior, attemptStart)
	return task.CriterionVerdict{CriterionID: c.ID, Met: res.Met, Reason: res.Reason}
}

// criterionScopeIsAttempt reports whether the payload requests attempt-only
// counting (the non-default scope).
func criterionScopeIsAttempt(c BehaviorCriterion) bool {
	return c.Scope == BehaviorScopeAttempt
}

// behaviorScanReason renders the human-readable verdict explanation shared by
// both the met and unmet paths of ScanBehaviorCriterionEntries.
func behaviorScanReason(c BehaviorCriterion, observed int, met bool) string {
	// Fix-wave finding #5, minimal compile fix (c.MinCount is now *int).
	bound := fmt.Sprintf("min_count=%d", c.EffectiveMinCount())
	if c.MaxCount != nil {
		bound += fmt.Sprintf(", max_count=%d", *c.MaxCount)
	}
	verdict := "unmet"
	if met {
		verdict = "met"
	}
	return fmt.Sprintf(
		"behavior criterion %s: observed %d successful call(s) of %q in scope %q (%s)",
		verdict, observed, c.Tool, c.Scope, bound,
	)
}

// ScanBehaviorCriterion is the session-store-backed entry point: given a
// session id, it reads that session's full tool-call log via store and
// evaluates criterion against it (ADR-052 rung 2 / FR-034). It is a thin
// wrapper over ScanBehaviorCriterionEntries — see that function for the
// scanning semantics; this wrapper only adds the session read.
func ScanBehaviorCriterion(
	store *session.PartitionStore, sessionID string, criterion BehaviorCriterion, attemptStart time.Time,
) (BehaviorScanResult, error) {
	entries, err := store.ReadMessages(sessionID)
	if err != nil {
		return BehaviorScanResult{}, fmt.Errorf("behavior scan: read session %q: %w", sessionID, err)
	}
	return ScanBehaviorCriterionEntries(entries, criterion, attemptStart), nil
}

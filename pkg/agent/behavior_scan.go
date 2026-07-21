// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
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

// BehaviorScope selects which portion of a session's tool-call log a
// BehaviorCriterion is evaluated against (FR-034 payload field `scope`).
type BehaviorScope string

const (
	// BehaviorScopeAttempt restricts counting to tool calls recorded at or
	// after the current attempt's start marker (see ScanBehaviorCriterionEntries's
	// attemptStart parameter) — a retried task's earlier, exhausted attempts
	// do not count toward the current one.
	BehaviorScopeAttempt BehaviorScope = "attempt"
	// BehaviorScopeTaskSession counts every tool call recorded anywhere in
	// the session, across all attempts. This is the FR-034 payload default.
	BehaviorScopeTaskSession BehaviorScope = "task_session"
)

// IsValidBehaviorScope reports whether s is a known behavior criterion scope.
func IsValidBehaviorScope(s BehaviorScope) bool {
	return s == BehaviorScopeAttempt || s == BehaviorScopeTaskSession
}

// BehaviorCriterion is the payload shape of a `behavior`-kind acceptance
// criterion (ADR-052 rung 2 / FR-034): {tool, min_count, max_count?, scope}.
//
// WAVE-MERGE: use task.BehaviorCriterion — a sibling Wave 1 agent is adding
// task.KindBehavior + the matching payload struct to pkg/task/criterion.go
// (mirroring task.CriterionCheck's shape for task.KindCheck). This type is
// intentionally a local, dependency-free mirror of that same shape so this
// scanner can be implemented and tested in isolation; reconcile the two at
// merge (replace this type's use-sites with task.BehaviorCriterion, keep the
// scanning logic below).
type BehaviorCriterion struct {
	// Tool is the exact tool name the criterion counts calls of (required).
	Tool string
	// MinCount is the minimum number of SUCCESSFUL calls required to meet
	// the criterion. FR-034 payload default is 1; MinCount == 0 combined
	// with a non-nil MaxCount == 0 expresses "never call this tool".
	MinCount int
	// MaxCount, when non-nil, is the maximum number of successful calls
	// allowed before the criterion is violated (exceeding it is unmet, not
	// an error).
	MaxCount *int
	// Scope selects attempt-only or whole-task-session counting. FR-034
	// payload default is BehaviorScopeTaskSession.
	Scope BehaviorScope
}

// Validate reports a non-nil error when c's shape violates the FR-034
// payload contract: tool required; min_count >= 0; max_count (when set) >=
// min_count; scope one of the two known values. ScanBehaviorCriterionEntries
// calls this itself and fails the criterion closed (unmet) rather than
// panicking or silently misinterpreting a malformed criterion.
func (c BehaviorCriterion) Validate() error {
	if strings.TrimSpace(c.Tool) == "" {
		return fmt.Errorf("behavior criterion: tool is required")
	}
	if c.MinCount < 0 {
		return fmt.Errorf("behavior criterion: min_count must be >= 0, got %d", c.MinCount)
	}
	if c.MaxCount != nil && *c.MaxCount < c.MinCount {
		return fmt.Errorf(
			"behavior criterion: max_count (%d) must be >= min_count (%d)", *c.MaxCount, c.MinCount,
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
// engine-side caller (task_executor.go, wired at merge) is the source of
// truth for when the current attempt began; this function only consumes the
// cutoff, it does not discover it. Passing the zero time.Time when
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
	if err := criterion.Validate(); err != nil {
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

	met := observed >= criterion.MinCount && (criterion.MaxCount == nil || observed <= *criterion.MaxCount)
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

// behaviorScanReason renders the human-readable verdict explanation shared by
// both the met and unmet paths of ScanBehaviorCriterionEntries.
func behaviorScanReason(c BehaviorCriterion, observed int, met bool) string {
	bound := fmt.Sprintf("min_count=%d", c.MinCount)
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

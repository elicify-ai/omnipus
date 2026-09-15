// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// validateClaim checks a Claim's shape: Status must be one of the three
// generated.GoalLatestClaimStatus values, and Evidence is required and
// non-empty (whitespace-trimmed) when Status == met (JUDGE's own
// machine-verifiable constraint, mirrored on Goal.yaml's latest_claim
// description).
func validateClaim(c *Claim) error {
	if c == nil {
		return fmt.Errorf("goal: validate claim: nil claim")
	}
	if !c.Status.Valid() {
		return fmt.Errorf("goal: claim: invalid status %q", c.Status)
	}
	if c.Status == generated.GoalLatestClaimStatusMet && strings.TrimSpace(c.Evidence) == "" {
		return fmt.Errorf("goal: claim: evidence is required and must be non-empty when status is %q", c.Status)
	}
	return nil
}

// RecordClaim sets g.LatestClaim from a goal_claim tool call (JUDGE §D12's
// reliable claim channel) and bumps LastActivityAt. It does not itself
// touch State, Round or AttemptsUsed — a claim is not yet an adjudication;
// the engine wave that dispatches the claim to the Judge calls RecordVerdict
// once that adjudication completes.
func (g *Goal) RecordClaim(status generated.GoalLatestClaimStatus, evidence string, now time.Time) error {
	claim := &Claim{Status: status, Evidence: evidence, ClaimedAt: now}
	if err := validateClaim(claim); err != nil {
		return fmt.Errorf("goal: record claim %q: %w", g.GoalID, err)
	}
	g.LatestClaim = claim
	g.LastActivityAt = now
	return nil
}

// RecordVerdict records a completed Judge adjudication: it stores v as
// g.LatestVerdict, sets LatestReason (the steering text fed forward per the
// evaluator-optimizer pattern), advances Round by one (one round = one
// adjudication, claim-triggered or idle-settled), and bumps LastActivityAt.
//
// It does NOT touch AttemptsUsed — Round and AttemptsUsed are deliberately
// DISTINCT counters (R-03; Goal.yaml's own field descriptions), and which
// events advance AttemptsUsed is owner-kind-specific engine logic this
// package must not hardcode. Callers that also need to advance the attempt
// counter for this adjudication call RecordAttempt separately, inside the
// same Store.Update mutate closure.
//
// It does NOT itself change g.State — a verdict on its own does not decide
// whether the goal is now terminal (that also depends on the budget ceiling
// and the criteria/dod outcome, which are the calling engine wave's
// business); call Terminate separately when the caller has decided the goal
// is now done.
//
// NFR-2 (fail-closed): RecordVerdict stores whatever v.Met the caller
// computed — it never infers or synthesises Met from an absent verdict, and
// a nil v is rejected rather than silently treated as "not met".
func (g *Goal) RecordVerdict(v *task.JudgeVerdict, reason string, now time.Time) error {
	if v == nil {
		return fmt.Errorf("goal: record verdict %q: nil verdict (NFR-2: absence of a verdict must never be synthesised)", g.GoalID)
	}
	g.LatestVerdict = v
	g.LatestReason = reason
	g.Round++
	g.LastActivityAt = now
	return nil
}

// RecordAttempt advances g.AttemptsUsed by one and bumps LastActivityAt.
// Kept separate from RecordVerdict (see its doc comment) because the two
// counters are distinct and are not always advanced by the same event.
func (g *Goal) RecordAttempt(now time.Time) {
	g.AttemptsUsed++
	g.LastActivityAt = now
}

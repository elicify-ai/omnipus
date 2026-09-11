// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestRecordClaimRequiresEvidenceWhenMet (JUDGE's machine-verifiable
// constraint, Goal.yaml's latest_claim description) proves a met claim with
// empty/whitespace-only evidence is rejected.
func TestRecordClaimRequiresEvidenceWhenMet(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	now := time.Now().UTC()

	if err := g.RecordClaim(generated.GoalLatestClaimStatusMet, "", now); err == nil {
		t.Error("RecordClaim(met, \"\"): want error, got nil")
	}
	if err := g.RecordClaim(generated.GoalLatestClaimStatusMet, "   ", now); err == nil {
		t.Error("RecordClaim(met, whitespace-only): want error, got nil")
	}
	if g.LatestClaim != nil {
		t.Error("a rejected RecordClaim call must not have set LatestClaim")
	}
}

// TestRecordClaimAllowsEmptyEvidenceWhenNotMet proves a blocked/
// waiting_on_user claim does not require evidence.
func TestRecordClaimAllowsEmptyEvidenceWhenNotMet(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	now := time.Now().UTC()
	if err := g.RecordClaim(generated.GoalLatestClaimStatusBlocked, "", now); err != nil {
		t.Fatalf("RecordClaim(blocked, \"\"): %v", err)
	}
	if g.LatestClaim == nil || g.LatestClaim.Status != generated.GoalLatestClaimStatusBlocked {
		t.Errorf("LatestClaim = %+v, want status=blocked", g.LatestClaim)
	}
}

// TestRecordClaimRejectsInvalidStatus proves an out-of-vocabulary status is
// refused.
func TestRecordClaimRejectsInvalidStatus(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	if err := g.RecordClaim(generated.GoalLatestClaimStatus("done"), "evidence", time.Now().UTC()); err == nil {
		t.Fatal("RecordClaim with invalid status: want error, got nil")
	}
}

// TestRecordVerdictRejectsNil (NFR-2: absence of a verdict must never be
// synthesised) proves RecordVerdict refuses a nil verdict rather than
// silently treating it as "not met".
func TestRecordVerdictRejectsNil(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	if err := g.RecordVerdict(nil, "reason", time.Now().UTC()); err == nil {
		t.Fatal("RecordVerdict(nil): want error, got nil")
	}
}

// TestRecordVerdictAdvancesRoundNotAttempts (R-03) proves RecordVerdict
// advances Round by exactly one and leaves AttemptsUsed untouched — the two
// counters are distinct, and this method must not conflate them.
func TestRecordVerdictAdvancesRoundNotAttempts(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	g.AttemptsUsed = 3 // pre-existing, unrelated attempt count
	roundBefore := g.Round

	v := &task.JudgeVerdict{ID: "v1", Scope: task.VerdictScopeGoal, Met: false}
	now := time.Now().UTC()
	if err := g.RecordVerdict(v, "not yet", now); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}
	if g.Round != roundBefore+1 {
		t.Errorf("Round = %d, want %d", g.Round, roundBefore+1)
	}
	if g.AttemptsUsed != 3 {
		t.Errorf("AttemptsUsed = %d, want unchanged 3 (RecordVerdict must not touch it)", g.AttemptsUsed)
	}
	if g.LatestVerdict != v {
		t.Error("LatestVerdict was not set to the recorded verdict")
	}
	if g.LatestReason != "not yet" {
		t.Errorf("LatestReason = %q, want %q", g.LatestReason, "not yet")
	}
	if !g.LastActivityAt.Equal(now) {
		t.Errorf("LastActivityAt = %v, want %v", g.LastActivityAt, now)
	}
}

// TestRecordAttemptAdvancesAttemptsNotRound mirrors
// TestRecordVerdictAdvancesRoundNotAttempts from the other direction.
func TestRecordAttemptAdvancesAttemptsNotRound(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	g.Round = 5
	attemptsBefore := g.AttemptsUsed

	g.RecordAttempt(time.Now().UTC())
	if g.AttemptsUsed != attemptsBefore+1 {
		t.Errorf("AttemptsUsed = %d, want %d", g.AttemptsUsed, attemptsBefore+1)
	}
	if g.Round != 5 {
		t.Errorf("Round = %d, want unchanged 5 (RecordAttempt must not touch it)", g.Round)
	}
}

// TestRecordVerdictPreservesFailClosedMet (NFR-2) proves RecordVerdict
// stores exactly the Met value the caller computed — it neither flips a
// false to true nor a true to false.
func TestRecordVerdictPreservesFailClosedMet(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	v := &task.JudgeVerdict{ID: "v1", Scope: task.VerdictScopeGoal, Met: false}
	if err := g.RecordVerdict(v, "still failing", time.Now().UTC()); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}
	if g.LatestVerdict.Met {
		t.Error("RecordVerdict must not flip a false Met to true")
	}
}

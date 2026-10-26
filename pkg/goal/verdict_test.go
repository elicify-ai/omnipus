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

// TestRecordClaim_AcceptsPresentEmptyEvidenceWhenMet — ADR-084 §13 as
// corrected 2026-10-23 (MAJ-001, issue #1431): the durable invariant follows
// the tool's — validateClaim keeps enforcing the status enum but MUST accept
// a present-empty or whitespace-only evidence text on a met claim. The
// previous form of this test (TestRecordClaimRequiresEvidenceWhenMet) pinned
// the rejection that reproduced the §13 defect one layer below the tool
// (task pauses via pauseRunForGoalError before the Judge; chat record goes
// stale), so it was inverted per §13 "Tests and specs that must change".
func TestRecordClaim_AcceptsPresentEmptyEvidenceWhenMet(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	now := time.Now().UTC()

	if err := g.RecordClaim(generated.GoalLatestClaimStatusMet, "", now); err != nil {
		t.Fatalf("ADR-084 §13 (MAJ-001): RecordClaim(met, \"\") must be accepted, got error: %v", err)
	}
	if g.LatestClaim == nil || g.LatestClaim.Status != generated.GoalLatestClaimStatusMet || g.LatestClaim.Evidence != "" {
		t.Fatalf("LatestClaim = %+v, want status=met with the empty evidence text as given", g.LatestClaim)
	}

	if err := g.RecordClaim(generated.GoalLatestClaimStatusMet, "   ", now); err != nil {
		t.Fatalf("ADR-084 §13 (MAJ-001): RecordClaim(met, whitespace-only) must be accepted, got error: %v", err)
	}
	if g.LatestClaim == nil || g.LatestClaim.Evidence != "   " {
		t.Fatalf("LatestClaim.Evidence = %+v, want the whitespace text carried as given", g.LatestClaim)
	}
}

// TestRecordClaim_EmptyEvidenceMetSurvivesStoreRoundTrip — ADR-084 §13 as
// corrected 2026-10-23 (MAJ-001, issue #1431): through the REAL goal store,
// an empty-evidence met claim records → saves → reads back intact for BOTH
// owner kinds (chat/session-owned and task-owned) — the durable record, not
// just the in-memory struct, carries the claim the Judge will adjudicate.
func TestRecordClaim_EmptyEvidenceMetSurvivesStoreRoundTrip(t *testing.T) {
	for _, kind := range []generated.GoalOwnerKind{generated.GoalOwnerKindSession, generated.GoalOwnerKindTask} {
		t.Run(string(kind), func(t *testing.T) {
			s := newTestStore(t)
			g := mustCreateActive(t, s, kind, "owner-"+string(kind))
			now := time.Now().UTC()

			if _, uerr := s.Update(g.GoalID, func(cur *Goal) error {
				return cur.RecordClaim(generated.GoalLatestClaimStatusMet, "", now)
			}); uerr != nil {
				t.Fatalf("ADR-084 §13 (MAJ-001): RecordClaim(met, \"\") through the real %s-owned store must be accepted, got error: %v", kind, uerr)
			}

			back, rerr := s.Get(g.GoalID)
			if rerr != nil {
				t.Fatalf("read back: %v", rerr)
			}
			if back.LatestClaim == nil {
				t.Fatal("the empty-evidence met claim did not survive the store round trip — LatestClaim is nil")
			}
			if back.LatestClaim.Status != generated.GoalLatestClaimStatusMet || back.LatestClaim.Evidence != "" {
				t.Fatalf("read-back LatestClaim = %+v, want status=met evidence=\"\"", back.LatestClaim)
			}
		})
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

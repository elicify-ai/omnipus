// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"encoding/json"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestCriterion builds a minimal, valid prose AcceptanceCriterion for use
// across this package's tests. Kept here (goal_test.go) since it is the
// first test file loaded alphabetically among this package's *_test.go
// siblings and every other test file in this package needs it.
func newTestCriterion(id, text string) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		ID:       id,
		Kind:     task.KindProse,
		Judgment: task.JudgmentBoolean,
		Text:     text,
		Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "daniel"},
		Status:   task.CritPending,
	}
}

func newTestGoal(t *testing.T, ownerKind generated.GoalOwnerKind, ownerID string) *Goal {
	t.Helper()
	now := time.Now().UTC()
	g, err := New(
		ownerKind, ownerID, generated.ChatCompiled,
		"make the tests pass", "All tests in pkg/goal pass.",
		[]task.AcceptanceCriterion{newTestCriterion("", "the tests pass")},
		[]task.AcceptanceCriterion{newTestCriterion("", "no secrets leaked")},
		20, now,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

// TestGoalEntityRoundTrip (FR-001, S-01) proves a Goal marshals to and from
// JSON without loss — the entity-store precedent (one file, atomic write,
// full round-trip) — via a marshal/unmarshal cycle rather than the store
// itself (store_test.go covers the on-disk half separately).
func TestGoalEntityRoundTrip(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindSession, "session-123")
	g.GoalID = "goal-abc"

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Goal
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.GoalID != g.GoalID {
		t.Errorf("GoalID round-trip: got %q, want %q", got.GoalID, g.GoalID)
	}
	if got.Prompt != g.Prompt || got.Definition != g.Definition {
		t.Errorf("Prompt/Definition round-trip mismatch: got %+v", got)
	}
	if len(got.Criteria) != 1 || got.Criteria[0].Text != g.Criteria[0].Text {
		t.Errorf("Criteria round-trip mismatch: got %+v, want %+v", got.Criteria, g.Criteria)
	}
	if len(got.DoD) != 1 || got.DoD[0].Text != g.DoD[0].Text {
		t.Errorf("DoD round-trip mismatch: got %+v, want %+v", got.DoD, g.DoD)
	}
	if got.State != g.State || got.OwnerKind != g.OwnerKind || got.OwnerID != g.OwnerID {
		t.Errorf("State/OwnerKind/OwnerID round-trip mismatch: got %+v", got)
	}
}

// TestGoalOwnerKindPersisted (FR-002, S-02) asserts owner_kind is part of
// the persisted JSON — never inferred — for both valid owner kinds.
func TestGoalOwnerKindPersisted(t *testing.T) {
	for _, kind := range []generated.GoalOwnerKind{generated.GoalOwnerKindSession, generated.GoalOwnerKindTask} {
		g := newTestGoal(t, kind, "owner-1")
		data, err := json.Marshal(g)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		gotKind, ok := raw["owner_kind"]
		if !ok {
			t.Fatalf("owner_kind missing from persisted JSON for kind %q", kind)
		}
		if gotKind != string(kind) {
			t.Errorf("owner_kind = %v, want %q", gotKind, kind)
		}
		gotID, ok := raw["owner_id"]
		if !ok || gotID != "owner-1" {
			t.Errorf("owner_id = %v, want %q", gotID, "owner-1")
		}
	}
}

// TestGoalCriteriaAreTypedLists (FR-003, S-25) asserts Criteria and DoD
// persist as JSON ARRAYS, never as a serialised string — the exact defect
// FR-003 forbids (today's session-meta GoalCriteriaJSON field).
func TestGoalCriteriaAreTypedLists(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, field := range []string{"criteria", "dod"} {
		rawField, ok := raw[field]
		if !ok {
			t.Fatalf("%s missing from persisted JSON", field)
		}
		// A serialised-string encoding would decode as a JSON string
		// (leading '"'), never as an array (leading '[').
		var asString string
		if err := json.Unmarshal(rawField, &asString); err == nil {
			t.Fatalf("%s persisted as a STRING (%q) — FR-003 forbids this; it must be a typed array", field, asString)
		}
		var asArray []json.RawMessage
		if err := json.Unmarshal(rawField, &asArray); err != nil {
			t.Fatalf("%s did not decode as a JSON array: %v", field, err)
		}
		if len(asArray) != 1 {
			t.Errorf("%s: want 1 element, got %d", field, len(asArray))
		}
	}

	// And at the Go type level: the fields are real, typed Go slices of the
	// shared ADR-080 criterion type — this is enforced by the compiler, but
	// assert it anyway so a future refactor to []byte/string is caught here.
	var _ []task.AcceptanceCriterion = g.Criteria
	var _ []task.AcceptanceCriterion = g.DoD
}

// TestGoalCarriesKeeperCounters (FR-004, S-09) asserts the keeper's own
// durable counters — the recordless-nudge/zero-output-push streak and the
// clarification-question door — are goal-record fields, independently
// settable and surviving a JSON round-trip.
func TestGoalCarriesKeeperCounters(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindTask, "task-1")
	g.ZeroOutputPushes = 2
	g.QuestionRoundsUsed = 1

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Goal
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ZeroOutputPushes != 2 {
		t.Errorf("ZeroOutputPushes round-trip: got %d, want 2", got.ZeroOutputPushes)
	}
	if got.QuestionRoundsUsed != 1 {
		t.Errorf("QuestionRoundsUsed round-trip: got %d, want 1", got.QuestionRoundsUsed)
	}
}

// TestGoalRecordFieldCoverage (FR-006, S-13) asserts the goal record carries
// every field FR-006 enumerates: condition and compiled statement (Prompt/
// Definition), budget and attempts used (MaxRounds/AttemptsUsed), status and
// latest reason (State/LatestReason), the most recent claim and verdict
// (LatestClaim/LatestVerdict), the superseded-criteria history
// (SupersededCriteria), started_at, last_activity_at, and the id of the
// active session.
func TestGoalRecordFieldCoverage(t *testing.T) {
	now := time.Now().UTC()
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	if err := g.Activate("session-active-1", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := g.RecordClaim(generated.GoalLatestClaimStatusMet, "all green", now); err != nil {
		t.Fatalf("RecordClaim: %v", err)
	}
	v := &task.JudgeVerdict{ID: "v1", Scope: task.VerdictScopeGoal, Met: true, Model: "test-model", JudgedAt: now.Format(time.RFC3339)}
	if err := g.RecordVerdict(v, "looks good", now); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}
	if err := g.SupersedeCriteria(
		[]task.AcceptanceCriterion{newTestCriterion("", "revised criterion")},
		[]task.AcceptanceCriterion{newTestCriterion("", "revised dod")},
		now,
	); err != nil {
		t.Fatalf("SupersedeCriteria: %v", err)
	}

	// Condition and compiled statement.
	if g.Prompt == "" {
		t.Error("Prompt (condition) is empty")
	}
	if g.Definition == "" {
		t.Error("Definition (compiled statement) is empty")
	}
	// Budget and attempts used.
	if g.MaxRounds < 1 {
		t.Error("MaxRounds (budget) is not set")
	}
	// Status and latest reason.
	if g.State == "" {
		t.Error("State (status) is empty")
	}
	if g.LatestReason == "" {
		t.Error("LatestReason is empty after RecordVerdict")
	}
	// Most recent claim and verdict.
	if g.LatestClaim == nil {
		t.Error("LatestClaim is nil after RecordClaim")
	}
	if g.LatestVerdict == nil {
		t.Error("LatestVerdict is nil after RecordVerdict")
	}
	// Superseded-criteria history.
	if len(g.SupersededCriteria) != 1 {
		t.Errorf("SupersededCriteria: got %d entries, want 1", len(g.SupersededCriteria))
	}
	// started_at / last_activity_at.
	if g.StartedAt == nil {
		t.Error("StartedAt is nil after Activate")
	}
	if g.LastActivityAt.IsZero() {
		t.Error("LastActivityAt is zero")
	}
	// Active session id.
	if g.ActiveSessionID != "session-active-1" {
		t.Errorf("ActiveSessionID = %q, want %q", g.ActiveSessionID, "session-active-1")
	}
}

// TestGoalNewStartsInDefiningPhase (ADR-086 D2, GOAL-FR-009) — a freshly
// constructed goal exists, is readable, and MUST NOT run: it starts in the
// defining phase, not active.
func TestGoalNewStartsInDefiningPhase(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindTask, "task-1")
	if g.State != generated.GoalStateDefining {
		t.Errorf("State = %q, want %q", g.State, generated.GoalStateDefining)
	}
	if !g.IsDefining() {
		t.Error("IsDefining() = false, want true")
	}
	if g.IsActive() || g.IsTerminal() {
		t.Error("a freshly-constructed goal must be neither active nor terminal")
	}
	if g.ActiveSessionID != "" {
		t.Errorf("ActiveSessionID = %q, want empty (defining phase has no session yet)", g.ActiveSessionID)
	}
}

// TestGoalNewRejectsEmptyDoD (D11/D15 — the schema's minItems: 1) proves New
// refuses to construct a goal with no definition-of-done items — this is a
// FAILING test if the empty-DoD floor is not enforced: a mutation deleting
// the len(dod)==0 check in Validate makes this test observe a nil error.
func TestGoalNewRejectsEmptyDoD(t *testing.T) {
	_, err := New(
		generated.GoalOwnerKindSession, "s1", generated.ChatCompiled,
		"prompt", "definition",
		[]task.AcceptanceCriterion{newTestCriterion("", "some criterion")},
		nil, // empty dod
		20, time.Now().UTC(),
	)
	if err == nil {
		t.Fatal("New with empty dod: want error (D11: at least one DoD item is mandatory), got nil")
	}
}

// TestGoalValidateRejectsInvalidOwnerKind (FR-002) proves Goal.Validate
// rejects an owner_kind outside the two allowed values.
func TestGoalValidateRejectsInvalidOwnerKind(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	g.OwnerKind = generated.GoalOwnerKind("plan") // R-31: plan was dropped
	if err := g.Validate(); err == nil {
		t.Fatal("Validate with owner_kind=plan: want error, got nil")
	}
}

// TestGoalValidateRejectsMissingOwnerID (FR-002) proves an owner kind with
// no owner id is rejected.
func TestGoalValidateRejectsMissingOwnerID(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	g.OwnerID = ""
	if err := g.Validate(); err == nil {
		t.Fatal("Validate with empty owner_id: want error, got nil")
	}
}

// TestGoalValidateRejectsInvalidState proves an out-of-vocabulary state is
// rejected — guards against a future typo in one of the six generated.GoalState
// constants slipping through unnoticed.
func TestGoalValidateRejectsInvalidState(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	g.State = generated.GoalState("done") // R-14: the pre-existing value, renamed to "met"
	if err := g.Validate(); err == nil {
		t.Fatal("Validate with state=done (pre-rename value): want error, got nil")
	}
}

// TestGoalValidateRejectsZeroMaxRounds proves MaxRounds < 1 is rejected —
// GOAL-FR-024 requires at least 1.
func TestGoalValidateRejectsZeroMaxRounds(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	g.MaxRounds = 0
	if err := g.Validate(); err == nil {
		t.Fatal("Validate with max_rounds=0: want error, got nil")
	}
}

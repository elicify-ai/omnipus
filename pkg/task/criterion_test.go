// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCriterion_Validate exercises the "Dataset: Criterion validation" rows
// from the Planning & Goals spec (Part A §C, SD-A9) via the store's normal
// Create path. Rows 14/15 (all-check + assignee bash policy deny/ask) are the
// agent-tool-path tier (ADR D2 rule 5, FR-017) — that check depends on caller
// identity/tool-policy the store does not have access to, so it is NOT
// exercised here; it belongs to the tool/gateway layer.
func TestCriterion_Validate(t *testing.T) {
	longText := strings.Repeat("x", 1001)
	cases := []struct {
		name    string
		crit    AcceptanceCriterion
		wantErr bool
	}{
		{
			name: "1 prose valid",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "looks good",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: false,
		},
		{
			name: "2 check valid",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "tests pass",
				Check:  &CriterionCheck{Command: "go test", ExpectedExitCode: 0},
				Author: CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"},
			},
			wantErr: false,
		},
		{
			name: "3 invalid kind",
			crit: AcceptanceCriterion{
				Kind: "bogus", Text: "x",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "4 empty text",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "5 text over 1000 runes",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: longText,
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "6 check with no check object",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "7 exit code min 0",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: 0},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: false,
		},
		{
			name: "8 exit code max 255",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: 255},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: false,
		},
		{
			name: "9 exit code 256 over max",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: 256},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "10 exit code -1 under min",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: -1},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "11 prose with check object is mixed shape",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: 0},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "12 author absent",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "x",
			},
			wantErr: true,
		},
		{
			name: "13 author id empty",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "x",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: ""},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			tk := mkTask("crit-task", "ws-1")
			tk.Criteria = []AcceptanceCriterion{tc.crit}
			err := s.Create(tk)
			if tc.wantErr {
				require.Error(t, err, "case %q must be rejected", tc.name)
				assert.True(t, errors.Is(err, ErrValidation),
					"case %q must wrap ErrValidation, got: %v", tc.name, err)
				return
			}
			require.NoError(t, err, "case %q must be accepted", tc.name)
			require.Len(t, tk.Criteria, 1)
			// Server-set fields: ID generated, Status defaulted to pending.
			assert.NotEmpty(t, tk.Criteria[0].ID, "criterion ID must be server-set")
			assert.Equal(t, CritPending, tk.Criteria[0].Status, "criterion status must default to pending")
		})
	}
}

// TestCriterion_AuthorRecorded verifies that a criterion's author identity
// (ADR D2 rule 3) is recorded exactly as supplied and survives a round trip
// through create + reload, for both agent and user authors — a
// differentiation check that the field is not dropped or hardcoded.
func TestCriterion_AuthorRecorded(t *testing.T) {
	s := newStore(t)

	agentAuthored := mkTask("agent-authored", "ws-1")
	agentAuthored.Criteria = []AcceptanceCriterion{{
		Kind: KindCheck, Text: "tests pass",
		Check:  &CriterionCheck{Command: "go test ./...", ExpectedExitCode: 0},
		Author: CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"},
	}}
	require.NoError(t, s.Create(agentAuthored))

	userAuthored := mkTask("user-authored", "ws-1")
	userAuthored.Criteria = []AcceptanceCriterion{{
		Kind: KindProse, Text: "reads well",
		Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
	}}
	require.NoError(t, s.Create(userAuthored))

	got1, err := s.Get(agentAuthored.ID)
	require.NoError(t, err)
	require.Len(t, got1.Criteria, 1)
	assert.Equal(t, AuthorKindAgent, got1.Criteria[0].Author.Kind)
	assert.Equal(t, "jim", got1.Criteria[0].Author.ID)

	got2, err := s.Get(userAuthored.ID)
	require.NoError(t, err)
	require.Len(t, got2.Criteria, 1)
	assert.Equal(t, AuthorKindUser, got2.Criteria[0].Author.Kind)
	assert.Equal(t, "alice", got2.Criteria[0].Author.ID)

	// Differentiation: the two tasks' recorded authors must differ.
	assert.NotEqual(t, got1.Criteria[0].Author, got2.Criteria[0].Author)
}

// TestCriterion_BehaviorPayload_Validate is the ADR-052 FR-034 companion to
// TestCriterion_Validate: valid rows plus each documented invalid case
// (missing tool, negative min, max<min, bad scope, mixed shape) for the new
// `behavior` criterion kind, exercised through the store's normal Create path
// exactly like the check/prose dataset above.
func TestCriterion_BehaviorPayload_Validate(t *testing.T) {
	author := CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"}
	cases := []struct {
		name    string
		crit    AcceptanceCriterion
		wantErr bool
		// wantMinCount/wantScope check the post-validate defaulted values,
		// only when wantErr is false.
		wantMinCount int
		wantScope    BehaviorScope
	}{
		{
			name: "valid: min_count/scope omitted defaults to 1/task_session",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "called bash at least once",
				Behavior: &CriterionBehavior{Tool: "bash"},
				Author:   author,
			},
			wantErr:      false,
			wantMinCount: 1,
			wantScope:    BehaviorScopeTaskSession,
		},
		{
			name: "valid: explicit min/max/scope",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "called bash 2-5 times this attempt",
				Behavior: &CriterionBehavior{Tool: "bash", MinCount: ptr(2), MaxCount: ptr(5), Scope: BehaviorScopeAttempt},
				Author:   author,
			},
			wantErr:      false,
			wantMinCount: 2,
			wantScope:    BehaviorScopeAttempt,
		},
		{
			name: "valid: min_count=0 + max_count=0 expresses never call X",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "never called rm",
				Behavior: &CriterionBehavior{Tool: "rm", MinCount: ptr(0), MaxCount: ptr(0)},
				Author:   author,
			},
			wantErr:      false,
			wantMinCount: 0,
			wantScope:    BehaviorScopeTaskSession,
		},
		{
			// Fix-wave finding #5: MinCount is now *int — nil (this field
			// simply never set) is the "omitted" signal, unambiguous without
			// the old max_count=0 disambiguator hack.
			name: "valid: min_count/max_count both omitted (nil) defaults min to 1",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "min_count omitted",
				Behavior: &CriterionBehavior{Tool: "bash"},
				Author:   author,
			},
			wantErr:      false,
			wantMinCount: 1,
			wantScope:    BehaviorScopeTaskSession,
		},
		{
			// Fix-wave finding #5's headline capability: an EXPLICIT
			// min_count=0 (a real *int pointing at 0, not an omitted field)
			// now stays 0 — it no longer silently defaults to 1 just because
			// no max_count=0 pairing disambiguates it.
			name: "valid: explicit min_count=0 alone (no max) stays 0, does not default to 1",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "optional, no upper bound",
				Behavior: &CriterionBehavior{Tool: "bash", MinCount: ptr(0)},
				Author:   author,
			},
			wantErr:      false,
			wantMinCount: 0,
			wantScope:    BehaviorScopeTaskSession,
		},
		{
			// [0, N>0] is now expressible (fix-wave finding #5): "called at
			// most 3 times, calling it at all is optional".
			name: "valid: min_count=0 + max_count=3 expresses 0..3 calls (optional, bounded)",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "called at most 3 times",
				Behavior: &CriterionBehavior{Tool: "bash", MinCount: ptr(0), MaxCount: ptr(3)},
				Author:   author,
			},
			wantErr:      false,
			wantMinCount: 0,
			wantScope:    BehaviorScopeTaskSession,
		},
		{
			name: "invalid: missing tool",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "x",
				Behavior: &CriterionBehavior{Tool: ""},
				Author:   author,
			},
			wantErr: true,
		},
		{
			name: "invalid: negative min_count",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "x",
				Behavior: &CriterionBehavior{Tool: "bash", MinCount: ptr(-1)},
				Author:   author,
			},
			wantErr: true,
		},
		{
			name: "invalid: max_count < min_count",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "x",
				Behavior: &CriterionBehavior{Tool: "bash", MinCount: ptr(3), MaxCount: ptr(2)},
				Author:   author,
			},
			wantErr: true,
		},
		{
			name: "invalid: unknown scope value",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "x",
				Behavior: &CriterionBehavior{Tool: "bash", Scope: "whenever"},
				Author:   author,
			},
			wantErr: true,
		},
		{
			name: "invalid: behavior kind with no payload",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "x",
				Author: author,
			},
			wantErr: true,
		},
		{
			name: "invalid: behavior kind mixed with a check object",
			crit: AcceptanceCriterion{
				Kind: KindBehavior, Text: "x",
				Behavior: &CriterionBehavior{Tool: "bash"},
				Check:    &CriterionCheck{Command: "x", ExpectedExitCode: 0},
				Author:   author,
			},
			wantErr: true,
		},
		{
			name: "invalid: check kind mixed with a behavior payload",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:    &CriterionCheck{Command: "x", ExpectedExitCode: 0},
				Behavior: &CriterionBehavior{Tool: "bash"},
				Author:   author,
			},
			wantErr: true,
		},
		{
			name: "invalid: prose kind mixed with a behavior payload",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "x",
				Behavior: &CriterionBehavior{Tool: "bash"},
				Author:   author,
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			tk := mkTask("crit-behavior-task", "ws-1")
			tk.Criteria = []AcceptanceCriterion{tc.crit}
			err := s.Create(tk)
			if tc.wantErr {
				require.Error(t, err, "case %q must be rejected", tc.name)
				assert.True(t, errors.Is(err, ErrValidation),
					"case %q must wrap ErrValidation, got: %v", tc.name, err)
				return
			}
			require.NoError(t, err, "case %q must be accepted", tc.name)
			require.Len(t, tk.Criteria, 1)
			require.NotNil(t, tk.Criteria[0].Behavior)
			require.NotNil(t, tk.Criteria[0].Behavior.MinCount, "min_count must be normalized to an explicit value")
			assert.Equal(t, tc.wantMinCount, tk.Criteria[0].Behavior.EffectiveMinCount(), "min_count default/value")
			assert.Equal(t, tc.wantScope, tk.Criteria[0].Behavior.Scope, "scope default/value")
		})
	}
}

// TestCriterionBehavior_UnmarshalJSON_RejectsUnknownFields locks in FR-034's
// "unknown fields reject" payload rule. A plain Go struct literal can't
// express an "unknown field" (there's no such field to set), so this is
// exercised at the JSON boundary directly, which is where an unrecognized key
// would actually arrive from (a REST/tool-layer request body).
func TestCriterionBehavior_UnmarshalJSON_RejectsUnknownFields(t *testing.T) {
	valid := `{"tool":"bash","min_count":1,"max_count":5,"scope":"attempt"}`
	var b CriterionBehavior
	require.NoError(t, json.Unmarshal([]byte(valid), &b), "known fields must decode cleanly")
	assert.Equal(t, "bash", b.Tool)

	withUnknown := `{"tool":"bash","min_count":1,"bogus_field":"nope"}`
	var b2 CriterionBehavior
	err := json.Unmarshal([]byte(withUnknown), &b2)
	require.Error(t, err, "an unrecognized payload key must be rejected")
	assert.Contains(t, err.Error(), "bogus_field")
}

// TestCriterionBehavior_UnmarshalJSON_RejectsUnknownFields_ViaCriterion
// confirms the same rejection fires when the behavior payload arrives nested
// inside a full AcceptanceCriterion (the realistic shape), not just when
// CriterionBehavior is unmarshaled standalone.
func TestCriterionBehavior_UnmarshalJSON_RejectsUnknownFields_ViaCriterion(t *testing.T) {
	raw := `{
		"kind": "behavior",
		"text": "x",
		"behavior": {"tool": "bash", "min_count": 1, "extra_key": true},
		"author": {"kind": "agent", "id": "jim"}
	}`
	var c AcceptanceCriterion
	err := json.Unmarshal([]byte(raw), &c)
	require.Error(t, err, "unknown key nested under behavior must be rejected")
	assert.Contains(t, err.Error(), "extra_key")
}

// TestValidateCriterion_KindJudgmentCorrelation is code-review fix-wave
// finding #3: validateCriterion itself must re-assert the kind<->judgment
// correlation, not rely solely on InferJudgment catching it upstream.
// Before the fix, a criterion built with an EXPLICIT, already-resolved kind
// AND a mismatched explicit judgment — e.g. {Kind: KindCheck, Judgment:
// JudgmentArtifact} — passed validateCriterion cleanly whenever a caller
// invoked it directly (bypassing normalizeCriteria's InferJudgment call),
// because the correlation lived only in InferJudgment. Called directly here
// (not through normalizeCriteria/InferJudgment) to prove validateCriterion
// itself, not just its usual caller, now rejects the mismatch.
func TestValidateCriterion_KindJudgmentCorrelation(t *testing.T) {
	validAuthor := CriterionAuthor{Kind: AuthorKindUser, ID: "alice"}

	t.Run("check with judgment artifact is rejected", func(t *testing.T) {
		c := AcceptanceCriterion{
			Kind: KindCheck, Judgment: JudgmentArtifact, Text: "tests pass",
			Check:  &CriterionCheck{Command: "go test", ExpectedExitCode: 0},
			Author: validAuthor, Status: CritPending,
		}
		err := validateCriterion(&c, 0)
		require.Error(t, err, "kind=check with judgment=artifact must be rejected by validateCriterion directly")
		assert.Contains(t, err.Error(), "check")
	})

	t.Run("behavior with judgment artifact is rejected", func(t *testing.T) {
		one := 1
		c := AcceptanceCriterion{
			Kind: KindBehavior, Judgment: JudgmentArtifact, Text: "call search_web",
			Behavior: &CriterionBehavior{Tool: "search_web", MinCount: &one, Scope: BehaviorScopeTaskSession},
			Author:   validAuthor, Status: CritPending,
		}
		err := validateCriterion(&c, 0)
		require.Error(t, err, "kind=behavior with judgment=artifact must be rejected by validateCriterion directly")
		assert.Contains(t, err.Error(), "behavior")
	})

	// Control: the matching (natural) judgment for each technical kind still
	// passes, so the new checks are additive, not a regression on the
	// already-correlated case.
	t.Run("check with judgment boolean (natural) still passes", func(t *testing.T) {
		c := AcceptanceCriterion{
			Kind: KindCheck, Judgment: JudgmentBoolean, Text: "tests pass",
			Check:  &CriterionCheck{Command: "go test", ExpectedExitCode: 0},
			Author: validAuthor, Status: CritPending,
		}
		require.NoError(t, validateCriterion(&c, 0))
	})

	t.Run("behavior with judgment quantitative (natural) still passes", func(t *testing.T) {
		one := 1
		c := AcceptanceCriterion{
			Kind: KindBehavior, Judgment: JudgmentQuantitative, Text: "call search_web",
			Behavior: &CriterionBehavior{Tool: "search_web", MinCount: &one, Scope: BehaviorScopeTaskSession},
			Author:   validAuthor, Status: CritPending,
		}
		require.NoError(t, validateCriterion(&c, 0))
	})
}

// TestNoFourthCriterionStatusValue locks GOAL-FR-037 (ADR-086) / the joint
// delivery plan's C-01 resolution: AcceptanceCriterion.status has exactly
// three values — pending, met, unmet — and no fourth is ever added, on the
// wire, in the store, or in the interface. ADR-084 revision 9 §10 withdrew
// the `unable_to_verify` third outcome the in-tree judge spec still
// describes throughout (judge-active-reviewer-spec.md §D, FR-076); this
// test is the guard against that withdrawn value (or any other) being
// reintroduced here.
//
// Asserted BEHAVIOURALLY, over a candidate table run through the real
// IsValidCriterionStatus predicate — never by grepping criterion.go's
// source text for a symbol (delivery plan §6 rule 14). A candidate list of
// (value, expected) pairs, table-driven, so a future edit that accepts a
// fourth value fails this test regardless of which value it is.
//
// Traces to: docs/internal/specs/goal-entity-spec.md GOAL-FR-037 (line 459);
// docs/internal/specs/adr-084-086-joint-delivery-plan.md C-01 (line 231),
// wave T1 (line 371).
func TestNoFourthCriterionStatusValue(t *testing.T) {
	cases := []struct {
		name  string
		value CriterionStatus
		valid bool
	}{
		{"pending is valid", CritPending, true},
		{"met is valid", CritMet, true},
		{"unmet is valid", CritUnmet, true},
		{"pending literal is valid", CriterionStatus("pending"), true},
		{"met literal is valid", CriterionStatus("met"), true},
		{"unmet literal is valid", CriterionStatus("unmet"), true},
		// The withdrawn ADR-084 revision 7 third outcome (D2a). Rev 9 §10
		// withdrew it in full; C-01 forbids it appearing anywhere.
		{"unable_to_verify is NOT valid (withdrawn ADR-084 D2a outcome)", CriterionStatus("unable_to_verify"), false},
		// Other plausible-looking but never-defined fourth values, so this
		// guard does not accidentally special-case unable_to_verify alone.
		{"unknown is NOT valid", CriterionStatus("unknown"), false},
		{"failed is NOT valid", CriterionStatus("failed"), false},
		{"in_progress is NOT valid", CriterionStatus("in_progress"), false},
		{"empty string is NOT valid", CriterionStatus(""), false},
		{"case-sensitive: MET is NOT valid", CriterionStatus("MET"), false},
		{"case-sensitive: Pending is NOT valid", CriterionStatus("Pending"), false},
		{"whitespace-padded ' met' is NOT valid", CriterionStatus(" met"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.valid, IsValidCriterionStatus(tc.value),
				"IsValidCriterionStatus(%q) = %v, want %v", tc.value, !tc.valid, tc.valid)
		})
	}

	// Differentiation: prove the three real values are pairwise distinct
	// under the predicate's caller (validateCriterion), not just that the
	// predicate itself returns true for each — a stub that always returned
	// true for any non-empty string would still pass the table above's
	// "valid" rows alone.
	t.Run("exactly three distinct valid values, no more", func(t *testing.T) {
		valid := map[CriterionStatus]bool{}
		for _, candidate := range []CriterionStatus{
			CritPending, CritMet, CritUnmet,
			"unable_to_verify", "unknown", "", "queued", "active", "done",
		} {
			if IsValidCriterionStatus(candidate) {
				valid[candidate] = true
			}
		}
		require.Len(t, valid, 3, "exactly three CriterionStatus values must validate; got %v", valid)
		assert.True(t, valid[CritPending])
		assert.True(t, valid[CritMet])
		assert.True(t, valid[CritUnmet])
	})
}

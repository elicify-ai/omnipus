// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verdict_adr084_test.go covers wave F2 of the ADR-084/085/086 joint
// delivery: JUDGE-FR-006b (the persisted, never-recomputed-at-adjudication
// clause count on AcceptanceCriterion), JUDGE-FR-070a (CriterionVerdict's
// four new optional Go fields — evidence_source, evidence_target,
// provenance, evidence[] — deliberately NOT a fifth "outcome" field, see
// C-02/D-H in docs/internal/specs/adr-084-086-joint-delivery-plan.md), and
// JUDGE-FR-074 (a pre-existing persisted verdict parses with the new fields
// empty and renders unchanged).
//
// Expected clause counts below are derived from the splitter's own
// documented rule (JUDGE-FR-006b: delimiters "; ", " and ", newline
// bullets, capped at 5) — not by running SplitCriterionClauses and
// recording what it printed.
package task

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSplitCriterionClauses_DeterministicAndCappedAtFive is the judge
// spec's FR-006a-named oracle (its file assignment collides with a
// cancelled test file per the plan's T1 row, so it lives here instead,
// scoped to the package that owns the splitter).
func TestSplitCriterionClauses_DeterministicAndCappedAtFive(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{
			name: "no delimiter is one clause",
			text: "the button links to the pricing page",
			want: 1,
		},
		{
			name: "semicolon-space splits into two",
			text: "go test passes; no linter errors",
			want: 2,
		},
		{
			name: "space-and-space splits into two",
			text: "the file exists and is non-empty",
			want: 2,
		},
		{
			name: "semicolon segments each further split on and",
			text: "a; b and c",
			want: 3,
		},
		{
			name: "newline bullets each become one clause",
			text: "- item one\n- item two\n- item three",
			want: 3,
		},
		{
			name: "asterisk bullets each become one clause",
			text: "* first thing\n* second thing",
			want: 2,
		},
		{
			name: "seven and-joined clauses cap at five",
			text: "a and b and c and d and e and f and g",
			want: 5,
		},
		{
			name: "a bare semicolon with no trailing space does not split",
			text: "a;b is one string",
			want: 1,
		},
		{
			name: "a quoted identifier containing literal and space-and-space still splits (E-15, accepted false split)",
			text: `the link label reads "terms and conditions"`,
			want: 2,
		},
		{
			name: "whitespace-only text still returns exactly one clause",
			text: "   ",
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitCriterionClauses(tc.text)
			assert.Lenf(t, got, tc.want, "SplitCriterionClauses(%q) = %#v", tc.text, got)
			assert.LessOrEqualf(t, len(got), maxClauseCount, "clause count must never exceed the FR-006b cap of %d", maxClauseCount)
		})
	}
}

// TestNormalizeCriteria_PersistsClauseCountAtCreation is JUDGE-FR-006b's
// named oracle: a criterion created with 3 clauses carries the persisted
// count, and passing the same (unchanged) criterion back through
// normalizeCriteria again — modelling a load-then-resave — reproduces the
// identical count rather than drifting, because the value is a pure
// function of Text computed fresh every call, never read back from the
// adjudicator's own state.
func TestNormalizeCriteria_PersistsClauseCountAtCreation(t *testing.T) {
	crit := AcceptanceCriterion{
		Kind:   KindProse,
		Text:   "go test passes; no linter errors; docs updated",
		Author: CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"},
	}

	normalized, err := NormalizeCriteria([]AcceptanceCriterion{crit})
	require.NoError(t, err)
	require.Len(t, normalized, 1)
	assert.Equal(t, 3, normalized[0].ClauseCount, "3-clause text must persist a clause count of 3")
	require.NotEmpty(t, normalized[0].ID, "normalizeCriteria server-sets an ID")

	// Simulate a load-then-resave with the criterion's text unchanged: the
	// persisted value must reproduce identically, not drift.
	again, err := NormalizeCriteria(normalized)
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.Equal(t, 3, again[0].ClauseCount, "clause count must not drift when the criterion text is unchanged")
}

// TestNormalizeCriteria_ClauseCountFollowsEditedText proves the count is
// recomputed (not frozen at some stale value) when Text is genuinely
// edited — the FR-006b protection is specifically against a LOWER count on
// the SAME criterion id while a verdict is on record, a rule this package
// does not enforce (it has no visibility into recorded verdicts); it is
// enforced by the wave that owns the update call path. This test only
// establishes the mechanism the protection depends on: the count tracks
// Text.
func TestNormalizeCriteria_ClauseCountFollowsEditedText(t *testing.T) {
	threeClause := AcceptanceCriterion{
		Kind:   KindProse,
		Text:   "a; b; c",
		Author: CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"},
	}
	normalized, err := NormalizeCriteria([]AcceptanceCriterion{threeClause})
	require.NoError(t, err)
	require.Equal(t, 3, normalized[0].ClauseCount)

	edited := normalized[0]
	edited.Text = "a"
	reNormalized, err := NormalizeCriteria([]AcceptanceCriterion{edited})
	require.NoError(t, err)
	assert.Equal(t, 1, reNormalized[0].ClauseCount, "a genuinely edited Text must recompute the count")
}

// TestCriterionVerdict_NewFieldsJSONRoundTrip is JUDGE-FR-070a's Go-field
// half (the mapping-loop population half belongs to E9, in pkg/agent,
// outside this package): the four new fields — evidence_source,
// evidence_target, provenance, evidence[] — exist on CriterionVerdict,
// carry the wire's exact JSON key names, and round-trip. It also asserts,
// as a corroborating check on the actual encoded output (not a source-text
// grep), that no "outcome" key is ever produced — C-02/D-H retire the
// three-state outcome in full.
func TestCriterionVerdict_NewFieldsJSONRoundTrip(t *testing.T) {
	v := CriterionVerdict{
		CriterionID:    "c1",
		Met:            true,
		Reason:         "the pricing link goes to /pricing and the button is blue",
		EvidenceQuote:  `<a href="/pricing">Pricing</a>`,
		EvidenceSource: EvidenceSourceFileRead,
		EvidenceTarget: "neon-2048/index.html",
		Provenance:     ProvenanceJudgeRead,
		Evidence: []CriterionEvidenceEntry{
			{Part: "the pricing link goes to /pricing", Source: "file_read", Target: "neon-2048/index.html", Quote: `<a href="/pricing">Pricing</a>`},
			{Part: "the button is blue", Source: "file_read", Target: "neon-2048/style.css", Quote: "background: blue;"},
		},
	}

	b, err := json.Marshal(v)
	require.NoError(t, err)
	body := string(b)

	assert.Contains(t, body, `"evidence_source":"file_read"`)
	assert.Contains(t, body, `"evidence_target":"neon-2048/index.html"`)
	assert.Contains(t, body, `"provenance":"judge_read"`)
	assert.Contains(t, body, `"part":"the pricing link goes to /pricing"`)
	assert.NotContains(t, body, `"outcome"`, "ADR-084 revision 9 withdraws the outcome field in full (C-02, D-H) — it must never be emitted")

	var back CriterionVerdict
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, v, back, "round-trip must reproduce every new field exactly")
}

// TestCriterionEvidenceEntry_RequiredFieldsAlwaysKeyed asserts Part and
// Quote are always present keys in the marshalled JSON, even when Quote is
// the empty string — the schema marks both required (key present), while
// Quote's VALUE may legitimately be empty when the Judge could not locate
// grounding for that clause and is reporting the gap rather than
// fabricating a quote (D-B).
func TestCriterionEvidenceEntry_RequiredFieldsAlwaysKeyed(t *testing.T) {
	entry := CriterionEvidenceEntry{Part: "the deck has a conclusion slide"}
	b, err := json.Marshal(entry)
	require.NoError(t, err)
	body := string(b)

	assert.Contains(t, body, `"part":"the deck has a conclusion slide"`)
	assert.Contains(t, body, `"quote":""`, "quote is a required key even when its value is empty (an honestly-reported grounding gap, D-B)")
	assert.NotContains(t, body, `"source"`, "source is optional and must be omitted when empty")
	assert.NotContains(t, body, `"target"`, "target is optional and must be omitted when empty")
}

// TestCriterionVerdict_PreExistingVerdictParsesWithEmptyNewFields is
// JUDGE-FR-074's named oracle: a persisted verdict written before this
// change (the old four-field shape only) MUST parse with all new fields
// empty, and re-marshalling it MUST NOT introduce any of the new keys —
// the SPA and replay.go's toJudgeVerdictFrame both depend on this to
// render a pre-existing verdict exactly as before.
func TestCriterionVerdict_PreExistingVerdictParsesWithEmptyNewFields(t *testing.T) {
	oldShape := `{"criterion_id":"c1","met":true,"reason":"go test output shows 3 passing tests","evidence_quote":"--- PASS: TestFoo (0.01s)"}`

	var v CriterionVerdict
	require.NoError(t, json.Unmarshal([]byte(oldShape), &v))

	assert.Equal(t, "c1", v.CriterionID)
	assert.True(t, v.Met)
	assert.Equal(t, "--- PASS: TestFoo (0.01s)", v.EvidenceQuote)
	assert.Empty(t, v.EvidenceSource, "a pre-existing verdict carries no evidence_source")
	assert.Empty(t, v.EvidenceTarget, "a pre-existing verdict carries no evidence_target")
	assert.Empty(t, v.Provenance, "a pre-existing verdict carries no provenance")
	assert.Empty(t, v.Evidence, "a pre-existing verdict carries no evidence array")

	reMarshaled, err := json.Marshal(v)
	require.NoError(t, err)
	body := string(reMarshaled)
	for _, key := range []string{"evidence_source", "evidence_target", "provenance", `"evidence"`, "outcome"} {
		assert.False(t, strings.Contains(body, key), "re-marshalled pre-existing verdict must not gain key %q, got %s", key, body)
	}
}

// TestIsValidVerdictEvidenceSource and TestIsValidVerdictProvenance assert
// the two new enum validators accept exactly the values
// contracts/components/schemas/CriterionVerdict.yaml declares (plus empty,
// since both fields are optional REPORTING fields, D-B) and reject
// everything else.
func TestIsValidVerdictEvidenceSource(t *testing.T) {
	valid := []VerdictEvidenceSource{
		"", EvidenceSourceDiff, EvidenceSourceTranscript, EvidenceSourceMachineCheck,
		EvidenceSourceFileRead, EvidenceSourceSessionRead,
	}
	for _, v := range valid {
		assert.Truef(t, IsValidVerdictEvidenceSource(v), "%q must be valid", v)
	}
	assert.False(t, IsValidVerdictEvidenceSource("bogus"))
	assert.False(t, IsValidVerdictEvidenceSource("unable_to_verify"))
}

func TestIsValidVerdictProvenance(t *testing.T) {
	valid := []VerdictProvenance{
		"", ProvenanceJudgeRead, ProvenanceDeterministic, ProvenanceDiffRead,
		ProvenanceTranscriptRead, ProvenanceSessionRead, ProvenanceNone,
	}
	for _, v := range valid {
		assert.Truef(t, IsValidVerdictProvenance(v), "%q must be valid", v)
	}
	assert.False(t, IsValidVerdictProvenance("bogus"))
}

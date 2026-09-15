// rest_task_wire_test.go: tests for translate task records to and from the wire: criteria, Definition of Done, status mapping

package gateway

import (
	"encoding/json"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest_tasks.go tests 2026-09-15 ---

func TestToWireJudgeVerdict_EvidenceQuote(t *testing.T) {
	out := toWireJudgeVerdict(judgeVerdictWithQuotes())
	require.Len(t, out.PerCriterion, 2)

	require.NotNil(t, out.PerCriterion[0].EvidenceQuote)
	assert.Equal(t, "--- PASS: TestX", *out.PerCriterion[0].EvidenceQuote)

	// Empty quote → absent from the wire, never a present "".
	assert.Nil(t, out.PerCriterion[1].EvidenceQuote)
}

// TestToWireJudgeVerdict_EmptyPerCriterion_MarshalsAsEmptyArrayNotNull covers
// rest_tasks.go's toWireJudgeVerdict (feeds GET /tasks/{id}/verdicts and the
// openapi Message.verdict shape).
func TestToWireJudgeVerdict_EmptyPerCriterion_MarshalsAsEmptyArrayNotNull(t *testing.T) {
	v := emptyCriterionVerdict()
	require.Nil(t, v.PerCriterion, "precondition: the source verdict must have a nil PerCriterion")

	out := toWireJudgeVerdict(v)
	data, err := json.Marshal(out)
	require.NoError(t, err)

	assert.Contains(t, string(data), `"per_criterion":[]`,
		"per_criterion is a required array on the wire (no omitempty) — an "+
			"empty verdict must still marshal it as [], not null, or the SPA's "+
			"zod schema rejects the frame and drops it")
	assert.NotContains(t, string(data), `"per_criterion":null`)

	// Round-trip through the generated type's own JSON tags to confirm the
	// field decodes back as a non-nil, empty slice (not "absent").
	var roundTrip struct {
		PerCriterion []struct {
			CriterionId string `json:"criterion_id"`
		} `json:"per_criterion"`
	}
	require.NoError(t, json.Unmarshal(data, &roundTrip))
	assert.NotNil(t, roundTrip.PerCriterion)
	assert.Empty(t, roundTrip.PerCriterion)
}

// TestToWireJudgeVerdict_NonEmptyPerCriterion_StillRoundTrips is a control
// case proving the fix's make([]T, 0, len(...)) preallocation didn't break
// the populated path — same assertions replay_judge_verdict_test.go already
// makes for toJudgeVerdictFrame's non-empty path, mirrored here for
// toWireJudgeVerdict.
func TestToWireJudgeVerdict_NonEmptyPerCriterion_StillRoundTrips(t *testing.T) {
	v := emptyCriterionVerdict()
	v.PerCriterion = []task.CriterionVerdict{
		{CriterionID: "c1", Met: true, Reason: "looks good"},
		{CriterionID: "c2", Met: false, Reason: "missing evidence"},
	}

	out := toWireJudgeVerdict(v)
	require.Len(t, out.PerCriterion, 2)
	assert.Equal(t, "c1", out.PerCriterion[0].CriterionId)
	assert.True(t, out.PerCriterion[0].Met)
	assert.Equal(t, "c2", out.PerCriterion[1].CriterionId)
	assert.False(t, out.PerCriterion[1].Met)
	assert.Equal(t, "missing evidence", out.PerCriterion[1].Reason)
}

// TestToWireJudgeVerdictCarriesEvidenceFields covers the stale-comment drop:
// toWireJudgeVerdict discarded Evidence, EvidenceSource, EvidenceTarget and
// Provenance on a comment asserting the Go fields did not exist yet. They do
// (pkg/task/verdict.go), and replay.go's WS frame already sends all four — so
// the live frame showed the judge's evidence and a page reload, which re-reads
// through REST, silently erased it.
func TestToWireJudgeVerdictCarriesEvidenceFields(t *testing.T) {
	v := task.JudgeVerdict{
		ID:    "verdict-1",
		Scope: task.VerdictScopeTask,
		Round: 1,
		Met:   true,
		PerCriterion: []task.CriterionVerdict{{
			CriterionID:    "crit-1",
			Met:            true,
			Reason:         "both clauses check out",
			EvidenceQuote:  "func Foo() error { return nil }",
			EvidenceSource: task.EvidenceSourceFileRead,
			EvidenceTarget: "pkg/foo/foo.go",
			Provenance:     task.ProvenanceJudgeRead,
			Evidence: []task.CriterionEvidenceEntry{
				{Part: "it compiles", Quote: "exit=0", Source: "machine_check", Target: "go build"},
				{Part: "it is documented", Quote: "// Foo does the thing", Source: "file_read", Target: "pkg/foo/foo.go"},
			},
		}},
	}

	out := toWireJudgeVerdict(v)

	require.Len(t, out.PerCriterion, 1)
	pc := out.PerCriterion[0]

	require.NotNil(t, pc.EvidenceSource, "evidence_source must reach the wire")
	assert.Equal(t, gen.JudgeVerdictPerCriterionEvidenceSourceFileRead, *pc.EvidenceSource)

	require.NotNil(t, pc.EvidenceTarget, "evidence_target must reach the wire")
	assert.Equal(t, "pkg/foo/foo.go", *pc.EvidenceTarget)

	require.NotNil(t, pc.Provenance, "provenance must reach the wire")
	assert.Equal(t, gen.JudgeVerdictPerCriterionProvenanceJudgeRead, *pc.Provenance)

	require.NotNil(t, pc.Evidence, "per-clause evidence must reach the wire")
	require.Len(t, *pc.Evidence, 2)
	assert.Equal(t, "it compiles", (*pc.Evidence)[0].Part)
	assert.Equal(t, "exit=0", (*pc.Evidence)[0].Quote)
	require.NotNil(t, (*pc.Evidence)[0].Source)
	assert.Equal(t, "machine_check", *(*pc.Evidence)[0].Source)
	require.NotNil(t, (*pc.Evidence)[0].Target)
	assert.Equal(t, "go build", *(*pc.Evidence)[0].Target)
	assert.Equal(t, "it is documented", (*pc.Evidence)[1].Part)

	// The pre-existing fields must be untouched by the addition.
	require.NotNil(t, pc.EvidenceQuote)
	assert.Equal(t, "func Foo() error { return nil }", *pc.EvidenceQuote)
	assert.Equal(t, "crit-1", pc.CriterionId)
	assert.True(t, pc.Met)
}

// TestToWireJudgeVerdictOmitsAbsentEvidenceFields pins the empty-safe half
// (JUDGE-FR-074): a verdict carrying none of the four new fields must render
// exactly as it did before they were mapped — absent, never empty strings or
// an empty array. Without this, the fix above could be satisfied by always
// emitting the fields, changing every legacy verdict's wire shape.
func TestToWireJudgeVerdictOmitsAbsentEvidenceFields(t *testing.T) {
	v := task.JudgeVerdict{
		ID:    "verdict-2",
		Scope: task.VerdictScopeTask,
		Met:   false,
		PerCriterion: []task.CriterionVerdict{{
			CriterionID: "crit-1", Met: false, Reason: "not done",
		}},
	}

	out := toWireJudgeVerdict(v)
	require.Len(t, out.PerCriterion, 1)
	pc := out.PerCriterion[0]

	assert.Nil(t, pc.EvidenceSource)
	assert.Nil(t, pc.EvidenceTarget)
	assert.Nil(t, pc.Provenance)
	assert.Nil(t, pc.Evidence)
	assert.Nil(t, pc.EvidenceQuote)

	// And the whole per-criterion object must carry no key for any of them.
	raw, err := json.Marshal(pc)
	require.NoError(t, err)
	for _, key := range []string{"evidence_source", "evidence_target", "provenance", "evidence", "evidence_quote"} {
		assert.NotContains(t, string(raw), `"`+key+`"`,
			"an absent reporting field must not appear on the wire at all: %s", raw)
	}
}

func TestWireCriterionJudgment_BackfillsLegacyEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   task.AcceptanceCriterion
		want task.JudgmentKind
	}{
		{"legacy prose, no judgment -> boolean", task.AcceptanceCriterion{Kind: task.KindProse, Text: "the script reads well", Judgment: ""}, task.JudgmentBoolean},
		{"legacy check, no judgment -> boolean", task.AcceptanceCriterion{Kind: task.KindCheck, Text: "tests pass", Judgment: "", Check: &task.CriterionCheck{Command: "go test ./...", ExpectedExitCode: 0}}, task.JudgmentBoolean},
		{"legacy behavior, no judgment -> quantitative", task.AcceptanceCriterion{Kind: task.KindBehavior, Text: "used the tool", Judgment: "", Behavior: &task.CriterionBehavior{Tool: "bash"}}, task.JudgmentQuantitative},
		{"explicit artifact preserved", task.AcceptanceCriterion{Kind: task.KindProse, Text: "a file exists", Judgment: task.JudgmentArtifact}, task.JudgmentArtifact},
		{"explicit quantitative preserved", task.AcceptanceCriterion{Kind: task.KindProse, Text: "at least 3 items", Judgment: task.JudgmentQuantitative}, task.JudgmentQuantitative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wireCriterionJudgment(tc.in)
			if got != tc.want {
				t.Fatalf("wireCriterionJudgment = %q, want %q", got, tc.want)
			}
			if got == "" {
				t.Fatal("wireCriterionJudgment must never return empty — the wire enum is required")
			}
		})
	}
}

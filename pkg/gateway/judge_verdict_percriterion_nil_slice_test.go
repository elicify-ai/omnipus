// judge_verdict_percriterion_nil_slice_test.go — regression coverage for
// fix-wave finding #3 (14-reviewer sign-off, release/v0.1.1): both
// rest_tasks.go's toWireJudgeVerdict and replay.go's toJudgeVerdictFrame
// built PerCriterion by appending onto a NIL slice. A JudgeVerdict with zero
// criteria (an empty verdict is a real, reachable case — e.g. a plan/goal
// scope verdict, or a task with no acceptance criteria at all) therefore
// left the wire field as a nil slice, which encoding/json marshals as JSON
// `null`. Both generated wire types declare per_criterion as a REQUIRED
// array with no `omitempty` (openapi_types.gen.go / asyncapi_types.gen.go),
// so a `null` there fails the SPA's zod schema and the frame gets dropped.
// The fix initializes a non-nil, empty slice before the loop in both
// functions so an empty verdict round-trips as `"per_criterion":[]`.
package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// emptyCriterionVerdict is a JudgeVerdict with zero PerCriterion entries —
// the reachable "empty verdict" case (e.g. a plan/goal-scope verdict with no
// per-criterion breakdown, or a task carrying no acceptance criteria at all).
// PerCriterion is left at its zero value (nil) deliberately, mirroring how a
// freshly-constructed task.JudgeVerdict looks before any criterion is
// appended.
func emptyCriterionVerdict() task.JudgeVerdict {
	return task.JudgeVerdict{
		ID:           "verdict-empty",
		Scope:        "plan",
		PlanID:       "plan-abc",
		Round:        1,
		Met:          true,
		Model:        "test-judge-model",
		JudgedAt:     "2026-01-01T00:00:00Z",
		JudgeAgentID: "judge",
	}
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

// TestToJudgeVerdictFrame_EmptyPerCriterion_MarshalsAsEmptyArrayNotNull
// covers replay.go's toJudgeVerdictFrame (the asyncapi JudgeVerdictFrame WS
// push and replay path) — the sibling function this bug was duplicated into
// (both build PerCriterion the same way; see toJudgeVerdictFrame's own doc
// comment on why the two aren't shared).
func TestToJudgeVerdictFrame_EmptyPerCriterion_MarshalsAsEmptyArrayNotNull(t *testing.T) {
	v := emptyCriterionVerdict()
	require.Nil(t, v.PerCriterion, "precondition: the source verdict must have a nil PerCriterion")

	f := toJudgeVerdictFrame(v)
	data, err := json.Marshal(f)
	require.NoError(t, err)

	assert.Contains(t, string(data), `"per_criterion":[]`,
		"per_criterion is a required array on the asyncapi wire (no omitempty) — "+
			"an empty verdict must still marshal it as [], not null")
	assert.NotContains(t, string(data), `"per_criterion":null`)
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

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

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestToJudgeVerdictFrame_EmptyPerCriterion_MarshalsAsEmptyArrayNotNull
// covers replay.go's toJudgeVerdictFrame (the asyncapi JudgeVerdictFrame WS
// push and replay path) — the sibling function this bug was duplicated into
// (both build PerCriterion the same way; see toJudgeVerdictFrame's own doc
// comment on why the two aren't shared).
func TestToJudgeVerdictFrame_EmptyPerCriterion_MarshalsAsEmptyArrayNotNull(t *testing.T) {
	v := emptyCriterionVerdict()
	require.Nil(t, v.PerCriterion, "precondition: the source verdict must have a nil PerCriterion")

	f := toJudgeVerdictFrame("", v)
	data, err := json.Marshal(f)
	require.NoError(t, err)

	assert.Contains(t, string(data), `"per_criterion":[]`,
		"per_criterion is a required array on the asyncapi wire (no omitempty) — "+
			"an empty verdict must still marshal it as [], not null")
	assert.NotContains(t, string(data), `"per_criterion":null`)
}

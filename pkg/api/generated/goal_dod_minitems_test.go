//go:build !windows

// goal_dod_minitems_test.go is the required missing test named by the
// code-review fix-wave: a contract/schema test proving the generated Goal
// schema (contracts/components/schemas/Goal.yaml) rejects `dod: []` —
// ADR-080 D-DOD requires `dod` to carry `minItems: 1` on the wire (the
// compiler's built-in floor layer guarantees at least one item on every
// newly-compiled goal, and loadCompiledGoal backfills the floor DoD for any
// legacy persisted goal with none, precisely so this invariant always
// holds by the time a Goal record is served). Uses the same
// validateAgainstComponentSchemaRawJSON/mustFailComponent-style harness as
// the rest of pkg/api/generated/contract_test.go (same package, same file
// build tag).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package generated

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validGoalDoDItemJSON is one schema-valid AcceptanceCriterion, shaped like
// the built-in floor DoD (newFloorDoD, pkg/agent/goal_compile.go).
const validGoalDoDItemJSON = `{"kind":"prose","judgment":"boolean","text":"no secrets leak",` +
	`"author":{"kind":"agent","id":"system"},"status":"pending"}`

// goalFixtureJSON builds a schema-shaped Goal payload (every required field
// populated) with the given raw `dod` array literal substituted in, so the
// two tests below differ ONLY in the one field under test.
func goalFixtureJSON(dodArrayLiteral string) []byte {
	return []byte(`{
		"goal_id": "goal_01J3ZQK8N2H8VXNRP5T7C9M4WU",
		"binding_kind": "session",
		"binding_id": "550e8400-e29b-41d4-a716-446655440000",
		"source": "chat_compiled",
		"prompt": "make the tests pass",
		"criteria": [],
		"dod": ` + dodArrayLiteral + `,
		"attempts_max": 3,
		"judge_rounds_max": 20,
		"round": 0,
		"state": "active",
		"created_at": "2026-07-22T10:00:00Z"
	}`)
}

// TestContract_Goal_EmptyDoDRejected is the required test: `dod: []` must
// be rejected by the generated Goal schema (minItems: 1) — an empty DoD
// array is wire-invalid, not just an engine-side convention.
func TestContract_Goal_EmptyDoDRejected(t *testing.T) {
	err := validateAgainstComponentSchemaRawJSON(t, "Goal", goalFixtureJSON("[]"))
	assert.Error(t, err, "Goal.dod must be rejected when empty — Goal.yaml declares dod: minItems: 1 (ADR-080 D-DOD)")
}

// TestContract_Goal_NonEmptyDoDAccepted is the positive control: the exact
// same fixture with one valid DoD item must pass — proving
// TestContract_Goal_EmptyDoDRejected's failure is specifically about
// `dod`'s emptiness, not some other malformed field in the shared fixture.
func TestContract_Goal_NonEmptyDoDAccepted(t *testing.T) {
	err := validateAgainstComponentSchemaRawJSON(t, "Goal", goalFixtureJSON("["+validGoalDoDItemJSON+"]"))
	require.NoError(t, err, "a Goal fixture with exactly one valid dod item must validate cleanly")
}

// TestContract_Goal_MissingDoDKeyRejected covers the sibling required-field
// case: `dod` is in Goal.yaml's top-level `required` list, so omitting the
// key entirely (not just supplying an empty array) must also be rejected.
func TestContract_Goal_MissingDoDKeyRejected(t *testing.T) {
	raw := []byte(`{
		"goal_id": "goal_01J3ZQK8N2H8VXNRP5T7C9M4WU",
		"binding_kind": "session",
		"binding_id": "550e8400-e29b-41d4-a716-446655440000",
		"source": "chat_compiled",
		"prompt": "make the tests pass",
		"criteria": [],
		"attempts_max": 3,
		"judge_rounds_max": 20,
		"round": 0,
		"state": "active",
		"created_at": "2026-07-22T10:00:00Z"
	}`)
	err := validateAgainstComponentSchemaRawJSON(t, "Goal", raw)
	assert.Error(t, err, "Goal.dod is a required top-level field — omitting it entirely must be rejected")
}

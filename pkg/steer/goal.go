// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package steer

// GoalSpec is a LaunchRequest's optional goal — criteria plus a Definition
// of Done, in the same shape create_task validates
// (pkg/tools/task.go::taskCreateToolExecute.validateRequest /
// parseCriteriaArgs, and task.AcceptanceCriterion in pkg/task). Nil on
// LaunchRequest means no goal (founder decision, round 3).
//
// pkg/steer may import neither pkg/tools nor pkg/task (landing order §2:
// only pkg/session and pkg/api/generated), so this shape is restated here
// field-for-field rather than reused by import. Phase 2's launcher body is
// what actually validates a GoalSpec against create_task's rules (US-1/AS-5:
// "a LaunchRequest.Goal that create_task would reject is rejected with an
// identical message") and maps an accepted one onto a real Goal record
// (GoalRef) — CP-0 publishes only the compiled input shape.
type GoalSpec struct {
	Criteria []Criterion
	DoD      []Criterion
}

// CriterionKind mirrors task.CriterionKind's two authoring-time values
// (kind is optional at authoring time and inferred from the payload shape
// when omitted — task.InferCriterionKind).
type CriterionKind string

const (
	CriterionKindCheck CriterionKind = "check"
	CriterionKindProse CriterionKind = "prose"
)

// JudgmentKind mirrors task.JudgmentKind (ADR-080 D-TYPES): the orthogonal
// "what shape of claim is this" axis, inferred from Kind when omitted.
type JudgmentKind string

const (
	JudgmentBoolean      JudgmentKind = "boolean"
	JudgmentQuantitative JudgmentKind = "quantitative"
	JudgmentArtifact     JudgmentKind = "artifact"
)

// CriterionCheck mirrors task.CriterionCheck — required iff Kind ==
// CriterionKindCheck.
type CriterionCheck struct {
	Command          string
	ExpectedExitCode int
}

// Criterion mirrors task.AcceptanceCriterion's authoring-time shape (the
// subset parseCriteriaArgs reads off a create_task-style payload: kind,
// judgment, text, check). ID, Author and Status are server-assigned at
// write time by phase 2's launcher body (mirroring parseCriteriaArgs /
// task.Store.normalizeCriteria) and are not part of the caller-supplied
// LaunchRequest.Goal input shape.
type Criterion struct {
	Kind     CriterionKind
	Judgment JudgmentKind
	Text     string
	// Check is set iff Kind == CriterionKindCheck; nil for every other
	// kind (no mixed shape, SD-A9).
	Check *CriterionCheck
}

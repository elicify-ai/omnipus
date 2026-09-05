// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verdict.go defines JudgeVerdict, the record produced by the Judge System
// Agent for one task attempt or plan round (ADR-049 D2/D4, spec Part A §C).
// Persisted alongside the run and also emitted as a `judge_verdict` transcript
// entry (pkg/session/daypartition.go).
package task

// The three JudgeVerdict.Scope values.
const (
	VerdictScopeTask = "task"
	VerdictScopePlan = "plan"
	// VerdictScopeGoal marks a verdict produced for a `/goal <condition>`
	// session round (ADR-049 Part B US-8). A goal-scope verdict carries
	// neither TaskID nor PlanID — it is correlated by the session the
	// judge_verdict transcript entry is written into.
	VerdictScopeGoal = "goal"
)

// CriterionVerdict is the judge's per-criterion outcome making up one
// JudgeVerdict. Reason feeds forward as steering context on the next attempt
// when Met is false (evaluator-optimizer pattern, ADR D2).
type CriterionVerdict struct {
	// CriterionID identifies the AcceptanceCriterion this verdict judges.
	CriterionID string `json:"criterion_id"`
	// Met is fail-closed default false — absence of evidence never defaults
	// to true (NFR-2).
	Met bool `json:"met"`
	// Reason is the judge's rationale for this criterion.
	Reason string `json:"reason"`
	// EvidenceQuote (ADR-074 D7) is the verbatim evidence excerpt the judge
	// grounded this verdict in, copied out of the UNTRUSTED-DATA region of
	// its input (diff/window/claim). Optional and empty-safe: empty on every
	// fail-closed verdict, every pre-D7 persisted verdict, and installs whose
	// Judge soul predates the quote-emitting rubric. Truncated rune-safe to
	// 500 code points at the parser (pkg/agent/judge.go). UNTRUSTED CONTENT:
	// any re-emission into another agent's prompt MUST wrap it in the same
	// UNTRUSTED-DATA framing buildJudgeUserContent uses — never bare trusted
	// text; the UI renders it as inert quoted text.
	EvidenceQuote string `json:"evidence_quote,omitempty"`
}

// JudgeVerdict is the Judge System Agent's overall PASS/FAIL verdict for one
// task attempt or plan round, plus its per-criterion breakdown. Persisted
// alongside the run and written as a `judge_verdict` transcript entry so it
// cannot silently disagree with the worker's own completion marker (ADR §6).
//
// Absence of a JudgeVerdict never defaults to success (NFR-2) — Met's
// zero-value is false, and callers must never synthesize a verdict when none
// was actually produced.
type JudgeVerdict struct {
	// ID is the server-set verdict identifier (UUID).
	ID string `json:"id"`
	// Scope is "task", "plan", or "goal".
	Scope string `json:"scope"`
	// TaskID is set when Scope == VerdictScopeTask.
	TaskID string `json:"task_id,omitempty"`
	// PlanID is set when Scope == VerdictScopePlan.
	PlanID string `json:"plan_id,omitempty"`
	// GoalSessionID is set when Scope == VerdictScopeGoal (ADR-052 FR-037):
	// the chat session carrying the /goal condition this verdict adjudicated,
	// so a goal-scope verdict stays correlated to its session like task/plan
	// verdicts correlate via TaskID/PlanID.
	GoalSessionID string `json:"goal_session_id,omitempty"`
	// Round is the attempt/round index (ADR D7: a "round" is one worker turn
	// plus its judge evaluation).
	Round int `json:"round"`
	// Met is the overall PASS/FAIL verdict, fail-closed default false.
	Met bool `json:"met"`
	// PerCriterion carries the per-criterion outcomes making up Met.
	PerCriterion []CriterionVerdict `json:"per_criterion"`
	// Model is the judge model used to produce this verdict (transparency /
	// NFR-5 metering).
	Model string `json:"model"`
	// JudgedAt is an RFC 3339 UTC timestamp.
	JudgedAt string `json:"judged_at"`
	// JudgeAgentID is the Judge System Agent's agent_id, correlated with the
	// plan/task/goal IDs for usage metering (NFR-5).
	JudgeAgentID string `json:"judge_agent_id"`
}

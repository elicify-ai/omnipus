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

// VerdictEvidenceSource is JUDGE-FR-065/FR-066's derived, REPORTING-only
// origin of a CriterionVerdict's grounding evidence — server-derived, never
// trusted from model output. OPTIONAL (D-B, ADR-084 revision 9 §10):
// absence, or a value that does not verify, NEVER flips Met to anything
// else; it never gates a verdict, it only explains one.
type VerdictEvidenceSource string

// The five VerdictEvidenceSource values (contracts/components/schemas/CriterionVerdict.yaml).
const (
	EvidenceSourceDiff         VerdictEvidenceSource = "diff"
	EvidenceSourceTranscript   VerdictEvidenceSource = "transcript"
	EvidenceSourceMachineCheck VerdictEvidenceSource = "machine_check"
	EvidenceSourceFileRead     VerdictEvidenceSource = "file_read"
	EvidenceSourceSessionRead  VerdictEvidenceSource = "session_read"
)

// IsValidVerdictEvidenceSource reports whether s is a known evidence source,
// or empty (the field is optional and legitimately absent — e.g. a
// fail-closed verdict, or a legacy rubric that emits no evidence_source).
func IsValidVerdictEvidenceSource(s VerdictEvidenceSource) bool {
	switch s {
	case "", EvidenceSourceDiff, EvidenceSourceTranscript, EvidenceSourceMachineCheck,
		EvidenceSourceFileRead, EvidenceSourceSessionRead:
		return true
	default:
		return false
	}
}

// VerdictProvenance is JUDGE-FR-065's investigation-log provenance for a
// CriterionVerdict: deterministic_check when a veto or check evidence
// decided it; judge_read/diff/transcript/session_read mapped from the
// validated evidence source when the Judge's own reading decided it; none
// when neither applies. OPTIONAL REPORTING field only (D-B) — the Judge's
// authority to rule Met on reasoned conviction alone is never conditioned on
// this field being present or non-none.
type VerdictProvenance string

// The six VerdictProvenance values (contracts/components/schemas/CriterionVerdict.yaml).
const (
	ProvenanceJudgeRead      VerdictProvenance = "judge_read"
	ProvenanceDeterministic  VerdictProvenance = "deterministic_check"
	ProvenanceDiffRead       VerdictProvenance = "diff"
	ProvenanceTranscriptRead VerdictProvenance = "transcript"
	ProvenanceSessionRead    VerdictProvenance = "session_read"
	ProvenanceNone           VerdictProvenance = "none"
)

// IsValidVerdictProvenance reports whether p is a known provenance value, or
// empty (the field is optional and legitimately absent).
func IsValidVerdictProvenance(p VerdictProvenance) bool {
	switch p {
	case "", ProvenanceJudgeRead, ProvenanceDeterministic, ProvenanceDiffRead,
		ProvenanceTranscriptRead, ProvenanceSessionRead, ProvenanceNone:
		return true
	default:
		return false
	}
}

// CriterionEvidenceEntry is one entry of CriterionVerdict.Evidence — one
// clause of the judged criterion answered with its own grounding excerpt
// (JUDGE-FR-006). Part and Quote are always present (Quote MAY be an empty
// string when the Judge could not locate grounding for this clause and is
// reporting that gap rather than fabricating a quote — D-B: an empty/failed
// entry here is reported, never fabricated, and never by itself flips the
// overall verdict). Source and Target are optional.
type CriterionEvidenceEntry struct {
	// Part is the clause text (a substring of the criterion's own Text) this
	// entry answers.
	Part string `json:"part"`
	// Source is where this clause's grounding excerpt came from. Plain
	// string, not a closed enum (unlike the verdict-level EvidenceSource
	// above) — a REPORTING detail only, never compared against by code the
	// way the top-level EvidenceSource is.
	Source string `json:"source,omitempty"`
	// Target is the specific artifact this clause's excerpt was read from.
	Target string `json:"target,omitempty"`
	// Quote is the verbatim, rune-truncated (500 code points) grounding
	// excerpt for this clause. UNTRUSTED CONTENT — same framing obligation
	// as EvidenceQuote below.
	Quote string `json:"quote"`
}

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
	// EvidenceSource (JUDGE-FR-070a, C-02) is the derived origin of this
	// verdict's grounding evidence. OPTIONAL REPORTING field only (D-B) —
	// see VerdictEvidenceSource. Populated by the mapping loop in
	// pkg/agent/verifier_adjudication.go (out of this package's scope).
	EvidenceSource VerdictEvidenceSource `json:"evidence_source,omitempty"`
	// EvidenceTarget (JUDGE-FR-065/FR-070a) is the specific artifact
	// EvidenceSource's evidence was read from, paired with EvidenceSource.
	// OPTIONAL REPORTING field only (D-B) — never a proof gate.
	EvidenceTarget string `json:"evidence_target,omitempty"`
	// Provenance (JUDGE-FR-070a) is this verdict's investigation-log
	// provenance. OPTIONAL REPORTING field only (D-B) — see
	// VerdictProvenance.
	Provenance VerdictProvenance `json:"provenance,omitempty"`
	// Evidence (JUDGE-FR-006/FR-070a) is a new, OPTIONAL sibling field
	// alongside EvidenceQuote — one entry per clause of the criterion this
	// verdict judges. EvidenceQuote keeps its existing type/length/
	// optionality unchanged for every reader that does not know about this
	// field (C2); when Evidence is present the engine populates
	// EvidenceQuote from Evidence[0].Quote (FR-071) so no existing
	// persisted-verdict reader, replay frame or SPA render is affected. A
	// REPORTING obligation the Judge uses to show its work (D-B) — never a
	// gate that can turn Met into false.
	//
	// NOTE (C-02, D-H): there is deliberately no Outcome field here.
	// ADR-084 revision 9 withdraws the three-state outcome in full — Met
	// stays the only verdict-shape bool, and `unable_to_verify` is retired
	// everywhere. Do not reintroduce either.
	Evidence []CriterionEvidenceEntry `json:"evidence,omitempty"`
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

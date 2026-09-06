// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// criterion.go defines AcceptanceCriterion and its nested types (ADR-049 D2,
// spec Part A §C). Shared by task.Task.Criteria and (in pkg/plan) Plan.DoD —
// keep these types free of any pkg/task-specific dependency so pkg/plan can
// import them cleanly.
package task

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// CriterionKind discriminates a machine-checkable command, a free-text prose
// statement judged by the Judge System Agent, or a deterministic behavior
// check against the session tool-call log.
type CriterionKind string

// The three criterion kinds (ADR D2; behavior added by ADR-052 FR-034).
const (
	KindCheck    CriterionKind = "check"
	KindProse    CriterionKind = "prose"
	KindBehavior CriterionKind = "behavior"
)

// IsValidCriterionKind reports whether k is a known criterion kind. Any other
// value fails closed — validateCriterion is reached only after this gate, so
// there is no unrecognized-kind fallthrough left to guard in its switch.
//
// Invariant (ADR-074 D2): authoring-time payloads may omit kind — it is
// resolved by InferCriterionKind before validation — but every PERSISTED
// criterion always carries an explicit, valid kind: normalizeCriteria infers
// then validates on every store write, so an empty or unknown kind never
// reaches disk (guards criterionKey/sameShape consumers, required-test #4).
func IsValidCriterionKind(k CriterionKind) bool {
	return k == KindCheck || k == KindProse || k == KindBehavior
}

// InferCriterionKind resolves a criterion's effective kind when the authoring
// payload omitted it (ADR-074 D2). The rule: absent kind + check payload =>
// check; + behavior payload => behavior; + no payload => prose. Absent kind
// with BOTH payloads is ambiguous and is an error (EC-1). An EXPLICIT kind is
// returned unchanged — inference never overrides it; a mismatch between an
// explicit kind and its payload stays a 400 in validateCriterion (shape rules
// unchanged).
//
// This is the ONLY inference implementation (spec FR-002): its call sites are
// exactly the two tool-layer criteria parsers (pkg/tools/task.go's
// parseCriteriaArgs, pkg/sysagent/tools/task.go's twin — both BEFORE the
// ADR-049 D2-rule-5 all-check bash-policy gate, so the gate fires on inferred
// kinds too) and normalizeCriteria below (which covers the REST paths: the
// gateway converters pass an absent kind THROUGH as empty and never default
// it themselves).
func InferCriterionKind(c *AcceptanceCriterion) (CriterionKind, error) {
	if c.Kind != "" {
		return c.Kind, nil
	}
	switch {
	case c.Check != nil && c.Behavior != nil:
		return "", fmt.Errorf("kind is omitted and both check and behavior payloads are present — " +
			"ambiguous; supply kind explicitly or remove one payload")
	case c.Check != nil:
		return KindCheck, nil
	case c.Behavior != nil:
		return KindBehavior, nil
	default:
		return KindProse, nil
	}
}

// BehaviorScope enumerates CriterionBehavior.Scope (ADR-052 FR-034): whether
// the deterministic tool-call count is read from just the current dispatch
// attempt's log, or the whole task session (every attempt).
type BehaviorScope string

// The two valid BehaviorScope values. BehaviorScopeTaskSession is the default
// when a payload omits scope.
const (
	BehaviorScopeAttempt     BehaviorScope = "attempt"
	BehaviorScopeTaskSession BehaviorScope = "task_session"
)

// IsValidBehaviorScope reports whether s is a known behavior scope.
func IsValidBehaviorScope(s BehaviorScope) bool {
	return s == BehaviorScopeAttempt || s == BehaviorScopeTaskSession
}

// CriterionBehavior is the payload of a `kind: behavior` criterion (ADR-052
// FR-034): the criterion is met when the count of SUCCESSFUL calls to Tool
// within Scope falls in [EffectiveMinCount(), MaxCount] (MaxCount nil =
// unbounded above). MinCount=0 (explicit) with MaxCount pointing at 0
// expresses "never call Tool"; MinCount=0 (explicit) with MaxCount pointing
// at N>0 expresses "call it 0..N times" (calling it at all is optional).
//
// This type is the payload SHAPE plus its own strict validation only — the
// deterministic scanner that reads the session tool-call log and evaluates a
// criterion against this payload lives in the runtime engine, not here.
type CriterionBehavior struct {
	// Tool is the tool name whose successful-call count is scanned. Required.
	Tool string `json:"tool"`
	// MinCount is the minimum number of successful Tool calls required within
	// Scope. Pointer so an OMITTED value (nil) defaults to 1, while an
	// EXPLICIT zero (MinCount != nil, *MinCount == 0) stays zero — this is
	// what makes "0..N calls, calling it at all is optional" expressible;
	// "never call Tool" is the plain {min_count:0, max_count:0} pairing, with
	// no special-case sentinel needed (fix-wave finding #5 — the pointer
	// refactor replaces the prior "0 unless paired with max_count:0" hack).
	// Must be >= 0 when set. Normalized (defaulted to 1 when nil) by
	// validateCriterionBehavior, so a persisted criterion always carries an
	// explicit value; use EffectiveMinCount() to read the resolved floor
	// without re-deriving the nil-default at each call site.
	MinCount *int `json:"min_count,omitempty"`
	// MaxCount is the maximum number of successful Tool calls allowed within
	// Scope. nil = unbounded. When set, MUST be >= EffectiveMinCount().
	MaxCount *int `json:"max_count,omitempty"`
	// Scope is "attempt" or "task_session" (default when empty).
	Scope BehaviorScope `json:"scope,omitempty"`
}

// EffectiveMinCount returns the resolved minimum successful-call floor: an
// explicit MinCount value (including an explicit zero), or 1 when MinCount is
// nil (the FR-034 default). Callers that read the floor for comparison logic
// (the deterministic scanner, validation, reason rendering) should always go
// through this method rather than dereferencing MinCount directly, so the
// nil-default is resolved in exactly one place.
func (b *CriterionBehavior) EffectiveMinCount() int {
	if b == nil || b.MinCount == nil {
		return 1
	}
	return *b.MinCount
}

// UnmarshalJSON implements json.Unmarshaler for CriterionBehavior. It rejects
// unknown fields (ADR-052 FR-034: "unknown fields reject") — a payload typo
// or an unsupported future key fails closed at decode time rather than being
// silently dropped.
func (b *CriterionBehavior) UnmarshalJSON(data []byte) error {
	type alias CriterionBehavior // breaks the recursive UnmarshalJSON call
	var a alias
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return err
	}
	*b = CriterionBehavior(a)
	return nil
}

// CriterionStatus is the per-run judgement status of a criterion.
type CriterionStatus string

// The three criterion statuses. Absence of a verdict never defaults to
// CritMet (NFR-2) — a fresh/unjudged criterion is CritPending.
const (
	CritPending CriterionStatus = "pending"
	CritMet     CriterionStatus = "met"
	CritUnmet   CriterionStatus = "unmet"
)

// IsValidCriterionStatus reports whether s is a known criterion status.
func IsValidCriterionStatus(s CriterionStatus) bool {
	switch s {
	case CritPending, CritMet, CritUnmet:
		return true
	default:
		return false
	}
}

// maxCriterionTextRunes bounds AcceptanceCriterion.Text (spec Part A §C).
const maxCriterionTextRunes = 1000

// CriterionCheck is the machine-checkable half of a `kind: check` criterion.
// The command is dispatched through the assignee agent's existing `bash` tool
// machinery (ADR D2 rule 1) — same tool registry, policy resolution, sandbox
// enforcement, and audit trail as any other bash call.
type CriterionCheck struct {
	// Command is the shell command run through the assignee's bash tool.
	Command string `json:"command"`
	// ExpectedExitCode is the exit code that counts as PASS (met), 0..255.
	ExpectedExitCode int `json:"expected_exit_code"`
}

// CriterionAuthor records the identity of whoever authored a criterion (ADR D2
// rule 3). Mandatory on every criterion — author attribution is what lets a
// cross-agent-authored machine check require assignee-owner confirmation.
type CriterionAuthor struct {
	// Kind is "agent" or "user".
	Kind string `json:"kind"`
	// ID is the agent ID or username of the author.
	ID string `json:"id"`
}

// The two valid CriterionAuthor.Kind values.
const (
	AuthorKindAgent = "agent"
	AuthorKindUser  = "user"
)

// AcceptanceCriterion is one Definition-of-Done item on a Task (Task.Criteria)
// or a Plan (Plan.DoD, pkg/plan). Shared type — kept dependency-free so
// pkg/plan can import it without importing anything else from pkg/task.
type AcceptanceCriterion struct {
	// ID is the server-set criterion identifier (UUID). Generated by
	// normalizeCriteria when absent on a create-time payload.
	ID string `json:"id"`
	// Kind is check (machine-checkable) or prose (LLM-judged).
	Kind CriterionKind `json:"kind"`
	// Text is the criterion statement (prose) or a human-readable description
	// of what the check verifies (check). 1..1000 runes.
	Text string `json:"text"`
	// Check is required iff Kind == KindCheck; must be nil for every other
	// kind (no mixed shape, SD-A9).
	Check *CriterionCheck `json:"check,omitempty"`
	// Behavior is required iff Kind == KindBehavior; must be nil for every
	// other kind (no mixed shape, mirrors Check; ADR-052 FR-034).
	Behavior *CriterionBehavior `json:"behavior,omitempty"`
	// Author records who authored this criterion (mandatory, ADR D2 rule 3).
	Author CriterionAuthor `json:"author"`
	// Status is the per-run judgement status; defaults to CritPending.
	Status CriterionStatus `json:"status"`
}

// normalizeCriteria server-sets a missing ID, defaults an empty Status to
// pending, and validates each criterion's shape (SD-A9, FR-014/015). It does
// NOT enforce "at least one criterion" (SD-A7's agent-path tiering) — that
// tiered rule depends on caller identity (agent tool vs. human/UI) which the
// store does not know; it is enforced by the caller.
//
// It works on a DEEP copy and never writes through the caller's slice or the
// structs it points at. It used to
// mutate in place, which made the store scribble server-set IDs into memory
// the caller still owned — so two goroutines normalising slices that share a
// backing array raced, with no lock anywhere in sight to suggest they might.
// Found by `go test -race ./pkg/tools/...`, a package neither CI race gate
// covered; the reporting sites were two parallel subtests sharing one
// fixture slice, but the hazard is the API's, not the test's: every one of
// the five call sites already assigns the returned slice, so nothing wanted
// the in-place behaviour in the first place.
func normalizeCriteria(criteria []AcceptanceCriterion) ([]AcceptanceCriterion, error) {
	if criteria == nil {
		return nil, nil
	}
	normalized := make([]AcceptanceCriterion, len(criteria))
	copy(normalized, criteria)
	for i := range normalized {
		c := &normalized[i]
		// DEEP copy, not just the slice. AcceptanceCriterion holds two
		// pointers, and validateCriterion server-sets values THROUGH them:
		// validateCriterionBehavior writes b.MinCount and b.Scope. A shallow
		// slice copy shares those structs, so the store would still scribble
		// into the caller's memory and two goroutines would still race —
		// which is exactly what the first version of this fix did, with a
		// test that only exercised the one kind carrying no pointer state.
		if c.Behavior != nil {
			b := *c.Behavior
			if b.MinCount != nil {
				n := *b.MinCount
				b.MinCount = &n
			}
			if b.MaxCount != nil {
				n := *b.MaxCount
				b.MaxCount = &n
			}
			c.Behavior = &b
		}
		if c.Check != nil {
			ck := *c.Check
			c.Check = &ck
		}
		if c.ID == "" {
			c.ID = uuid.New().String()
		}
		if c.Status == "" {
			c.Status = CritPending
		}
		// ADR-074 D2: an authoring payload may omit kind — infer it from the
		// payload shape BEFORE validation, so every persisted criterion
		// carries an explicit, valid kind (the IsValidCriterionKind
		// invariant). Explicit kinds pass through unchanged; a dual-payload
		// kind-less criterion is rejected here (EC-1).
		k, kErr := InferCriterionKind(c)
		if kErr != nil {
			return nil, verr("criteria[%d]: %v", i, kErr)
		}
		c.Kind = k
		if err := validateCriterion(c, i); err != nil {
			return nil, err
		}
	}
	return normalized, nil
}

// NormalizeCriteria is the exported form of normalizeCriteria, for other
// packages that hold their own []AcceptanceCriterion slice needing the exact
// same shape validation this store applies to Task.Criteria — e.g. pkg/plan's
// Plan.DoD (ADR-049 D1), which is deliberately the SAME AcceptanceCriterion
// type so the judge, evidence, and verdict machinery are uniform across task
// and plan scopes. Same semantics as normalizeCriteria: server-sets a missing
// ID, defaults empty Status to pending, validates shape, and does NOT enforce
// "at least one criterion" (that tiered SD-A7 rule is the caller's job).
func NormalizeCriteria(criteria []AcceptanceCriterion) ([]AcceptanceCriterion, error) {
	return normalizeCriteria(criteria)
}

// validateCriterion validates a single criterion's shape (spec Part A §C /
// Dataset: Criterion validation). idx is used only to name the field in the
// returned error.
func validateCriterion(c *AcceptanceCriterion, idx int) error {
	if !IsValidCriterionKind(c.Kind) {
		return verr("criteria[%d]: invalid kind %q (must be \"check\", \"prose\", or \"behavior\")", idx, c.Kind)
	}
	n := len([]rune(c.Text))
	if n < 1 {
		return verr("criteria[%d]: text is required", idx)
	}
	if n > maxCriterionTextRunes {
		return verr("criteria[%d]: text must be %d characters or fewer", idx, maxCriterionTextRunes)
	}
	switch c.Kind {
	case KindCheck:
		if c.Check == nil || c.Check.Command == "" {
			return verr("criteria[%d]: check criterion requires command", idx)
		}
		if c.Check.ExpectedExitCode < 0 || c.Check.ExpectedExitCode > 255 {
			return verr("criteria[%d]: expected_exit_code must be between 0 and 255", idx)
		}
		if c.Behavior != nil {
			return verr("criteria[%d]: check criterion must not carry a behavior payload", idx)
		}
	case KindProse:
		if c.Check != nil {
			return verr("criteria[%d]: prose criterion must not carry a check object", idx)
		}
		if c.Behavior != nil {
			return verr("criteria[%d]: prose criterion must not carry a behavior payload", idx)
		}
	case KindBehavior:
		if c.Check != nil {
			return verr("criteria[%d]: behavior criterion must not carry a check object", idx)
		}
		if err := validateCriterionBehavior(c.Behavior, idx); err != nil {
			return err
		}
	}
	if c.Author.Kind != AuthorKindAgent && c.Author.Kind != AuthorKindUser {
		return verr("criteria[%d]: author.kind must be \"agent\" or \"user\"", idx)
	}
	if c.Author.ID == "" {
		return verr("criteria[%d]: author.id is required", idx)
	}
	if !IsValidCriterionStatus(c.Status) {
		return verr("criteria[%d]: invalid status %q", idx, c.Status)
	}
	return nil
}

// validateCriterionBehavior validates (and, for MinCount/Scope, defaults) a
// KindBehavior criterion's payload (ADR-052 FR-034, spec Part A §C). It
// mutates b in place to apply the documented defaults, mirroring how
// normalizeCriteria defaults an unset Status before validating. idx is used
// only to name the field in the returned error.
//
// Fix-wave finding #5 deleted the old "MinCount==0 defaults to 1 UNLESS
// max_count is also 0" sentinel: MinCount is now *int, so nil (omitted) is
// unambiguous and defaults to 1, while an explicit zero simply stays zero —
// there is no longer a special "never call" combo to detect here.
func validateCriterionBehavior(b *CriterionBehavior, idx int) error {
	if b == nil {
		return verr("criteria[%d]: behavior criterion requires a behavior payload", idx)
	}
	if b.Tool == "" {
		return verr("criteria[%d]: behavior.tool is required", idx)
	}
	if b.MinCount != nil && *b.MinCount < 0 {
		return verr("criteria[%d]: behavior.min_count must not be negative", idx)
	}
	if b.MinCount == nil {
		one := 1
		b.MinCount = &one
	}
	if b.MaxCount != nil && *b.MaxCount < *b.MinCount {
		return verr("criteria[%d]: behavior.max_count must be >= min_count", idx)
	}
	if b.Scope == "" {
		b.Scope = BehaviorScopeTaskSession
	}
	if !IsValidBehaviorScope(b.Scope) {
		return verr("criteria[%d]: invalid behavior.scope %q (must be \"attempt\" or \"task_session\")", idx, b.Scope)
	}
	return nil
}

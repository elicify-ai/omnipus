// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

// lint.go — plan-lint write-set disjointness + join-point enforcement
// (ADR-053 §3c, acceptance G-16, FR-156/FR-159, US-11, conformance g4/g5).
//
// Lint runs at APPROVE (called from both approve paths — the create_plan
// tool's execute_plan, pkg/tools/plan.go, and the human/UI REST path,
// handlePlanApprove in pkg/gateway/rest_plans.go) and REJECTS a plan whose
// member tasks either:
//
//  1. declare two PARALLEL (no blocked_by ordering between them) write_sets
//     that overlap on a concrete path — the plan would otherwise dispatch
//     into silent last-write-wins (G-16 AS-1); or
//  2. converge >=2 mutually-parallel predecessors at a member that is not
//     itself an authored join/assemble member (IsJoin==true) with its own
//     acceptance criteria (G-16 AS-2, FR-159).
//
// "Parallel" is determined TOPOLOGICALLY from each member's BlockedBy DAG
// edges (transitive closure), not from the informational Task.Stream label —
// two members with no ancestor/descendant relationship in either direction
// can run concurrently and are therefore "parallel" for lint purposes,
// whether or not they share (or even set) a Stream id. An empty WriteSet is
// the declared-exploratory case (D10) and is exempt from the overlap check —
// it runs in its own isolated checkout at runtime instead (Phase 2, out of
// this package's scope); see CorrectionEvent's doc for how a genuine
// same-file conflict discovered THERE (at merge time, not at approve) is
// meant to surface.
import (
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// LintViolationKind discriminates why plan-lint rejected a plan.
type LintViolationKind string

const (
	// LintOverlap: two parallel members declare write_set paths that
	// overlap (FR-156, G-16 AS-1).
	LintOverlap LintViolationKind = "write_set_overlap"
	// LintJoinless: a member converges >=2 parallel predecessors without
	// being an authored join member (IsJoin + own criteria) (FR-156/FR-159,
	// G-16 AS-2).
	LintJoinless LintViolationKind = "join_less_convergence"
	// LintEmptyPlan: the plan has no member tasks at all.
	//
	// This is an ARITY PRECONDITION rather than a pairwise invariant, and it
	// lives here — inside Lint — rather than at either approve call site
	// because Lint is the ONE choke point both of them already share (the
	// create_plan tool's execute_plan, pkg/tools/plan.go, and the human/UI
	// REST path, handlePlanApprove in pkg/gateway/rest_plans.go). A gate
	// implemented at one call site and not the other is not a gate.
	//
	// Why it must reject rather than vacuously pass (UAT defect A): every
	// other check in this file is a predicate over PAIRS of members, so on an
	// empty member list all of them are trivially satisfied and the plan
	// sailed through approve into `running`. A PlanSupervisor then populated
	// the empty plan with auto-generated members via a correction — and
	// corrections had no lint of their own — so the overlap and join-point
	// invariants were never applied to those members at all. Approving empty
	// was therefore a complete, one-step bypass of plan-lint, not merely a
	// cosmetic gap. A plan with no members also cannot satisfy its own
	// Definition of Done by construction, so there is no legitimate case in
	// which approving one is the right outcome.
	LintEmptyPlan LintViolationKind = "empty_plan"
)

// LintViolation is a single plan-lint finding. Field tags follow the same
// snake_case convention the existing FR-084 task_errors payload uses
// (pkg/tools/plan.go's planTaskError / rest_plans.go's PlanApproveError
// TaskErrors) — this is tool-result/LLM-facing JSON, not a generated wire
// type (not-wire-format).
type LintViolation struct {
	Kind LintViolationKind `json:"kind"`
	// MemberIDs are the offending member task ID(s): two for LintOverlap
	// (the colliding pair), one for LintJoinless (the convergence member).
	MemberIDs []string `json:"member_ids"`
	// Paths are the specific overlapping path(s) named for LintOverlap.
	// Empty for LintJoinless.
	Paths []string `json:"paths,omitempty"`
	// Reason is a clear, actionable, human-readable description naming the
	// offending members/paths — this is what a calling agent or the
	// approving human sees.
	Reason string `json:"reason"`
}

// LintError is a plan-lint rejection (ADR-053 FR-156/159, G-16). It wraps
// ErrValidation so callers' existing errors.Is(err, ErrValidation) 400
// mapping picks it up unchanged, while exposing the STRUCTURED violation
// list for a caller that wants more than the flattened Error() string (the
// execute_plan tool encodes Violations as JSON, mirroring the existing
// FR-084 task_errors payload shape it already returns).
type LintError struct {
	// PlanID is the plan that failed lint.
	PlanID string
	// Violations is never empty on a non-nil *LintError.
	Violations []LintViolation
}

// Error implements the error interface. It names every violation so the
// rejection is immediately actionable without a second round-trip.
func (e *LintError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return fmt.Sprintf("%s: plan-lint rejected with no violations recorded (internal error)", ErrValidation)
	}
	reasons := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		reasons = append(reasons, v.Reason)
	}
	return fmt.Sprintf("%s: plan-lint rejected plan %q with %d violation(s): %s",
		ErrValidation, e.PlanID, len(e.Violations), strings.Join(reasons, "; "))
}

// Unwrap lets errors.Is(err, ErrValidation) — and therefore every existing
// plan-validation-error-to-HTTP-400 mapping — see through a *LintError
// unchanged.
func (e *LintError) Unwrap() error { return ErrValidation }

// Lint validates a plan's member tasks against the write-set-disjointness +
// join-point invariants (FR-156/FR-159, G-16) and returns a non-nil
// *LintError naming every violation found, or nil when the plan is clean.
// Callers pass p (for PlanID, used only in messages/events) and the plan's
// member tasks (task.Store.List(task.Filter{PlanID: p.ID})) — Lint itself
// never touches a store; it is a pure function over the data the caller
// already has, mirroring how the existing FR-084 member-criteria gate is
// implemented inline at both approve call sites today.
//
// Every violation found ALSO raises a CorrectionEvent, logged via slog.Warn
// (see logCorrectionEvent) — "surfaced, never silent" (FR-157) holds even
// though the primary surfacing here is the returned rejection itself; this
// gives the eventual Phase-2 owner-loop consumer (pkg/agent, out of this
// package's scope) a working precedent for the SAME typed signal a runtime
// merge-time conflict (CorrectionKindMergeConflict, see NewMergeConflictEvent)
// will emit once that consumer exists.
func Lint(p *Plan, members []task.Task) *LintError {
	// A nil plan stays a no-op: p is used only to label messages/events, so
	// there is no plan identity to reject against and nothing meaningful to
	// say. This is pure nil-safety for a programming error, NOT a statement
	// that the member set is acceptable.
	if p == nil {
		return nil
	}

	// Arity precondition, checked before the pairwise invariants because it
	// is the one condition under which all of them are vacuously true. See
	// LintEmptyPlan's doc comment for why passing here was a total bypass of
	// this entire file.
	if len(members) == 0 {
		v := emptyPlanViolation(p.ID)
		logCorrectionEvent(v.toCorrectionEvent(p.ID))
		return &LintError{PlanID: p.ID, Violations: []LintViolation{v}}
	}

	violations := lintViolations(members)
	if len(violations) == 0 {
		return nil
	}

	for _, v := range violations {
		logCorrectionEvent(v.toCorrectionEvent(p.ID))
	}
	return &LintError{PlanID: p.ID, Violations: violations}
}

// lintViolations is the pure pairwise core shared by Lint and LintCorrection:
// every violation in a NON-EMPTY member set, computed and returned, with
// nothing logged and no *LintError built. LintCorrection needs it precisely
// because it runs the checks TWICE (once on the plan as it stands, once on the
// set the correction would produce) and must not raise a CorrectionEvent for
// the baseline pass.
func lintViolations(members []task.Task) []LintViolation {
	idx := membersByID(members)
	ancestors := ancestorSets(members, idx)
	violations := lintOverlaps(members, ancestors)
	return append(violations, lintJoinlessConvergence(members, idx, ancestors)...)
}

// emptyPlanViolation builds the LintEmptyPlan arity violation for planID. It
// is shared by Lint and LintCorrection so the two paths cannot drift into
// saying different things about the same condition.
func emptyPlanViolation(planID string) LintViolation {
	return LintViolation{
		Kind: LintEmptyPlan,
		Reason: fmt.Sprintf(
			"plan %q has no member tasks; a plan must have at least one member task before it can be "+
				"approved or corrected — an empty plan cannot satisfy its Definition of Done, and "+
				"approving one would let members added later (e.g. by a supervision correction) skip "+
				"the write-set and join-point checks entirely",
			planID),
	}
}

// LintCorrection applies Lint's checks to the member set a correction WOULD
// produce if committed — the plan's current members, plus req's tail members,
// with req's tail edges applied as BlockedBy dependencies — and rejects the
// correction for the violations it INTRODUCES, not for the ones the plan was
// already carrying (see "WHAT THIS LINT IS FOR" below).
//
// WHY THIS EXISTS (UAT defect A, second half). Lint had exactly two call
// sites, both at APPROVE. A PlanSupervisor correction (plan_correct) adds
// brand-new member tasks and brand-new dependency edges to an ALREADY-RUNNING
// plan, and nothing linted them — ever. So the write-set-overlap and
// join-point invariants applied only to the members a plan was born with, and
// any member added afterwards was exempt by construction. Observed live: a
// supervisor-added member converging FOUR predecessors (two of them mutually
// parallel) with is_join=false — a textbook join_less_convergence violation —
// was committed without complaint, because no lint ran on that path.
//
// Two deliberate adjustments to the projected set, neither of which weakens
// the checks for live work:
//
//  1. The SUPERSEDED member (supersede verb) is dropped entirely. Its outcome
//     is by definition discounted and replaced by the tail members, and the
//     engine already hides it from the Judge for the same reason
//     (supersededMemberSet). Keeping it would make the canonical supersede
//     pattern — replace member X with X' writing the same file — self-
//     rejecting.
//
//  2. A member already in status `done` has its WriteSet cleared. The overlap
//     check asks "can these two run CONCURRENTLY and clobber each other?", and
//     a done member will not run again; without this, appending any member
//     that touches a file an earlier, finished member wrote would be rejected
//     as a parallel conflict that cannot actually occur. Clearing WriteSet
//     (rather than removing the member) routes it through lintOverlaps' own
//     pre-existing exploratory-member exemption while leaving the member —
//     and therefore the DAG topology — fully intact for the join/ancestor
//     analysis. That distinction is what still catches the live defect above,
//     whose four predecessors were all done at correction time.
//
// `failed` members are deliberately NOT given the done treatment: the engine
// auto-resets live-round failed members back to `next` right after a
// correction commits (autoResetLiveRoundFailedMembers), so they DO run again
// and their write-sets can still race.
//
// WHAT THIS LINT IS FOR, AND WHAT IT MUST NOT DO (H2 review finding against
// the first version, which was a bare `Lint(p, projectCorrectedMembers(...))`).
// A correction lint exists to stop a correction from INTRODUCING a violation.
// Linting the whole projected set instead rejected a correction for violations
// the plan was ALREADY carrying, and that turns out to be self-defeating:
//
//   - A running plan carrying a pre-existing join-less convergence (member
//     `gamma`, is_join=false, converging two parallel members) parks at
//     PhaseStalled. The whole point of parking there — stated in the engine's
//     own surfaceJudgeUnavailableStall — is that PhaseStalled is a phase where
//     plan_correct IS accepted, so the park "unlocks the mechanism the bug had
//     locked out". The adjudicator then appends a fix member `delta`; the lint
//     projects {alpha,beta,gamma,delta}, trips on `gamma`, and rejects a
//     correction that never mentioned gamma.
//   - There is no way out. A correction cannot edit an existing member
//     (supersede requires the target be `done`), so gamma's violation cannot
//     be repaired by any correction. Every retry fails identically, the
//     bounded supervision ladder exhausts, and the plan ends
//     failed(supervision_unavailable) — unfixable by the one mechanism that
//     was supposed to fix it.
//
// So the rule is a DIFF: lint the set the correction would produce, lint the
// plan as it stands today, and reject only the violations the correction
// ADDS. A pre-existing violation is still surfaced (logCarriedViolations) —
// it is never silently forgiven — but it does not block a correction that did
// not cause it.
//
// WHY THIS CANNOT BE USED TO LAUNDER A VIOLATION. A violation is suppressed
// only when the baseline contains one with an IDENTICAL fingerprint: same
// kind, same member id set, same overlapping paths (violationFingerprint).
// That leaves no room to smuggle one in:
//
//  1. Every member a correction adds is a NEW id (tail member ids are minted
//     fresh and validateCorrectionTailMembers rejects one that collides with
//     an existing member), so any violation naming a tail member has a
//     fingerprint the baseline cannot contain. The live defect this file was
//     written for — the appended join-less `epsilon` — is exactly this case
//     and is still rejected.
//  2. A violation naming only PRE-EXISTING members cannot be newly created by
//     a correction's additions. Corrections only ever ADD BlockedBy edges, and
//     adding edges only ever makes more pairs ORDERED; `isOrdered` is
//     monotonic under edge addition, so an existing pair can lose its
//     parallelism (violation disappears) but never gain it. Existing members'
//     write_sets, IsJoin and Criteria are copied verbatim by the projection
//     and are not editable by any correction verb.
//  3. The one correction effect that CAN remove ordering — dropping the
//     superseded member, which orphans the edges through it — is deliberately
//     EXCLUDED from the baseline (the baseline keeps that member). So if
//     superseding X makes two survivors newly parallel and newly overlapping,
//     that violation is absent from the baseline, counts as introduced, and
//     is rejected.
//  4. The empty-plan arity violation is never suppressed. It is a property of
//     the RESULT rather than a pairwise invariant, and "the plan was already
//     empty" is not a reason to accept a correction that leaves it empty.
func LintCorrection(p *Plan, members []task.Task, req CorrectionRequest) *LintError {
	// Same nil-safety contract as Lint: p labels messages/events only.
	if p == nil {
		return nil
	}

	projected := projectCorrectedMembers(members, req)
	if len(projected) == 0 {
		v := emptyPlanViolation(p.ID)
		logCorrectionEvent(v.toCorrectionEvent(p.ID))
		return &LintError{PlanID: p.ID, Violations: []LintViolation{v}}
	}

	after := lintViolations(projected)
	if len(after) == 0 {
		return nil
	}

	// The baseline is the plan AS IT STANDS: the same "what can still run"
	// adjustment the projection applies to done members (so a violation means
	// the same thing on both sides), but nothing this correction does — no
	// tail members, no tail edges, and the superseded member still present.
	carried := violationFingerprints(lintViolations(projectCorrectedMembers(members, CorrectionRequest{})))

	introduced := make([]LintViolation, 0, len(after))
	preExisting := make([]LintViolation, 0, len(after))
	for _, v := range after {
		if carried[violationFingerprint(v)] {
			preExisting = append(preExisting, v)
			continue
		}
		introduced = append(introduced, v)
	}

	if len(preExisting) > 0 {
		logCarriedViolations(p.ID, preExisting)
	}
	if len(introduced) == 0 {
		return nil
	}
	for _, v := range introduced {
		logCorrectionEvent(v.toCorrectionEvent(p.ID))
	}
	return &LintError{PlanID: p.ID, Violations: introduced}
}

// violationFingerprint is the identity under which LintCorrection decides
// whether a violation is the SAME one the plan already carried. Kind, member
// id set and overlapping paths — sorted, so neither member order within a
// violation nor member order within the projected slice can change it.
//
// Reason is deliberately NOT part of the fingerprint. A join-less violation's
// Reason enumerates the converging predecessors, so including it would treat
// "already-broken member gains one more predecessor" as a brand-new violation
// and reject the correction. That buys no safety — the member is ALREADY an
// unguarded convergence point and no correction verb can repair it — while
// costing exactly the fixability this diff exists to restore.
func violationFingerprint(v LintViolation) string {
	ids := append([]string(nil), v.MemberIDs...)
	sort.Strings(ids)
	paths := append([]string(nil), v.Paths...)
	sort.Strings(paths)
	return string(v.Kind) + "\x00" + strings.Join(ids, ",") + "\x00" + strings.Join(paths, ",")
}

// violationFingerprints indexes a violation list by fingerprint.
func violationFingerprints(violations []LintViolation) map[string]bool {
	out := make(map[string]bool, len(violations))
	for _, v := range violations {
		out[violationFingerprint(v)] = true
	}
	return out
}

// logCarriedViolations surfaces the violations a correction was NOT blamed for
// — the ones the plan was already carrying. FR-157's "surfaced, never silent"
// still applies to them: they are real problems with the plan, and the only
// thing the diff changes is who is held responsible for them. They are
// deliberately NOT raised as CorrectionEvents: a CorrectionEvent is the
// prompt that a correction is NEEDED, and re-raising one on every subsequent
// correction would make the same unrepairable finding look like a fresh
// event each time.
func logCarriedViolations(planID string, violations []LintViolation) {
	ids := make([]string, 0, len(violations))
	kinds := make([]string, 0, len(violations))
	for _, v := range violations {
		ids = append(ids, v.MemberIDs...)
		kinds = append(kinds, string(v.Kind))
	}
	slog.Warn("plan: correction lint found pre-existing violations it did not attribute to this correction",
		"plan_id", planID, "kinds", kinds, "member_ids", ids, "count", len(violations))
}

// projectCorrectedMembers builds LintCorrection's projected member set. It is
// a pure function: neither members nor req is mutated, and every BlockedBy
// slice it edits is cloned first (the inputs are the caller's live store
// snapshot and request payload).
func projectCorrectedMembers(members []task.Task, req CorrectionRequest) []task.Task {
	projected := make([]task.Task, 0, len(members)+len(req.TailMembers))
	for i := range members {
		m := members[i]
		if req.SupersededMemberID != "" && m.ID == req.SupersededMemberID {
			continue // adjustment 1 — see LintCorrection's doc comment.
		}
		if m.Status == task.StatusDone {
			m.WriteSet = nil // adjustment 2 — see LintCorrection's doc comment.
		}
		m.BlockedBy = append([]string(nil), m.BlockedBy...)
		projected = append(projected, m)
	}
	for i := range req.TailMembers {
		m := req.TailMembers[i]
		m.BlockedBy = append([]string(nil), m.BlockedBy...)
		projected = append(projected, m)
	}

	idx := make(map[string]int, len(projected))
	for i := range projected {
		idx[projected[i].ID] = i
	}
	// An IntentEdge {From, To} commits as AddDependency(To, From) — i.e. To
	// becomes blocked by From (buildCorrectionApplyFunc). Mirror that exactly,
	// so the lint sees the DAG the commit will actually build.
	for _, e := range req.TailEdges {
		i, ok := idx[e.ToTaskID]
		if !ok {
			continue // unknown/ dropped endpoint — validateCorrectionTailEdges rejects these first.
		}
		if _, known := idx[e.FromTaskID]; !known {
			continue
		}
		already := false
		for _, b := range projected[i].BlockedBy {
			if b == e.FromTaskID {
				already = true
				break
			}
		}
		if !already {
			projected[i].BlockedBy = append(projected[i].BlockedBy, e.FromTaskID)
		}
	}
	return projected
}

// lintOverlaps implements FR-156/G-16 AS-1: reject every PARALLEL pair of
// members whose non-empty write_sets overlap.
func lintOverlaps(members []task.Task, ancestors map[string]map[string]bool) []LintViolation {
	var violations []LintViolation
	for i := 0; i < len(members); i++ {
		if len(members[i].WriteSet) == 0 {
			continue // exploratory member (D10) — exempt from the overlap check.
		}
		for j := i + 1; j < len(members); j++ {
			if len(members[j].WriteSet) == 0 {
				continue
			}
			if isOrdered(members[i].ID, members[j].ID, ancestors) {
				continue // serial (one is a transitive dependency of the other) — never runs concurrently.
			}
			overlap := overlappingPaths(members[i].WriteSet, members[j].WriteSet)
			if len(overlap) == 0 {
				continue
			}
			violations = append(violations, LintViolation{
				Kind:      LintOverlap,
				MemberIDs: []string{members[i].ID, members[j].ID},
				Paths:     overlap,
				Reason: fmt.Sprintf(
					"member %s (%q) and member %s (%q) run in parallel (no blocked_by ordering between "+
						"them) but declare overlapping write_set paths: %s",
					members[i].ID, members[i].Title, members[j].ID, members[j].Title,
					strings.Join(overlap, ", ")),
			})
		}
	}
	return violations
}

// lintJoinlessConvergence implements FR-156/FR-159/G-16 AS-2: a member
// depending on >=2 mutually-parallel predecessors MUST be an authored join
// member (IsJoin==true) with >=1 acceptance criterion.
//
// ADR-086 D5/GOAL-FR-029/FR-030 names this function as a Task.Criteria
// consumer to re-point onto the task's paired goal record (pkg/goal). No
// change was needed here: m.Criteria (task.Task.Criteria) stays a real,
// disk-persisted, dual-written field this round rather than being removed
// (see Task.Criteria's own doc comment, pkg/task/task.go, for the two
// independent reasons — one of them a Go import-cycle impossibility, not a
// design choice), so this pre-existing plain-field read continues to see
// the correct, current criteria set with no repointing required.
func lintJoinlessConvergence(members []task.Task, idx map[string]*task.Task, ancestors map[string]map[string]bool) []LintViolation {
	var violations []LintViolation
	for i := range members {
		m := &members[i]
		if len(m.BlockedBy) < 2 {
			continue
		}
		parallelDeps := parallelDependencies(m.BlockedBy, idx, ancestors)
		if len(parallelDeps) < 2 {
			continue // does not converge >=2 parallel streams.
		}
		switch {
		case !m.IsJoin:
			violations = append(violations, LintViolation{
				Kind:      LintJoinless,
				MemberIDs: []string{m.ID},
				Reason: fmt.Sprintf(
					"member %s (%q) converges %d parallel predecessors (%s) but is not marked is_join; "+
						"a convergence point must be a first-class authored join/assemble member "+
						"(is_join=true) with its own acceptance criteria",
					m.ID, m.Title, len(parallelDeps), strings.Join(parallelDeps, ", ")),
			})
		case len(m.Criteria) == 0:
			violations = append(violations, LintViolation{
				Kind:      LintJoinless,
				MemberIDs: []string{m.ID},
				Reason: fmt.Sprintf(
					"member %s (%q) is marked is_join=true but has zero acceptance criteria; an "+
						"authored join/assemble member must carry its own Definition of Done (FR-159)",
					m.ID, m.Title),
			})
		}
	}
	return violations
}

// parallelDependencies returns the sorted, deduplicated subset of blockedBy
// (restricted to IDs present in idx — an edge to a task outside this plan or
// since deleted is an orphan edge, ignored here exactly as
// task.Task.BlockedBy's own doc convention drops it elsewhere) that
// participates in at least one mutually-unordered (parallel) pair with
// another entry in blockedBy.
func parallelDependencies(blockedBy []string, idx map[string]*task.Task, ancestors map[string]map[string]bool) []string {
	deps := make([]string, 0, len(blockedBy))
	for _, d := range blockedBy {
		if _, ok := idx[d]; ok {
			deps = append(deps, d)
		}
	}
	found := map[string]bool{}
	for x := 0; x < len(deps); x++ {
		for y := x + 1; y < len(deps); y++ {
			if isOrdered(deps[x], deps[y], ancestors) {
				continue
			}
			found[deps[x]] = true
			found[deps[y]] = true
		}
	}
	out := make([]string, 0, len(found))
	for id := range found {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// --- DAG topology helpers ---------------------------------------------------

// membersByID indexes a plan's member tasks for O(1) lookup by ID.
func membersByID(members []task.Task) map[string]*task.Task {
	idx := make(map[string]*task.Task, len(members))
	for i := range members {
		idx[members[i].ID] = &members[i]
	}
	return idx
}

// ancestorSets computes, for every member, the full transitive set of
// member IDs it depends on via BlockedBy, restricted to the given plan's own
// members. Plan sizes are small (approve-time only, not a hot path) so the
// straightforward per-member DFS is deliberately not memoized further.
func ancestorSets(members []task.Task, idx map[string]*task.Task) map[string]map[string]bool {
	sets := make(map[string]map[string]bool, len(members))
	for i := range members {
		sets[members[i].ID] = collectAncestors(members[i].ID, idx, map[string]bool{})
	}
	return sets
}

// collectAncestors walks BlockedBy edges from id, collecting every
// transitively-reachable member ID within idx. An edge to an ID absent from
// idx (outside this plan, or since deleted) is an orphan edge and is
// silently dropped, mirroring task.Task.BlockedBy's own documented
// orphan-edge-drop convention. visiting guards against an unexpected cycle
// (task.Store's write-time cycle validator already rejects one at
// create/update time, so this should never fire in practice — it defends
// against topology confusion rather than an infinite loop).
func collectAncestors(id string, idx map[string]*task.Task, visiting map[string]bool) map[string]bool {
	result := map[string]bool{}
	t, ok := idx[id]
	if !ok || visiting[id] {
		return result
	}
	visiting[id] = true
	defer delete(visiting, id)
	for _, dep := range t.BlockedBy {
		if _, ok := idx[dep]; !ok {
			continue
		}
		if result[dep] {
			continue
		}
		result[dep] = true
		for anc := range collectAncestors(dep, idx, visiting) {
			result[anc] = true
		}
	}
	return result
}

// isOrdered reports whether a and b are ordered by the BlockedBy DAG in
// either direction (one is a transitive dependency of the other) — i.e.
// they are NOT parallel and can never actually run concurrently.
func isOrdered(a, b string, ancestors map[string]map[string]bool) bool {
	return ancestors[a][b] || ancestors[b][a]
}

// --- write-set path overlap -------------------------------------------------

// overlappingPaths returns the sorted, deduplicated subset of a's and b's
// RAW (un-normalized) declared path strings that overlap per pathsOverlap.
// Empty when a and b are disjoint.
func overlappingPaths(a, b []string) []string {
	seen := map[string]bool{}
	var hits []string
	for _, pa := range a {
		na := normalizeWriteSetPath(pa)
		if na == "" {
			continue
		}
		for _, pb := range b {
			nb := normalizeWriteSetPath(pb)
			if nb == "" {
				continue
			}
			if !pathsOverlap(na, nb) {
				continue
			}
			if !seen[pa] {
				seen[pa] = true
				hits = append(hits, pa)
			}
			if !seen[pb] {
				seen[pb] = true
				hits = append(hits, pb)
			}
		}
	}
	sort.Strings(hits)
	return hits
}

// normalizeWriteSetPath cleans a declared write_set path for comparison:
// trims whitespace and a trailing slash, then path.Clean (POSIX-style
// slash paths — write_set entries are repo-relative, not OS paths). An
// empty/whitespace-only entry normalizes to "" and is ignored by the
// overlap check (never treated as matching everything).
func normalizeWriteSetPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return ""
	}
	return path.Clean(p)
}

// pathsOverlap reports whether two ALREADY-NORMALIZED paths could touch the
// same file: identical paths always overlap; a directory-style declaration
// overlaps anything nested beneath it (e.g. "src/a" overlaps "src/a/b.go").
// Comparison is on cleaned, slash-delimited PATH SEGMENTS — disjoint
// siblings that merely share a string prefix (e.g. "src/a" vs "src/ab") do
// NOT overlap.
func pathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

// --- plan-correction event (FR-157) -----------------------------------------

// CorrectionEventKind discriminates why a CorrectionEvent was raised.
type CorrectionEventKind string

const (
	// CorrectionKindWriteSetOverlap is raised by Lint when two parallel
	// members declare overlapping write_set paths (FR-156/G-16, static —
	// detected at approve).
	CorrectionKindWriteSetOverlap CorrectionEventKind = "write_set_overlap"
	// CorrectionKindJoinlessConvergence is raised by Lint when a
	// convergence point has no authored join member (FR-156/159/G-16,
	// static — detected at approve).
	CorrectionKindJoinlessConvergence CorrectionEventKind = "join_less_convergence"
	// CorrectionKindMergeConflict is raised at RUNTIME — not by Lint, which
	// has no visibility into live git state — when an exploratory member's
	// isolated checkout and another stream's work collide on the SAME file
	// at merge time (D10, FR-157, write-set-disjointness dataset row 3).
	// This is the Phase-2 owner-loop's/git-boundary-commit machinery's
	// (pkg/agent, pkg/gitevidence — out of this package's scope) emission
	// point: see NewMergeConflictEvent.
	CorrectionKindMergeConflict CorrectionEventKind = "merge_conflict"
	// CorrectionKindEmptyPlan is raised by Lint when a plan carries no member
	// tasks at all (static — detected at approve, and at every correction
	// that adds work). Its own kind rather than a third meaning of
	// write_set_overlap: toCorrectionEvent's pre-existing default branch
	// would otherwise have labelled an empty plan a write-set overlap, which
	// is both untrue and unactionable.
	CorrectionKindEmptyPlan CorrectionEventKind = "empty_plan"
)

// CorrectionEvent is the typed signal a write-set problem raises so it is
// surfaced, never silently resolved (FR-157) — the two STATIC kinds above
// are raised synchronously by Lint (as part of the approve-time rejection
// this package already returns); the RUNTIME kind
// (CorrectionKindMergeConflict) has no consumer built yet — that is Phase 2,
// the owner loop (pkg/agent), tracked in the spec's US-9/User Story 9.
//
// not-wire-format: this is an INTERNAL signal, not a generated wire type.
// There is no asyncapi/openapi schema for it today because nothing
// persists or transports it over the wire yet — every current call site
// (Lint) only logs it (see logCorrectionEvent) alongside returning the
// synchronous *LintError rejection. When the Phase-2 owner loop is built it
// should either (a) grow a dedicated wire schema (a SessionMessage variant
// or WS frame) shaped after this struct, or (b) map a CorrectionEvent onto
// a RevisionEntry (contracts/components/schemas/RevisionEntry.yaml) —
// CorrectionEvent.Reason maps to RevisionEntry.reason /
// falsified_assumption, CorrectionEvent.PlanID to RevisionEntry.plan_id.
// The two are deliberately NOT unified here: RevisionEntry is an
// OWNER-AUTHORED correction record (verb append/supersede/targeted_retry)
// committed transactionally via the write-ahead intent-log (INV-6); a
// CorrectionEvent is the narrower, system-DETECTED prompt that a correction
// is needed — it does not itself decide or record what the owner does about
// it.
type CorrectionEvent struct {
	Kind CorrectionEventKind `json:"kind"`
	// PlanID is the plan this event concerns.
	PlanID string `json:"plan_id"`
	// MemberIDs are the offending member task ID(s). For
	// write_set_overlap: the two colliding members. For
	// join_less_convergence: the convergence member itself. For
	// merge_conflict: the member(s) whose checkouts collided.
	MemberIDs []string `json:"member_ids"`
	// Paths are the specific overlapping/conflicting path(s), when known.
	Paths []string `json:"paths,omitempty"`
	// Reason is a human-readable, actionable description.
	Reason string `json:"reason"`
	// DetectedAt is when the event was raised (RFC 3339 UTC).
	DetectedAt string `json:"detected_at"`
}

// NewMergeConflictEvent is the EMISSION POINT the Phase-2 owner loop's
// merge/boundary-commit machinery should call when a genuine same-file
// conflict is discovered at merge time for an exploratory member (D10,
// FR-157, write-set-disjointness dataset row 3: "streams converge, no
// declaration ... conflict->correction"). Lint itself never calls this — it
// has no visibility into runtime git state — but the payload shape and the
// "log it, never drop it" discipline live here so both the static (Lint)
// and the future runtime caller emit the SAME typed signal through the SAME
// choke point (logCorrectionEvent).
func NewMergeConflictEvent(planID string, memberIDs, paths []string, reason string) CorrectionEvent {
	ev := CorrectionEvent{
		Kind:       CorrectionKindMergeConflict,
		PlanID:     planID,
		MemberIDs:  memberIDs,
		Paths:      paths,
		Reason:     reason,
		DetectedAt: time.Now().UTC().Format(time.RFC3339),
	}
	logCorrectionEvent(ev)
	return ev
}

// toCorrectionEvent converts a Lint-detected violation to its
// CorrectionEvent form.
func (v LintViolation) toCorrectionEvent(planID string) CorrectionEvent {
	// An explicit switch, not an if-ladder over a default: a violation kind
	// added later must not silently inherit "write_set_overlap" the way
	// LintEmptyPlan would have.
	kind := CorrectionKindWriteSetOverlap
	switch v.Kind {
	case LintJoinless:
		kind = CorrectionKindJoinlessConvergence
	case LintEmptyPlan:
		kind = CorrectionKindEmptyPlan
	case LintOverlap:
		kind = CorrectionKindWriteSetOverlap
	}
	return CorrectionEvent{
		Kind:       kind,
		PlanID:     planID,
		MemberIDs:  v.MemberIDs,
		Paths:      v.Paths,
		Reason:     v.Reason,
		DetectedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// logCorrectionEvent is the SINGLE choke point every CorrectionEvent flows
// through today (FR-157 "surfaced, never silent") — until the Phase-2 owner
// loop lands a real sink (durable persistence + UI surfacing), a structured
// slog.Warn is the interim guarantee that a correction-worthy event is never
// dropped soundlessly.
func logCorrectionEvent(ev CorrectionEvent) {
	slog.Warn("plan: correction event raised",
		"kind", ev.Kind, "plan_id", ev.PlanID, "member_ids", ev.MemberIDs,
		"paths", ev.Paths, "reason", ev.Reason, "detected_at", ev.DetectedAt)
}

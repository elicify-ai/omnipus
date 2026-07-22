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
	if p == nil || len(members) == 0 {
		return nil
	}

	idx := membersByID(members)
	ancestors := ancestorSets(members, idx)

	violations := lintOverlaps(members, ancestors)
	violations = append(violations, lintJoinlessConvergence(members, idx, ancestors)...)

	if len(violations) == 0 {
		return nil
	}

	for _, v := range violations {
		logCorrectionEvent(v.toCorrectionEvent(p.ID))
	}
	return &LintError{PlanID: p.ID, Violations: violations}
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
	kind := CorrectionKindWriteSetOverlap
	if v.Kind == LintJoinless {
		kind = CorrectionKindJoinlessConvergence
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

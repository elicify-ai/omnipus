// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import (
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// mustCriteria returns a single non-empty acceptance-criteria slice — every
// plan-lint test member that should pass the (separate, pre-existing) FR-084
// gate needs one so a test failure is unambiguously attributable to Lint,
// never to an incidental empty-criteria gap.
func mustCriteria() []task.AcceptanceCriterion {
	return []task.AcceptanceCriterion{{
		ID:     "c1",
		Kind:   task.KindProse,
		Text:   "done",
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "agent1"},
		Status: task.CritPending,
	}}
}

func member(id string, blockedBy []string, writeSet []string) task.Task {
	return task.Task{
		ID:        id,
		Title:     "member " + id,
		BlockedBy: blockedBy,
		WriteSet:  writeSet,
		Criteria:  mustCriteria(),
	}
}

// --- Dataset: Write-set disjointness (spec table, rows 1-5) -----------------

// TestPlanLint_DisjointWriteSetsPass covers dataset row 1: two parallel
// members with disjoint write_sets approve cleanly.
func TestPlanLint_DisjointWriteSetsPass(t *testing.T) {
	p := &Plan{ID: "plan1"}
	members := []task.Task{
		member("a", nil, []string{"api/routes.go"}),
		member("b", nil, []string{"web/app.tsx"}),
	}
	if lerr := Lint(p, members); lerr != nil {
		t.Fatalf("expected no lint violation for disjoint write-sets, got: %v", lerr)
	}
}

// TestPlanLint_RejectsOverlappingWriteSets covers dataset row 2 (G-16 AS-1).
func TestPlanLint_RejectsOverlappingWriteSets(t *testing.T) {
	p := &Plan{ID: "plan1"}
	members := []task.Task{
		member("a", nil, []string{"api/routes.go"}),
		member("b", nil, []string{"api/routes.go"}),
	}
	lerr := Lint(p, members)
	if lerr == nil {
		t.Fatal("expected a lint violation for overlapping write-sets, got nil")
	}
	if !errors.Is(lerr, ErrValidation) {
		t.Fatalf("expected LintError to wrap ErrValidation, got: %v", lerr)
	}
	if len(lerr.Violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %d: %+v", len(lerr.Violations), lerr.Violations)
	}
	v := lerr.Violations[0]
	if v.Kind != LintOverlap {
		t.Fatalf("expected LintOverlap, got %q", v.Kind)
	}
	if len(v.MemberIDs) != 2 || v.MemberIDs[0] != "a" || v.MemberIDs[1] != "b" {
		t.Fatalf("expected both offending member IDs named, got %v", v.MemberIDs)
	}
	if len(v.Paths) == 0 {
		t.Fatal("expected the overlapping path to be named")
	}
	if v.Reason == "" {
		t.Fatal("expected a human-readable reason naming the conflict")
	}
}

// TestPlanLint_PathPrefixAware asserts the overlap check is path-PREFIX
// aware (a directory-style declaration overlaps anything nested under it)
// and that disjoint SIBLING paths sharing only a string prefix are correctly
// NOT flagged (task acceptance: "src/a/ overlaps src/a/b.go; disjoint
// sibling paths pass").
func TestPlanLint_PathPrefixAware(t *testing.T) {
	t.Run("directory_prefix_overlaps_nested_file", func(t *testing.T) {
		p := &Plan{ID: "plan1"}
		members := []task.Task{
			member("a", nil, []string{"src/a/"}),
			member("b", nil, []string{"src/a/b.go"}),
		}
		lerr := Lint(p, members)
		if lerr == nil {
			t.Fatal("expected src/a/ to overlap src/a/b.go")
		}
		if lerr.Violations[0].Kind != LintOverlap {
			t.Fatalf("expected LintOverlap, got %q", lerr.Violations[0].Kind)
		}
	})

	t.Run("string_prefix_sibling_is_disjoint", func(t *testing.T) {
		p := &Plan{ID: "plan1"}
		members := []task.Task{
			member("a", nil, []string{"src/a"}),
			member("b", nil, []string{"src/ab"}),
		}
		if lerr := Lint(p, members); lerr != nil {
			t.Fatalf("src/a and src/ab are disjoint siblings, expected no violation, got: %v", lerr)
		}
	})

	t.Run("identical_path_overlaps", func(t *testing.T) {
		p := &Plan{ID: "plan1"}
		members := []task.Task{
			member("a", nil, []string{"pkg/plan/plan_lint.go"}),
			member("b", nil, []string{"pkg/plan/plan_lint.go"}),
		}
		if lerr := Lint(p, members); lerr == nil {
			t.Fatal("expected identical declared paths to overlap")
		}
	})
}

// TestPlanLint_SerialMembersNeverFlagged asserts a blocked_by ORDERING
// between two members exempts them from the overlap check entirely, even
// when they declare the exact same path — they never run concurrently.
func TestPlanLint_SerialMembersNeverFlagged(t *testing.T) {
	p := &Plan{ID: "plan1"}
	members := []task.Task{
		member("a", nil, []string{"shared/file.go"}),
		member("b", []string{"a"}, []string{"shared/file.go"}), // b depends on a: serial, not parallel.
	}
	if lerr := Lint(p, members); lerr != nil {
		t.Fatalf("serial (blocked_by-ordered) members sharing a path must not be flagged, got: %v", lerr)
	}
}

// TestPlanLint_ExploratoryMembersExempt covers dataset row 3: two
// exploratory members (empty write_set) never trigger an overlap violation
// — they declare no write footprint precisely because it is unknowable up
// front (D10); a genuine conflict is a RUNTIME merge-time concern (see
// TestNewMergeConflictEvent), not a static lint rejection.
func TestPlanLint_ExploratoryMembersExempt(t *testing.T) {
	p := &Plan{ID: "plan1"}
	members := []task.Task{
		member("a", nil, nil),
		member("b", nil, nil),
	}
	if lerr := Lint(p, members); lerr != nil {
		t.Fatalf("exploratory members (no write_set) must be exempt from the overlap check, got: %v", lerr)
	}
}

// --- Join-point enforcement (G-16 AS-2, FR-159) -----------------------------

// TestPlanLint_RejectsJoinlessConvergence covers dataset row 5: a member
// converging >=2 parallel predecessors with no is_join marker is rejected.
func TestPlanLint_RejectsJoinlessConvergence(t *testing.T) {
	p := &Plan{ID: "plan1"}
	members := []task.Task{
		member("a", nil, []string{"shards/a.csv"}),
		member("b", nil, []string{"shards/b.csv"}),
		member("merge", []string{"a", "b"}, nil), // converges a+b, IsJoin left false.
	}
	lerr := Lint(p, members)
	if lerr == nil {
		t.Fatal("expected a join-less-convergence violation")
	}
	found := false
	for _, v := range lerr.Violations {
		if v.Kind == LintJoinless && len(v.MemberIDs) == 1 && v.MemberIDs[0] == "merge" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a LintJoinless violation naming member %q, got: %+v", "merge", lerr.Violations)
	}
}

// TestPlanLint_JoinMemberMissingCriteriaRejected asserts that IsJoin=true
// alone is not sufficient — FR-159 requires the join member to carry its
// OWN acceptance criteria.
func TestPlanLint_JoinMemberMissingCriteriaRejected(t *testing.T) {
	p := &Plan{ID: "plan1"}
	mergeNoCriteria := member("merge", []string{"a", "b"}, nil)
	mergeNoCriteria.IsJoin = true
	mergeNoCriteria.Criteria = nil // explicitly empty, overriding the member() helper's default.
	members := []task.Task{
		member("a", nil, []string{"shards/a.csv"}),
		member("b", nil, []string{"shards/b.csv"}),
		mergeNoCriteria,
	}
	lerr := Lint(p, members)
	if lerr == nil {
		t.Fatal("expected a violation for an is_join member with zero criteria")
	}
	if lerr.Violations[0].Kind != LintJoinless {
		t.Fatalf("expected LintJoinless, got %q", lerr.Violations[0].Kind)
	}
}

// TestPlanLint_AuthoredJoinMemberPasses is the positive counterpart: a
// convergence point marked is_join=true WITH its own criteria is accepted.
func TestPlanLint_AuthoredJoinMemberPasses(t *testing.T) {
	p := &Plan{ID: "plan1"}
	merge := member("merge", []string{"a", "b"}, nil)
	merge.IsJoin = true
	members := []task.Task{
		member("a", nil, []string{"shards/a.csv"}),
		member("b", nil, []string{"shards/b.csv"}),
		merge,
	}
	if lerr := Lint(p, members); lerr != nil {
		t.Fatalf("expected an authored join member to pass, got: %v", lerr)
	}
}

// TestPlanLint_SerialDependencyChainNeverAJoin asserts that a member
// depending on a SERIAL chain (b depends on a, then c depends on a AND b) is
// NOT treated as converging parallel streams — a and b are ordered (a is
// b's ancestor), so c's dependency set has no mutually-parallel pair.
func TestPlanLint_SerialDependencyChainNeverAJoin(t *testing.T) {
	p := &Plan{ID: "plan1"}
	members := []task.Task{
		member("a", nil, []string{"x.go"}),
		member("b", []string{"a"}, []string{"y.go"}),
		member("c", []string{"a", "b"}, []string{"z.go"}), // a is an ancestor of b: not a parallel convergence.
	}
	if lerr := Lint(p, members); lerr != nil {
		t.Fatalf("a serial dependency chain must not be treated as a join, got: %v", lerr)
	}
}

// --- g5 worked topologies (Acceptance Scenario 4 / conformance g5) ---------

// TestPlanLint_G5_SoftwarePlan_ContractThenTwoStreamsThenMerge covers the g5
// "software plan" topology: a serial contract-first member -> two
// lint-disjoint streams -> a merge member leaving one green tree.
func TestPlanLint_G5_SoftwarePlan_ContractThenTwoStreamsThenMerge(t *testing.T) {
	p := &Plan{ID: "plan-software"}
	merge := member("merge", []string{"stream-a", "stream-b"}, nil)
	merge.IsJoin = true
	members := []task.Task{
		member("contract", nil, []string{"contracts/openapi.yaml"}),
		member("stream-a", []string{"contract"}, []string{"pkg/plan/lint.go", "pkg/plan/lint_test.go"}),
		member("stream-b", []string{"contract"}, []string{"src/lib/api.ts"}),
		merge,
	}
	if lerr := Lint(p, members); lerr != nil {
		t.Fatalf("g5 software-plan topology should validate cleanly, got: %v", lerr)
	}
}

// TestPlanLint_G5_ReportWorkbook_ShardSchemaThenThreeShardsThenAssemble
// covers the g5 "report-with-workbook" topology: a serial shard-schema
// member -> three disjoint-shard streams -> ONE assemble member building
// the .xlsx (dataset row 4).
func TestPlanLint_G5_ReportWorkbook_ShardSchemaThenThreeShardsThenAssemble(t *testing.T) {
	p := &Plan{ID: "plan-report"}
	assemble := member("assemble", []string{"shard-a", "shard-b", "shard-c"}, []string{"out/report.xlsx"})
	assemble.IsJoin = true
	members := []task.Task{
		member("schema", nil, []string{"schema/shard.json"}),
		member("shard-a", []string{"schema"}, []string{"shards/a.csv"}),
		member("shard-b", []string{"schema"}, []string{"shards/b.csv"}),
		member("shard-c", []string{"schema"}, []string{"shards/c.csv"}),
		assemble,
	}
	if lerr := Lint(p, members); lerr != nil {
		t.Fatalf("g5 report-with-workbook topology should validate cleanly, got: %v", lerr)
	}
	// The assemble member's OWN write (out/report.xlsx) must not collide with
	// anything either — it is parallel to nothing (it is ordered after every
	// shard) so this is a control assertion that Lint did not spuriously flag it.
	for _, m := range members {
		if m.ID == "assemble" && !m.IsJoin {
			t.Fatal("assemble member must be marked is_join")
		}
	}
}

// TestPlanLint_G5_ThreeShardsOverlapping_Rejected is the negative
// counterpart of the report-workbook topology: if two of the three shard
// streams accidentally declare the SAME shard path, lint must still catch
// it even inside an otherwise-valid shard+assemble shape.
func TestPlanLint_G5_ThreeShardsOverlapping_Rejected(t *testing.T) {
	p := &Plan{ID: "plan-report"}
	assemble := member("assemble", []string{"shard-a", "shard-b", "shard-c"}, []string{"out/report.xlsx"})
	assemble.IsJoin = true
	members := []task.Task{
		member("schema", nil, []string{"schema/shard.json"}),
		member("shard-a", []string{"schema"}, []string{"shards/a.csv"}),
		member("shard-b", []string{"schema"}, []string{"shards/a.csv"}), // collides with shard-a.
		member("shard-c", []string{"schema"}, []string{"shards/c.csv"}),
		assemble,
	}
	lerr := Lint(p, members)
	if lerr == nil {
		t.Fatal("expected the accidental shard-a/shard-b collision to be rejected")
	}
	found := false
	for _, v := range lerr.Violations {
		if v.Kind == LintOverlap {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a LintOverlap violation, got: %+v", lerr.Violations)
	}
}

// --- Nil / empty inputs ------------------------------------------------------

func TestPlanLint_NilOrEmptyInputsPass(t *testing.T) {
	if lerr := Lint(nil, []task.Task{member("a", nil, []string{"x"})}); lerr != nil {
		t.Fatalf("a nil plan must not panic and must return no violation, got: %v", lerr)
	}
	if lerr := Lint(&Plan{ID: "p"}, nil); lerr != nil {
		t.Fatalf("an empty member list must return no violation, got: %v", lerr)
	}
}

// --- Plan-correction event (FR-157) ----------------------------------------

// TestPlanLint_ViolationsCarryCorrectionEventKind asserts the Lint-detected
// violations map onto the correct CorrectionEvent kind (write_set_overlap
// vs. join_less_convergence) — the same mapping toCorrectionEvent applies
// internally when logging every violation (FR-157 "surfaced, never
// silent").
func TestPlanLint_ViolationsCarryCorrectionEventKind(t *testing.T) {
	p := &Plan{ID: "plan1"}
	members := []task.Task{
		member("a", nil, []string{"api/routes.go"}),
		member("b", nil, []string{"api/routes.go"}),
	}
	lerr := Lint(p, members)
	if lerr == nil {
		t.Fatal("expected overlap violation")
	}
	ev := lerr.Violations[0].toCorrectionEvent(p.ID)
	if ev.Kind != CorrectionKindWriteSetOverlap {
		t.Fatalf("expected CorrectionKindWriteSetOverlap, got %q", ev.Kind)
	}
	if ev.PlanID != "plan1" {
		t.Fatalf("expected plan_id to be threaded through, got %q", ev.PlanID)
	}
	if len(ev.MemberIDs) != 2 {
		t.Fatalf("expected 2 member IDs, got %v", ev.MemberIDs)
	}
	if ev.Reason == "" {
		t.Fatal("expected a non-empty reason")
	}
	if _, err := time.Parse(time.RFC3339, ev.DetectedAt); err != nil {
		t.Fatalf("expected DetectedAt to be RFC3339, got %q: %v", ev.DetectedAt, err)
	}
}

// TestNewMergeConflictEvent is the EMISSION-POINT contract test for the
// Phase-2 owner loop / git-boundary-commit machinery (FR-157, D10, dataset
// row 3): a runtime merge-time conflict for an exploratory member must
// build a well-formed CorrectionEvent of kind merge_conflict and never
// panic even with no store/session wiring available — this package's ONLY
// job here is to define the payload shape + emission point; consuming it is
// out of scope (pkg/agent, Phase 2).
func TestNewMergeConflictEvent(t *testing.T) {
	ev := NewMergeConflictEvent("plan-x", []string{"exploratory-1", "stream-b-3"},
		[]string{"shared/config.go"}, "exploratory-1's checkout and stream-b-3 both wrote shared/config.go")

	if ev.Kind != CorrectionKindMergeConflict {
		t.Fatalf("expected CorrectionKindMergeConflict, got %q", ev.Kind)
	}
	if ev.PlanID != "plan-x" {
		t.Fatalf("expected plan_id %q, got %q", "plan-x", ev.PlanID)
	}
	if len(ev.MemberIDs) != 2 {
		t.Fatalf("expected 2 member IDs, got %v", ev.MemberIDs)
	}
	if len(ev.Paths) != 1 || ev.Paths[0] != "shared/config.go" {
		t.Fatalf("expected the conflicting path to be carried through, got %v", ev.Paths)
	}
	if ev.Reason == "" {
		t.Fatal("expected a non-empty reason — a conflict must never be silently resolved (FR-157)")
	}
	if _, err := time.Parse(time.RFC3339, ev.DetectedAt); err != nil {
		t.Fatalf("expected DetectedAt to be RFC3339, got %q: %v", ev.DetectedAt, err)
	}
}

// --- LintError shape ----------------------------------------------------

func TestLintError_ErrorMessageNamesEveryViolation(t *testing.T) {
	p := &Plan{ID: "plan1"}
	members := []task.Task{
		member("a", nil, []string{"api/routes.go"}),
		member("b", nil, []string{"api/routes.go"}),
		member("c", nil, []string{"api/routes.go"}), // also overlaps both a and b.
	}
	lerr := Lint(p, members)
	if lerr == nil {
		t.Fatal("expected violations")
	}
	if len(lerr.Violations) < 2 {
		t.Fatalf("expected at least 2 overlap violations (a-b, a-c, b-c all collide), got %d", len(lerr.Violations))
	}
	msg := lerr.Error()
	if msg == "" {
		t.Fatal("expected a non-empty error message")
	}
	if !errors.Is(lerr, ErrValidation) {
		t.Fatal("expected errors.Is(lerr, ErrValidation) to hold")
	}
}

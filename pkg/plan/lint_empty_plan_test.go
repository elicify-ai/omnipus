// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

// lint_empty_plan_test.go — regression coverage for UAT defect A: an EMPTY
// plan (0 member tasks) was approved and set `running`.
//
// Why this matters more than the empty plan itself: every check in lint.go is
// a predicate over PAIRS of members, so an empty member list satisfied all of
// them vacuously and Lint returned nil. Approve therefore accepted the plan, a
// PlanSupervisor correction then populated it with auto-generated members, and
// because corrections carried no lint of their own those members were never
// checked for write-set overlap or join-less convergence at all. Approving
// empty was a complete one-step bypass of plan-lint.
//
// The fix puts the arity precondition inside Lint — the ONE choke point both
// approve paths already share (pkg/tools/plan.go's execute_plan and
// pkg/gateway/rest_plans.go's handlePlanApprove) — so neither can be gated
// without the other.

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestPlanLint_EmptyMemberListIsRejected is the defect-A regression test.
//
// It fails against the pre-fix Lint, whose first statement was
// `if p == nil || len(members) == 0 { return nil }`.
func TestPlanLint_EmptyMemberListIsRejected(t *testing.T) {
	p := &Plan{ID: "plan-empty"}

	for _, tc := range []struct {
		name    string
		members []task.Task
	}{
		{"nil slice", nil},
		{"empty slice", []task.Task{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lerr := Lint(p, tc.members)
			if lerr == nil {
				t.Fatal("a plan with zero member tasks must be REJECTED by lint, got nil " +
					"(an empty plan approves and starts running — UAT defect A)")
			}
			if len(lerr.Violations) != 1 {
				t.Fatalf("expected exactly 1 violation, got %d: %+v", len(lerr.Violations), lerr.Violations)
			}
			if got := lerr.Violations[0].Kind; got != LintEmptyPlan {
				t.Fatalf("violation kind = %q, want %q", got, LintEmptyPlan)
			}
			if lerr.PlanID != p.ID {
				t.Fatalf("LintError.PlanID = %q, want %q", lerr.PlanID, p.ID)
			}
			// The rejection must name the plan and say what is required, so
			// the approving human or calling agent can act on it without a
			// second round-trip.
			if reason := lerr.Violations[0].Reason; !strings.Contains(reason, p.ID) ||
				!strings.Contains(reason, "at least one member task") {
				t.Fatalf("reason must name the plan and the requirement, got: %q", reason)
			}
			// Both approve call sites map a lint rejection onto HTTP 400 /
			// a tool-level validation error via errors.Is(err, ErrValidation).
			// Without this the empty-plan rejection would surface as a 500.
			if !errors.Is(lerr, ErrValidation) {
				t.Fatal("an empty-plan rejection must unwrap to ErrValidation so both approve paths " +
					"map it to a 400 rather than a 500")
			}
		})
	}
}

// TestPlanLint_EmptyPlanCorrectionEventKind pins that an empty-plan violation
// is reported as its OWN correction-event kind.
//
// Pre-fix, toCorrectionEvent was an if-ladder defaulting to
// CorrectionKindWriteSetOverlap for every kind that was not LintJoinless, so a
// newly added kind silently inherited a label that is both untrue and
// unactionable. The mapping is now an explicit switch.
func TestPlanLint_EmptyPlanCorrectionEventKind(t *testing.T) {
	ev := LintViolation{Kind: LintEmptyPlan, Reason: "r"}.toCorrectionEvent("plan-empty")
	if ev.Kind != CorrectionKindEmptyPlan {
		t.Fatalf("correction event kind = %q, want %q (an empty plan is not a write-set overlap)",
			ev.Kind, CorrectionKindEmptyPlan)
	}
	if ev.PlanID != "plan-empty" {
		t.Fatalf("correction event plan_id = %q, want %q", ev.PlanID, "plan-empty")
	}

	// The two pre-existing kinds must keep their own labels — this test must
	// not be satisfiable by mapping everything to empty_plan instead.
	if ev := (LintViolation{Kind: LintOverlap}).toCorrectionEvent("p"); ev.Kind != CorrectionKindWriteSetOverlap {
		t.Fatalf("overlap kind = %q, want %q", ev.Kind, CorrectionKindWriteSetOverlap)
	}
	if ev := (LintViolation{Kind: LintJoinless}).toCorrectionEvent("p"); ev.Kind != CorrectionKindJoinlessConvergence {
		t.Fatalf("joinless kind = %q, want %q", ev.Kind, CorrectionKindJoinlessConvergence)
	}
}

// TestPlanLint_NonEmptyPlanStillLintsPairwise guards the fix's blast radius:
// the new arity precondition must not short-circuit or otherwise disturb the
// two working lints. A clean multi-member plan must still pass, and the
// overlap/join-less checks must still fire on a dirty one.
//
// This is the "do not regress write_set_overlap / join_less_convergence"
// requirement expressed as an assertion rather than a claim.
func TestPlanLint_NonEmptyPlanStillLintsPairwise(t *testing.T) {
	p := &Plan{ID: "plan-1"}

	clean := []task.Task{
		member("a", nil, []string{"out/a.md"}),
		member("b", nil, []string{"out/b.md"}),
	}
	if lerr := Lint(p, clean); lerr != nil {
		t.Fatalf("a clean two-member plan must pass lint, got: %v", lerr)
	}

	// write_set_overlap still fires.
	overlap := []task.Task{
		member("a", nil, []string{"out/shared.md"}),
		member("b", nil, []string{"out/shared.md"}),
	}
	lerr := Lint(p, overlap)
	if lerr == nil {
		t.Fatal("two parallel members writing the same path must still be rejected (write_set_overlap)")
	}
	if lerr.Violations[0].Kind != LintOverlap {
		t.Fatalf("kind = %q, want %q", lerr.Violations[0].Kind, LintOverlap)
	}

	// join_less_convergence still fires: c converges the two parallel
	// members a and b without being an authored join member.
	joinless := []task.Task{
		member("a", nil, []string{"out/a.md"}),
		member("b", nil, []string{"out/b.md"}),
		member("c", []string{"a", "b"}, []string{"out/c.md"}),
	}
	lerr = Lint(p, joinless)
	if lerr == nil {
		t.Fatal("a join-less convergence point must still be rejected (join_less_convergence)")
	}
	if lerr.Violations[0].Kind != LintJoinless {
		t.Fatalf("kind = %q, want %q", lerr.Violations[0].Kind, LintJoinless)
	}
}

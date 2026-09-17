// Omnipus — ADR-074 D2 kind-inference tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// criterion_infer_adr074_test.go — ADR-074 D2 / judgment-first-criteria-spec
// tests #1 (InferCriterionKind table, DS-1/DS-2) and #4 (persisted kind
// always non-empty valid), plus the FR-002 call-site pin (grep-level guard on
// the allowed set).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

// TestInferCriterionKind_Table is DS-1: kind absent x {none, check, behavior,
// both} => prose / check / behavior / error — plus explicit-kind passthrough
// (inference never overrides an explicit kind).
func TestInferCriterionKind_Table(t *testing.T) {
	t.Parallel()
	check := &CriterionCheck{Command: "go test ./...", ExpectedExitCode: 0}
	behavior := &CriterionBehavior{Tool: "bash"}

	cases := []struct {
		name     string
		c        AcceptanceCriterion
		want     CriterionKind
		wantErr  bool
		errNeeds string
	}{
		{"absent_no_payload_prose", AcceptanceCriterion{Text: "t"}, KindProse, false, ""},
		{"absent_check_payload_check", AcceptanceCriterion{Text: "t", Check: check}, KindCheck, false, ""},
		{"absent_behavior_payload_behavior", AcceptanceCriterion{Text: "t", Behavior: behavior}, KindBehavior, false, ""},
		{"absent_both_payloads_error", AcceptanceCriterion{Text: "t", Check: check, Behavior: behavior}, "", true, "ambiguous"},
		{"explicit_prose_passthrough", AcceptanceCriterion{Kind: KindProse, Text: "t"}, KindProse, false, ""},
		{"explicit_check_passthrough", AcceptanceCriterion{Kind: KindCheck, Text: "t", Check: check}, KindCheck, false, ""},
		{"explicit_behavior_passthrough", AcceptanceCriterion{Kind: KindBehavior, Text: "t", Behavior: behavior}, KindBehavior, false, ""},
		// Inference NEVER overrides an explicit kind — the mismatch is left
		// for validateCriterion to 400 (DS-2 covers that below).
		{"explicit_prose_with_check_payload_passthrough", AcceptanceCriterion{Kind: KindProse, Text: "t", Check: check}, KindProse, false, ""},
		// Explicit kind + BOTH payloads (EC-1b) also passes through here;
		// validateCriterion rejects it.
		{"explicit_check_with_both_payloads_passthrough", AcceptanceCriterion{Kind: KindCheck, Text: "t", Check: check, Behavior: behavior}, KindCheck, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := tc.c
			got, err := InferCriterionKind(&c)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got kind %q", got)
				}
				if !strings.Contains(err.Error(), tc.errNeeds) {
					t.Errorf("error %q does not mention %q", err, tc.errNeeds)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("kind = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeCriteria_InferenceAndMismatches drives DS-1/DS-2 through
// normalizeCriteria — the REST-facing inference call site: kind-omitted
// criteria persist with the inferred kind (spec test #4's persisted-kind
// invariant), a kind-omitted dual-payload criterion is a validation error
// (EC-1), and every explicit-kind/payload mismatch stays a validation error
// (DS-2, pinned unchanged).
func TestNormalizeCriteria_InferenceAndMismatches(t *testing.T) {
	t.Parallel()
	author := CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"}
	check := func() *CriterionCheck { return &CriterionCheck{Command: "true", ExpectedExitCode: 0} }
	behavior := func() *CriterionBehavior {
		return &CriterionBehavior{Tool: "bash", MinCount: intp(0), MaxCount: intp(0)}
	}

	t.Run("kind_omitted_all_shapes_infer_and_persist_valid", func(t *testing.T) {
		t.Parallel()
		in := []AcceptanceCriterion{
			{Text: "prose one", Author: author},
			{Text: "check one", Check: check(), Author: author},
			{Text: "behavior one", Behavior: behavior(), Author: author},
		}
		out, err := NormalizeCriteria(in)
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		wantKinds := []CriterionKind{KindProse, KindCheck, KindBehavior}
		for i, c := range out {
			if c.Kind != wantKinds[i] {
				t.Errorf("criteria[%d].Kind = %q, want %q", i, c.Kind, wantKinds[i])
			}
			// Spec test #4: every persisted criterion carries a non-empty
			// valid kind (guards criterionKey/sameShape).
			if !IsValidCriterionKind(c.Kind) {
				t.Errorf("criteria[%d]: persisted kind %q is not valid", i, c.Kind)
			}
			if c.ID == "" || c.Status != CritPending {
				t.Errorf("criteria[%d]: id/status not server-set: id=%q status=%q", i, c.ID, c.Status)
			}
		}
		// Pointer semantics survived inference: the explicit 0/0 pair.
		b := out[2].Behavior
		if b.MinCount == nil || *b.MinCount != 0 || b.MaxCount == nil || *b.MaxCount != 0 {
			t.Errorf("explicit 0/0 behavior counts not preserved: min=%v max=%v", b.MinCount, b.MaxCount)
		}
		// The input slice's kinds must NOT have been scribbled on (normalize
		// works on a deep copy).
		if in[0].Kind != "" || in[1].Kind != "" || in[2].Kind != "" {
			t.Errorf("normalizeCriteria mutated the caller's slice: %q %q %q", in[0].Kind, in[1].Kind, in[2].Kind)
		}
	})

	t.Run("kind_omitted_dual_payload_rejected", func(t *testing.T) {
		t.Parallel()
		_, err := NormalizeCriteria([]AcceptanceCriterion{
			{Text: "ambiguous", Check: check(), Behavior: behavior(), Author: author},
		})
		if err == nil {
			t.Fatal("kind-omitted criterion with BOTH payloads must be rejected (EC-1)")
		}
		if !strings.Contains(err.Error(), "ambiguous") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("explicit_kind_payload_mismatches_rejected", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name string
			c    AcceptanceCriterion
		}{
			{"prose_with_check", AcceptanceCriterion{Kind: KindProse, Text: "t", Check: check(), Author: author}},
			{"prose_with_behavior", AcceptanceCriterion{Kind: KindProse, Text: "t", Behavior: behavior(), Author: author}},
			{"check_without_payload", AcceptanceCriterion{Kind: KindCheck, Text: "t", Author: author}},
			{"behavior_without_payload", AcceptanceCriterion{Kind: KindBehavior, Text: "t", Author: author}},
			{"check_with_behavior_too", AcceptanceCriterion{Kind: KindCheck, Text: "t", Check: check(), Behavior: behavior(), Author: author}},
			{"behavior_with_check_too", AcceptanceCriterion{Kind: KindBehavior, Text: "t", Check: check(), Behavior: behavior(), Author: author}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				if _, err := NormalizeCriteria([]AcceptanceCriterion{tc.c}); err == nil {
					t.Fatal("explicit-kind/payload mismatch must stay a validation error (DS-2)")
				}
			})
		}
	})
}

// TestInferCriterionKind_CallSitesPinned is the FR-002 grep-level guard: the
// ONLY production call sites of InferCriterionKind are the two tool-layer
// criteria parsers (pkg/tools/task.go, pkg/sysagent/tools/task.go) and
// pkg/task/criterion.go's own normalizeCriteria. The gateway converters pass
// an absent kind THROUGH and never call the helper; no other inference
// implementation or call site may appear.
func TestInferCriterionKind_CallSitesPinned(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join("..", "..")
	allowed := map[string]bool{
		filepath.Join("pkg", "task", "criterion.go"):         true,
		filepath.Join("pkg", "tools", "task.go"):             true,
		filepath.Join("pkg", "sysagent", "tools", "task.go"): true,
	}
	var callers []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == "node_modules" || base == ".git" || base == "dist" || base == ".gitnexus" ||
				base == "spa" || base == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(data), "InferCriterionKind(") {
			rel, _ := filepath.Rel(repoRoot, path)
			callers = append(callers, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(callers) == 0 {
		t.Fatal("no InferCriterionKind call sites found — the guard is scanning the wrong root")
	}
	for _, rel := range callers {
		if !allowed[rel] {
			t.Errorf("InferCriterionKind referenced from %q — FR-002 pins the call sites to exactly "+
				"{both tool parsers, normalizeCriteria}; new inference call sites need a spec change", rel)
		}
	}
}

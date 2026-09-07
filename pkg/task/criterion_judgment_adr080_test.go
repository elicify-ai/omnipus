// Omnipus — ADR-080 D-TYPES judgment-inference tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// criterion_judgment_adr080_test.go — ADR-080 D-TYPES tests: InferJudgment's
// table (the check->boolean / behavior->quantitative deterministic
// correlation, the prose author-stated-or-default-boolean catch-all, and the
// explicit-judgment-mismatches-a-technical-kind error), plus
// normalizeCriteria's judgment backfill (every persisted criterion carries an
// explicit, valid judgment — including a load-time backfill of legacy
// persisted criteria omitting the field entirely, mirroring
// criterion_infer_adr074_test.go's kind-inference coverage field-for-field).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInferJudgment_Table is ADR-080 D-TYPES' correlation table: kind=check
// => judgment boolean (explicit boolean passes through, any other explicit
// value is a mismatch error); kind=behavior => judgment quantitative (same
// shape); kind=prose => author-stated judgment passes through when valid,
// defaults to boolean when omitted, and an invalid explicit value is an
// error.
func TestInferJudgment_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		c        AcceptanceCriterion
		want     JudgmentKind
		wantErr  bool
		errNeeds string
	}{
		{"check_omitted_defaults_boolean", AcceptanceCriterion{Kind: KindCheck, Text: "t"}, JudgmentBoolean, false, ""},
		{"check_explicit_boolean_passthrough", AcceptanceCriterion{Kind: KindCheck, Judgment: JudgmentBoolean, Text: "t"}, JudgmentBoolean, false, ""},
		{"check_explicit_quantitative_mismatch", AcceptanceCriterion{Kind: KindCheck, Judgment: JudgmentQuantitative, Text: "t"}, "", true, "incompatible with kind \"check\""},
		{"check_explicit_artifact_mismatch", AcceptanceCriterion{Kind: KindCheck, Judgment: JudgmentArtifact, Text: "t"}, "", true, "incompatible with kind \"check\""},

		{"behavior_omitted_defaults_quantitative", AcceptanceCriterion{Kind: KindBehavior, Text: "t"}, JudgmentQuantitative, false, ""},
		{"behavior_explicit_quantitative_passthrough", AcceptanceCriterion{Kind: KindBehavior, Judgment: JudgmentQuantitative, Text: "t"}, JudgmentQuantitative, false, ""},
		{"behavior_explicit_boolean_mismatch", AcceptanceCriterion{Kind: KindBehavior, Judgment: JudgmentBoolean, Text: "t"}, "", true, "incompatible with kind \"behavior\""},
		{"behavior_explicit_artifact_mismatch", AcceptanceCriterion{Kind: KindBehavior, Judgment: JudgmentArtifact, Text: "t"}, "", true, "incompatible with kind \"behavior\""},

		{"prose_omitted_defaults_boolean", AcceptanceCriterion{Kind: KindProse, Text: "t"}, JudgmentBoolean, false, ""},
		{"prose_explicit_boolean_passthrough", AcceptanceCriterion{Kind: KindProse, Judgment: JudgmentBoolean, Text: "t"}, JudgmentBoolean, false, ""},
		{"prose_explicit_quantitative_passthrough", AcceptanceCriterion{Kind: KindProse, Judgment: JudgmentQuantitative, Text: "t"}, JudgmentQuantitative, false, ""},
		{"prose_explicit_artifact_passthrough", AcceptanceCriterion{Kind: KindProse, Judgment: JudgmentArtifact, Text: "t"}, JudgmentArtifact, false, ""},
		{"prose_explicit_invalid_rejected", AcceptanceCriterion{Kind: KindProse, Judgment: JudgmentKind("vibes"), Text: "t"}, "", true, "invalid judgment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := tc.c
			got, err := InferJudgment(&c)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got judgment %q", got)
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
				t.Errorf("judgment = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIsValidJudgment covers the enum gate directly, including the empty
// string (invalid — judgment is required on every persisted criterion,
// unlike provenance which legitimately stays empty).
func TestIsValidJudgment(t *testing.T) {
	t.Parallel()
	valid := []JudgmentKind{JudgmentBoolean, JudgmentQuantitative, JudgmentArtifact}
	for _, j := range valid {
		if !IsValidJudgment(j) {
			t.Errorf("IsValidJudgment(%q) = false, want true", j)
		}
	}
	invalid := []JudgmentKind{"", "vibes", "Boolean"}
	for _, j := range invalid {
		if IsValidJudgment(j) {
			t.Errorf("IsValidJudgment(%q) = true, want false", j)
		}
	}
}

// TestNormalizeCriteria_JudgmentBackfill drives ADR-080 D-TYPES through
// normalizeCriteria (the store-facing inference call site, mirroring
// TestNormalizeCriteria_InferenceAndMismatches's kind coverage): a
// judgment-omitted criterion of every kind persists with the inferred,
// deterministic-or-default judgment — this is also the LOAD-TIME backfill
// path for legacy (pre-ADR-080) persisted criteria carrying no judgment at
// all, since normalizeCriteria is the single point every store write and
// re-serialization passes through.
func TestNormalizeCriteria_JudgmentBackfill(t *testing.T) {
	t.Parallel()
	author := CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"}
	check := func() *CriterionCheck { return &CriterionCheck{Command: "true", ExpectedExitCode: 0} }
	behavior := func() *CriterionBehavior { return &CriterionBehavior{Tool: "bash"} }

	in := []AcceptanceCriterion{
		{Kind: KindProse, Text: "prose one", Author: author},
		{Kind: KindCheck, Text: "check one", Check: check(), Author: author},
		{Kind: KindBehavior, Text: "behavior one", Behavior: behavior(), Author: author},
	}
	out, err := NormalizeCriteria(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	wantJudgments := []JudgmentKind{JudgmentBoolean, JudgmentBoolean, JudgmentQuantitative}
	for i, c := range out {
		if c.Judgment != wantJudgments[i] {
			t.Errorf("criteria[%d].Judgment = %q, want %q", i, c.Judgment, wantJudgments[i])
		}
		if !IsValidJudgment(c.Judgment) {
			t.Errorf("criteria[%d]: persisted judgment %q is not valid", i, c.Judgment)
		}
	}
	// The input slice's judgments must NOT have been scribbled on (normalize
	// works on a deep copy, mirroring the kind invariant).
	if in[0].Judgment != "" || in[1].Judgment != "" || in[2].Judgment != "" {
		t.Errorf("normalizeCriteria mutated the caller's slice: %q %q %q", in[0].Judgment, in[1].Judgment, in[2].Judgment)
	}

	t.Run("explicit_judgment_mismatch_rejected", func(t *testing.T) {
		t.Parallel()
		_, err := NormalizeCriteria([]AcceptanceCriterion{
			{Kind: KindCheck, Judgment: JudgmentArtifact, Text: "t", Check: check(), Author: author},
		})
		if err == nil {
			t.Fatal("explicit judgment mismatching a technical kind must stay a validation error")
		}
	})

	t.Run("explicit_prose_judgment_preserved", func(t *testing.T) {
		t.Parallel()
		out, err := NormalizeCriteria([]AcceptanceCriterion{
			{Kind: KindProse, Judgment: JudgmentArtifact, Text: "a report exists", Author: author},
		})
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		if out[0].Judgment != JudgmentArtifact {
			t.Errorf("Judgment = %q, want %q (explicit author-stated value must survive)", out[0].Judgment, JudgmentArtifact)
		}
	})

	t.Run("invalid_provenance_rejected", func(t *testing.T) {
		t.Parallel()
		_, err := NormalizeCriteria([]AcceptanceCriterion{
			{Kind: KindProse, Text: "t", Author: author, Provenance: CriterionProvenance("made-up")},
		})
		if err == nil {
			t.Fatal("invalid provenance must be rejected")
		}
	})

	t.Run("valid_provenance_preserved", func(t *testing.T) {
		t.Parallel()
		out, err := NormalizeCriteria([]AcceptanceCriterion{
			{Kind: KindProse, Text: "t", Author: author, Provenance: ProvenanceFloor},
		})
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		if out[0].Provenance != ProvenanceFloor {
			t.Errorf("Provenance = %q, want %q", out[0].Provenance, ProvenanceFloor)
		}
	})
}

// TestInferJudgment_CallSitesPinned mirrors
// TestInferCriterionKind_CallSitesPinned's FR-002-style guard: InferJudgment's
// production call sites are pinned to exactly normalizeCriteria and the same
// two tool-layer criteria parsers InferCriterionKind is pinned to (each
// parser resolves judgment immediately after kind, so a caller-supplied
// judgment is honored and criterionKey/sameShape dedup comparisons never see
// a judgment-less criterion next to an already-normalized one). A new call
// site appearing anywhere else in the tree needs a spec change.
func TestInferJudgment_CallSitesPinned(t *testing.T) {
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
		if strings.Contains(string(data), "InferJudgment(") {
			rel, _ := filepath.Rel(repoRoot, path)
			callers = append(callers, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(callers) == 0 {
		t.Fatal("no InferJudgment call sites found — the guard is scanning the wrong root")
	}
	for _, rel := range callers {
		if !allowed[rel] {
			t.Errorf("InferJudgment referenced from %q — this wave pins the call site to exactly "+
				"{normalizeCriteria}; a new inference call site (e.g. a tool parser) needs a spec change", rel)
		}
	}
}

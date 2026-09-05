// Omnipus — ADR-074 D3a drift-repair tests (pkg/tools half)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// criteria_behavior_adr074_test.go — ADR-074 D3a (judgment-first criteria,
// spec judgment-first-criteria-spec.md tests #2/#5): the three pkg/tools
// criteria-authoring schemas (create_task, create_plan, plan_correct) carry
// the full three-kind enum the contract has had since ADR-052 FR-034, and
// parseCriteriaArgs decodes a `behavior` payload with the documented
// MinCount/MaxCount pointer semantics (explicit 0 != absent).

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// criterionItemsSchema drills into a tool Parameters() map to the criteria
// item schema: params.properties.<arrayField>.items (optionally through
// tail_members.items.properties first for plan_correct).
func criterionItemsSchema(t *testing.T, params map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := params
	for _, step := range path {
		props, ok := cur["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema node has no properties map while descending to %q (path %v)", step, path)
		}
		next, ok := props[step].(map[string]any)
		if !ok {
			t.Fatalf("schema property %q missing (path %v)", step, path)
		}
		items, ok := next["items"].(map[string]any)
		if !ok {
			t.Fatalf("schema property %q has no items object (path %v)", step, path)
		}
		cur = items
	}
	return cur
}

// assertCriterionSchemaShape asserts one criterion item schema carries the
// full three-kind enum and a behavior payload object (ADR-074 D3a).
func assertCriterionSchemaShape(t *testing.T, items map[string]any) {
	t.Helper()
	props, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("criterion items schema has no properties")
	}
	kind, ok := props["kind"].(map[string]any)
	if !ok {
		t.Fatal("criterion items schema has no kind property")
	}
	enum, ok := kind["enum"].([]string)
	if !ok {
		t.Fatalf("kind.enum is %T, want []string", kind["enum"])
	}
	want := []string{"check", "prose", "behavior"}
	if len(enum) != len(want) {
		t.Fatalf("kind.enum = %v, want %v", enum, want)
	}
	for i := range want {
		if enum[i] != want[i] {
			t.Fatalf("kind.enum = %v, want %v", enum, want)
		}
	}
	beh, ok := props["behavior"].(map[string]any)
	if !ok {
		t.Fatal("criterion items schema has no behavior property (ADR-074 D3a payload decode has no schema surface)")
	}
	behProps, ok := beh["properties"].(map[string]any)
	if !ok {
		t.Fatal("behavior schema has no properties")
	}
	for _, f := range []string{"tool", "min_count", "max_count", "scope"} {
		if _, present := behProps[f]; !present {
			t.Errorf("behavior schema is missing field %q", f)
		}
	}
	req, ok := beh["required"].([]string)
	if !ok || len(req) != 1 || req[0] != "tool" {
		t.Errorf("behavior.required = %v, want [tool]", beh["required"])
	}
}

// TestCriteriaSchemas_BehaviorKind_AllThreeTools proves create_task,
// create_plan and plan_correct all advertise kind enum [check, prose,
// behavior] plus a behavior payload schema (ADR-074 D3a; required-test #5's
// enum half).
func TestCriteriaSchemas_BehaviorKind_AllThreeTools(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		params map[string]any
		path   []string
	}{
		{"create_task", (&TaskCreateTool{}).Parameters(), []string{"criteria"}},
		{"create_plan", (&PlanCreateTool{}).Parameters(), []string{"dod"}},
		{"plan_correct", (&PlanCorrectTool{}).Parameters(), []string{"tail_members", "criteria"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertCriterionSchemaShape(t, criterionItemsSchema(t, tc.params, tc.path...))
		})
	}
}

// TestParseCriteriaArgs_BehaviorDecode is spec test #2 (DS-4) for the
// pkg/tools parser: a behavior payload decodes end-to-end through
// parseCriteriaArgs and task.NormalizeCriteria with pointer semantics intact
// — absent min_count defaults to 1 at validation, an EXPLICIT zero survives
// as zero, and max_count < min_count is rejected.
func TestParseCriteriaArgs_BehaviorDecode(t *testing.T) {
	t.Parallel()

	behaviorArg := func(kind string, minCount, maxCount any) []any {
		beh := map[string]any{"tool": "bash"}
		if minCount != nil {
			beh["min_count"] = minCount
		}
		if maxCount != nil {
			beh["max_count"] = maxCount
		}
		m := map[string]any{"text": "bash usage bounded", "behavior": beh}
		if kind != "" {
			m["kind"] = kind
		}
		return []any{m}
	}

	t.Run("absent_counts_default_min_1_unbounded_max", func(t *testing.T) {
		t.Parallel()
		parsed, err := parseCriteriaArgs(behaviorArg("behavior", nil, nil), "author-a")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if parsed[0].Behavior == nil {
			t.Fatal("behavior payload was discarded by the parser")
		}
		if parsed[0].Behavior.MinCount != nil {
			t.Fatalf("parser must keep an ABSENT min_count nil, got %d", *parsed[0].Behavior.MinCount)
		}
		norm, err := task.NormalizeCriteria(parsed)
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		if norm[0].Behavior.MinCount == nil || *norm[0].Behavior.MinCount != 1 {
			t.Errorf("absent min_count must default to 1 at validation, got %v", norm[0].Behavior.MinCount)
		}
		if norm[0].Behavior.MaxCount != nil {
			t.Errorf("absent max_count must stay nil (unbounded), got %d", *norm[0].Behavior.MaxCount)
		}
	})

	t.Run("explicit_zero_zero_never_call_preserved", func(t *testing.T) {
		t.Parallel()
		parsed, err := parseCriteriaArgs(behaviorArg("behavior", float64(0), float64(0)), "author-a")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		norm, err := task.NormalizeCriteria(parsed)
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		b := norm[0].Behavior
		if b.MinCount == nil || *b.MinCount != 0 {
			t.Errorf("EXPLICIT min_count 0 must survive as 0 (never default to 1), got %v", b.MinCount)
		}
		if b.MaxCount == nil || *b.MaxCount != 0 {
			t.Errorf("EXPLICIT max_count 0 must survive as 0, got %v", b.MaxCount)
		}
	})

	t.Run("zero_to_three_range_preserved", func(t *testing.T) {
		t.Parallel()
		parsed, err := parseCriteriaArgs(behaviorArg("behavior", float64(0), float64(3)), "author-a")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		norm, err := task.NormalizeCriteria(parsed)
		if err != nil {
			t.Fatalf("normalize: %v", err)
		}
		b := norm[0].Behavior
		if b.MinCount == nil || *b.MinCount != 0 || b.MaxCount == nil || *b.MaxCount != 3 {
			t.Errorf("0..3 range not preserved: min=%v max=%v", b.MinCount, b.MaxCount)
		}
	})

	t.Run("max_below_min_rejected", func(t *testing.T) {
		t.Parallel()
		parsed, err := parseCriteriaArgs(behaviorArg("behavior", float64(3), float64(1)), "author-a")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := task.NormalizeCriteria(parsed); err == nil {
			t.Fatal("max_count < min_count must be rejected at validation")
		} else if !strings.Contains(err.Error(), "max_count") {
			t.Errorf("unexpected rejection message: %v", err)
		}
	})

	t.Run("scope_decoded", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"kind": "behavior", "text": "attempt-scoped",
			"behavior": map[string]any{"tool": "bash", "scope": "attempt"},
		}}
		parsed, err := parseCriteriaArgs(raw, "author-a")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if parsed[0].Behavior.Scope != task.BehaviorScopeAttempt {
			t.Errorf("scope = %q, want attempt", parsed[0].Behavior.Scope)
		}
	})
}

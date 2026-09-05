// Omnipus — ADR-074 D3a drift-repair tests (sysagent twin)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

// criteria_behavior_adr074_test.go — ADR-074 D3a (judgment-first criteria,
// spec judgment-first-criteria-spec.md tests #2/#5): create_task_in_workspace
// advertises the full three-kind enum and its parser twin decodes a
// `behavior` payload with the documented MinCount/MaxCount pointer semantics
// (explicit 0 != absent). Internal-package test: the parser twin is
// unexported by design (see its doc comment).

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestWorkspaceCriteriaSchema_BehaviorKind proves create_task_in_workspace's
// criteria schema carries kind enum [check, prose, behavior] plus the
// behavior payload object (ADR-074 D3a; required-test #5's enum half).
func TestWorkspaceCriteriaSchema_BehaviorKind(t *testing.T) {
	t.Parallel()
	params := (&TaskCreateTool{}).Parameters()
	props := params["properties"].(map[string]any)
	items := props["criteria"].(map[string]any)["items"].(map[string]any)
	itemProps := items["properties"].(map[string]any)

	enum, ok := itemProps["kind"].(map[string]any)["enum"].([]string)
	if !ok {
		t.Fatalf("kind.enum is %T, want []string", itemProps["kind"].(map[string]any)["enum"])
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

	beh, ok := itemProps["behavior"].(map[string]any)
	if !ok {
		t.Fatal("criteria items schema has no behavior property (ADR-074 D3a)")
	}
	behProps := beh["properties"].(map[string]any)
	for _, f := range []string{"tool", "min_count", "max_count", "scope"} {
		if _, ok := behProps[f]; !ok {
			t.Errorf("behavior schema is missing field %q", f)
		}
	}
}

// TestParseCriteriaArgsFromWorkspaceTool_BehaviorDecode is spec test #2
// (DS-4) for the sysagent parser twin — mirrors pkg/tools'
// TestParseCriteriaArgs_BehaviorDecode.
func TestParseCriteriaArgsFromWorkspaceTool_BehaviorDecode(t *testing.T) {
	t.Parallel()

	behaviorArg := func(minCount, maxCount any) []any {
		beh := map[string]any{"tool": "bash"}
		if minCount != nil {
			beh["min_count"] = minCount
		}
		if maxCount != nil {
			beh["max_count"] = maxCount
		}
		return []any{map[string]any{"kind": "behavior", "text": "bash usage bounded", "behavior": beh}}
	}

	t.Run("absent_counts_default_min_1_unbounded_max", func(t *testing.T) {
		t.Parallel()
		parsed, err := parseCriteriaArgsFromWorkspaceTool(behaviorArg(nil, nil), "author-a")
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
		parsed, err := parseCriteriaArgsFromWorkspaceTool(behaviorArg(float64(0), float64(0)), "author-a")
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

	t.Run("max_below_min_rejected", func(t *testing.T) {
		t.Parallel()
		parsed, err := parseCriteriaArgsFromWorkspaceTool(behaviorArg(float64(3), float64(1)), "author-a")
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
		parsed, err := parseCriteriaArgsFromWorkspaceTool(raw, "author-a")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if parsed[0].Behavior.Scope != task.BehaviorScopeAttempt {
			t.Errorf("scope = %q, want attempt", parsed[0].Behavior.Scope)
		}
	})
}

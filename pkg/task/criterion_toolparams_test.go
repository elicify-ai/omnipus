// Omnipus — behavior-criterion tool-parameter helper tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import "testing"

// TestDecodeBehaviorPayload_PointerSemantics pins the ADR-052 FR-034
// pointer semantics the shared decoder must honor now that pkg/tools and
// pkg/sysagent/tools both consume it instead of private copies: an ABSENT
// min_count/max_count stays nil, while an EXPLICIT 0 decodes to a pointer
// at 0 — the distinction that keeps {min_count:0, max_count:0} ("never
// call this tool") expressible.
func TestDecodeBehaviorPayload_PointerSemantics(t *testing.T) {
	t.Run("absent counts stay nil", func(t *testing.T) {
		b := DecodeBehaviorPayload(map[string]any{"tool": "bash"})
		if b.Tool != "bash" {
			t.Fatalf("tool = %q, want bash", b.Tool)
		}
		if b.MinCount != nil || b.MaxCount != nil {
			t.Fatalf("absent min_count/max_count must decode to nil, got %v %v", b.MinCount, b.MaxCount)
		}
		if b.Scope != "" {
			t.Fatalf("absent scope must stay empty, got %q", b.Scope)
		}
		if got := b.EffectiveMinCount(); got != 1 {
			t.Fatalf("nil MinCount must default to 1 via EffectiveMinCount, got %d", got)
		}
	})

	t.Run("explicit zero decodes to a pointer at 0", func(t *testing.T) {
		b := DecodeBehaviorPayload(map[string]any{
			"tool":      "bash",
			"min_count": float64(0),
			"max_count": float64(0),
		})
		if b.MinCount == nil || *b.MinCount != 0 {
			t.Fatalf("explicit min_count 0 must decode to a pointer at 0, got %v", b.MinCount)
		}
		if b.MaxCount == nil || *b.MaxCount != 0 {
			t.Fatalf("explicit max_count 0 must decode to a pointer at 0, got %v", b.MaxCount)
		}
		if got := b.EffectiveMinCount(); got != 0 {
			t.Fatalf("explicit MinCount 0 must stay 0 via EffectiveMinCount, got %d", got)
		}
	})

	t.Run("counts and scope carried", func(t *testing.T) {
		b := DecodeBehaviorPayload(map[string]any{
			"tool":      "web_search",
			"min_count": float64(2),
			"max_count": float64(5),
			"scope":     "attempt",
		})
		if b.MinCount == nil || *b.MinCount != 2 || b.MaxCount == nil || *b.MaxCount != 5 {
			t.Fatalf("counts not carried: %v %v", b.MinCount, b.MaxCount)
		}
		if b.Scope != BehaviorScopeAttempt {
			t.Fatalf("scope = %q, want %q", b.Scope, BehaviorScopeAttempt)
		}
	})

	t.Run("non-numeric counts treated as absent", func(t *testing.T) {
		b := DecodeBehaviorPayload(map[string]any{"tool": "bash", "min_count": "2", "scope": 7})
		if b.MinCount != nil {
			t.Fatalf("non-numeric min_count must stay nil, got %v", b.MinCount)
		}
		if b.Scope != "" {
			t.Fatalf("non-string scope must stay empty, got %q", b.Scope)
		}
	})
}

// TestBehaviorCriterionParamSchema_Shape pins the shared JSON-schema
// fragment's load-bearing shape: the four CriterionBehavior fields, the
// required tool, the zero floors that keep an explicit 0 legal, and the
// scope enum.
func TestBehaviorCriterionParamSchema_Shape(t *testing.T) {
	s := BehaviorCriterionParamSchema()
	if s["type"] != "object" {
		t.Fatalf("type = %v, want object", s["type"])
	}
	req, ok := s["required"].([]string)
	if !ok || len(req) != 1 || req[0] != "tool" {
		t.Fatalf("required = %v, want [tool]", s["required"])
	}
	props, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties is %T, want map", s["properties"])
	}
	for _, field := range []string{"tool", "min_count", "max_count", "scope"} {
		if _, present := props[field]; !present {
			t.Fatalf("schema is missing the %q property", field)
		}
	}
	for _, field := range []string{"min_count", "max_count"} {
		fs := props[field].(map[string]any)
		if fs["type"] != "integer" || fs["minimum"] != 0 {
			t.Fatalf("%s must be an integer with minimum 0, got %v", field, fs)
		}
	}
	scope := props["scope"].(map[string]any)
	enum, ok := scope["enum"].([]string)
	if !ok || len(enum) != 2 || enum[0] != "attempt" || enum[1] != "task_session" {
		t.Fatalf("scope enum = %v, want [attempt task_session]", scope["enum"])
	}
}

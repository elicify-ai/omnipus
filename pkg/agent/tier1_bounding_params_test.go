// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memory"
)

// TestTier1Tools_BoundingParams — spec test 45 (ADR-066 spec FR-040, B-47,
// DS-9 #3). recall_conversation's archive-selection modes must accept a
// caller-supplied max_results (turns, ≥ 1) that NARROWS the built-in bound,
// and must refuse an out-of-range value with a tool error.
//
// DS-9 #1 and #2 (list_directory / inspect_session offset+limit) live in the
// pkg/tools test of the same name.
func TestTier1Tools_BoundingParams(t *testing.T) {
	// recallArchive builds n query-matching turns plus the nonce they share.
	const nonce = "TIER1_BOUND_NONCE"
	recallArchive := func(n int) *stubArchive {
		msgs := make([]memory.ArchivedMessage, 0, 2*n)
		for i := 0; i < n; i++ {
			msgs = append(msgs,
				makeUserMsg(fmt.Sprintf("turn %d about %s", i+1, nonce)),
				makeAssistantMsg(fmt.Sprintf("answer %d", i+1)),
			)
		}
		return &stubArchive{msgs: msgs}
	}

	t.Run("schema advertises max_results", func(t *testing.T) {
		props, ok := makeTool(&stubArchive{}, newStubSpanSetter()).
			Parameters()["properties"].(map[string]any)
		if !ok {
			t.Fatal("recall_conversation schema has no properties map")
		}
		p, ok := props["max_results"].(map[string]any)
		if !ok {
			t.Fatalf("recall_conversation schema has no max_results property: %v", props)
		}
		if p["type"] != "integer" {
			t.Errorf("max_results type = %v, want integer", p["type"])
		}
		if desc, _ := p["description"].(string); desc == "" {
			t.Error("max_results has no description — FR-040 requires it documented in the schema")
		}
	})

	// DS-9 #3: query + max_results 3 → at most 3 turns.
	t.Run("query mode honours max_results", func(t *testing.T) {
		setter := newStubSpanSetter()
		res := makeTool(recallArchive(12), setter).Execute(makeCtx("sess-mr-query"),
			map[string]any{"query": nonce, "max_results": 3})
		if res.IsError {
			t.Fatalf("bounded query recall failed: %s", res.ForLLM)
		}
		span := setter.spans["sess-mr-query"]
		if span == nil {
			t.Fatal("no span installed")
		}
		if len(span.Ordinals) != 3 {
			t.Fatalf("recalled %d turns (%v), want 3", len(span.Ordinals), span.Ordinals)
		}
		if !strings.Contains(res.ForLLM, "3 turn(s)") {
			t.Errorf("receipt must state the bounded count, got: %s", res.ForLLM)
		}
	})

	t.Run("turn_range mode honours max_results", func(t *testing.T) {
		setter := newStubSpanSetter()
		res := makeTool(recallArchive(12), setter).Execute(makeCtx("sess-mr-range"),
			map[string]any{"turn_range": "1-10", "max_results": 2})
		if res.IsError {
			t.Fatalf("bounded range recall failed: %s", res.ForLLM)
		}
		span := setter.spans["sess-mr-range"]
		if span == nil {
			t.Fatal("no span installed")
		}
		if len(span.Ordinals) != 2 {
			t.Fatalf("recalled %d turns (%v), want 2", len(span.Ordinals), span.Ordinals)
		}
		if span.Ordinals[0] != 1 || span.Ordinals[1] != 2 {
			t.Errorf("ordinals = %v, want the first two of the range", span.Ordinals)
		}
	})

	// max_results only ever narrows: it can never lift the built-in bound
	// (8 turns in query mode) that keeps a recall inside the budget.
	t.Run("max_results never widens the built-in bound", func(t *testing.T) {
		setter := newStubSpanSetter()
		res := makeTool(recallArchive(30), setter).Execute(makeCtx("sess-mr-wide"),
			map[string]any{"query": nonce, "max_results": 500})
		if res.IsError {
			t.Fatalf("recall with a large max_results failed: %s", res.ForLLM)
		}
		span := setter.spans["sess-mr-wide"]
		if span == nil {
			t.Fatal("no span installed")
		}
		if len(span.Ordinals) > recallDefaultTurns {
			t.Fatalf("recalled %d turns, want at most the built-in bound %d",
				len(span.Ordinals), recallDefaultTurns)
		}
	})

	t.Run("max_results below one is refused", func(t *testing.T) {
		for _, bad := range []any{0, -3} {
			setter := newStubSpanSetter()
			res := makeTool(recallArchive(4), setter).Execute(makeCtx("sess-mr-bad"),
				map[string]any{"query": nonce, "max_results": bad})
			if !res.IsError {
				t.Fatalf("max_results %v must be a tool error, got: %s", bad, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "max_results") {
				t.Errorf("error must name the offending parameter, got: %s", res.ForLLM)
			}
			if len(setter.spans) != 0 {
				t.Error("a refused call must not install a span")
			}
		}
	})

	t.Run("non-integer max_results is refused", func(t *testing.T) {
		setter := newStubSpanSetter()
		res := makeTool(recallArchive(4), setter).Execute(makeCtx("sess-mr-nan"),
			map[string]any{"query": nonce, "max_results": "many"})
		if !res.IsError {
			t.Fatalf("a non-integer max_results must be a tool error, got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "max_results") {
			t.Errorf("error must name the offending parameter, got: %s", res.ForLLM)
		}
	})
}

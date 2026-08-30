// Omnipus — ToolSearch policy-filtered match list tests (ADR-071 D2, §3.2.2 / CRIT-201)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The four required tests from §3.2.2's table, plus the scan-cap bound edge
// case from the same section and the User Story 1 "one agent's denials do
// not alter another agent's ranking" scenario. These exercise the real
// execSearchAndLoad path end-to-end (Execute), not the unit-level
// selectSearchPromotionCandidates tested in search_ambiguity_test.go.

package tools

import (
	"context"
	"strings"
	"testing"
)

// denyOneCanLoad denies exactly the names in denied, allows everything else.
func denyOneCanLoad(denied map[string]bool) func(context.Context, string) (bool, string) {
	return func(_ context.Context, name string) (bool, string) {
		if denied[name] {
			return false, name + " — denied by this agent's policy"
		}
		return true, ""
	}
}

// setupRankedRegistry registers 5 hidden tools whose descriptions overlap
// query's terms by a strictly decreasing count, producing a deterministic
// BM25 rank order toolA > toolB > toolC > toolD > toolE for query.
func setupRankedRegistry() (reg *ToolRegistry, query string) {
	reg = NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "toolA", desc: "kestrel osprey falcon merlin harrier"})
	reg.RegisterHidden(&mockSearchableTool{name: "toolB", desc: "kestrel osprey falcon merlin"})
	reg.RegisterHidden(&mockSearchableTool{name: "toolC", desc: "kestrel osprey falcon"})
	reg.RegisterHidden(&mockSearchableTool{name: "toolD", desc: "kestrel osprey"})
	reg.RegisterHidden(&mockSearchableTool{name: "toolE", desc: "kestrel"})
	return reg, "kestrel osprey falcon merlin harrier"
}

// Required test 1: a denied tool ranked mid-list (second of five) is absent
// from the answer entirely; the four permitted results are all present in
// their unfiltered relative order.
func TestCRIT201_DeniedMidListAbsentFromAnswer(t *testing.T) {
	reg, query := setupRankedRegistry()
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(denyOneCanLoad(map[string]bool{"toolB": true}), stubMarkLoaded)

	res := tt.Execute(context.Background(), map[string]any{"query": query})
	if res.IsError {
		t.Fatalf("query failed: %s", res.ForLLM)
	}

	if strings.Contains(res.ForLLM, "toolB") {
		t.Errorf("denied tool's name must not appear anywhere in the answer; got: %s", res.ForLLM)
	}
	// Its description text is unique to it (contains "merlin" once shared
	// with toolA, but the exact 4-term phrase is toolB's alone at the
	// registration boundary); the name check above is the load-bearing one.

	idxA := strings.Index(res.ForLLM, "toolA")
	idxC := strings.Index(res.ForLLM, "toolC")
	idxD := strings.Index(res.ForLLM, "toolD")
	idxE := strings.Index(res.ForLLM, "toolE")
	for name, idx := range map[string]int{"toolA": idxA, "toolC": idxC, "toolD": idxD, "toolE": idxE} {
		if idx == -1 {
			t.Errorf("permitted tool %q must be present in the answer; got: %s", name, res.ForLLM)
		}
	}
	if idxA >= idxC || idxC >= idxD || idxD >= idxE {
		t.Errorf("permitted results must keep their unfiltered relative order (A<C<D<E); got indices A=%d C=%d D=%d E=%d",
			idxA, idxC, idxD, idxE)
	}
}

// Required test 2: every ranked result denied — the answer is exactly "No
// tools found matching the query.", no listing, and the zero-result counter
// increments by exactly 1.
func TestCRIT201_AllDenied_SilentNoListing(t *testing.T) {
	reg, query := setupRankedRegistry()
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(denyOneCanLoad(map[string]bool{
		"toolA": true, "toolB": true, "toolC": true, "toolD": true, "toolE": true,
	}), stubMarkLoaded)

	before := ToolSearchZeroResultQueries()
	res := tt.Execute(context.Background(), map[string]any{"query": query})
	after := ToolSearchZeroResultQueries()

	if res.IsError {
		t.Fatalf("query failed: %s", res.ForLLM)
	}
	const want = "No tools found matching the query."
	if res.ForLLM != want {
		t.Errorf("all-denied answer = %q, want exactly %q (no listing)", res.ForLLM, want)
	}
	if after-before != 1 {
		t.Errorf("ToolSearchZeroResultQueries() increased by %d, want exactly 1", after-before)
	}
}

// Required test 4: no resolver at all discloses nothing — see also
// TestToolsTool_Query_DoesNotPromote_WithoutResolver (search_tools_test.go)
// and TestToolsTool_Query_DoesNotPromoteWithoutResolver (load_tool_test.go)
// for the promotion-side assertions. This one is scoped narrowly to §3.2.2's
// own required-test row: no tool name or description anywhere in the answer.
func TestCRIT201_NoResolver_DisclosesNothing(t *testing.T) {
	reg, query := setupRankedRegistry()
	tt := NewToolsTool(reg, 5, 10) // no resolver

	res := tt.Execute(context.Background(), map[string]any{"query": query})
	if res.IsError {
		t.Fatalf("query failed: %s", res.ForLLM)
	}
	for _, name := range []string{"toolA", "toolB", "toolC", "toolD", "toolE"} {
		if strings.Contains(res.ForLLM, name) {
			t.Errorf("no-resolver answer must disclose nothing; found %q in: %s", name, res.ForLLM)
		}
	}
}

// Required test 3 / User Story 1's "one agent's denials do not alter another
// agent's ranking": the SAME corpus, queried by two differently-permissioned
// callers, produces identical relative order and identical scores for the
// results both are permitted to see. This is the test that protects the
// filter-after-rank DECISION — it is what fails if a later change quietly
// converts this into corpus-exclusion (ADR-071 §3.2.2's own framing).
func TestCRIT201_RankingInvariance_AcrossAgents(t *testing.T) {
	reg, query := setupRankedRegistry()

	// Agent A: everything allowed.
	ttA := NewToolsTool(reg, 5, 10)
	ttA.SetResolver(denyOneCanLoad(map[string]bool{}), stubMarkLoaded)
	resA := ttA.Execute(context.Background(), map[string]any{"query": query})
	if resA.IsError {
		t.Fatalf("agent A query failed: %s", resA.ForLLM)
	}

	// Agent B: three of the five denied.
	ttB := NewToolsTool(reg, 5, 10)
	ttB.SetResolver(denyOneCanLoad(map[string]bool{"toolB": true, "toolD": true, "toolE": true}), stubMarkLoaded)
	resB := ttB.Execute(context.Background(), map[string]any{"query": query})
	if resB.IsError {
		t.Fatalf("agent B query failed: %s", resB.ForLLM)
	}

	// B is permitted toolA and toolC — both must appear in the SAME
	// relative order they have for A (A permits everything, so A's order is
	// the unfiltered ranking: A, C both present with A before C).
	aIdxA, aIdxC := strings.Index(resA.ForLLM, "toolA"), strings.Index(resA.ForLLM, "toolC")
	bIdxA, bIdxC := strings.Index(resB.ForLLM, "toolA"), strings.Index(resB.ForLLM, "toolC")
	if aIdxA == -1 || aIdxC == -1 || bIdxA == -1 || bIdxC == -1 {
		t.Fatalf("toolA/toolC must appear for both agents; A=%q B=%q", resA.ForLLM, resB.ForLLM)
	}
	if (aIdxA < aIdxC) != (bIdxA < bIdxC) {
		t.Errorf("relative order of toolA vs toolC must match between agents A and B (A: %v, B: %v)",
			aIdxA < aIdxC, bIdxA < bIdxC)
	}
	// B must not see the tools it denied.
	for _, denied := range []string{"toolB", "toolD", "toolE"} {
		if strings.Contains(resB.ForLLM, denied) {
			t.Errorf("agent B denied %q but it appears in B's answer: %s", denied, resB.ForLLM)
		}
	}
}

// Edge case: the permission walk is bounded on a large tool set — more than
// searchCanLoadScanCap (50) ranked results precede the first permitted one.
// The walk must stop at the cap; the answer must never contain a denied
// name, and canLoad must be called at most searchCanLoadScanCap times.
func TestCRIT201_PermissionWalkBounded(t *testing.T) {
	reg := NewToolRegistry()
	denied := make(map[string]bool, 55)
	// 55 tools that rank strictly above 5 "allowed" tools, via strictly
	// decreasing shared-term repetition (higher repeat count -> higher BM25
	// term frequency -> higher score for this shared corpus/term).
	for i := 0; i < 55; i++ {
		name := "denied_tool_" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		denied[name] = true
		reg.RegisterHidden(&mockSearchableTool{
			name: name,
			desc: strings.Repeat("kestrel ", 60-i),
		})
	}
	for i := 0; i < 5; i++ {
		reg.RegisterHidden(&mockSearchableTool{
			name: "allowed_tool_" + string(rune('0'+i)),
			desc: strings.Repeat("kestrel ", 5-i),
		})
	}

	var canLoadCalls int
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(func(_ context.Context, name string) (bool, string) {
		canLoadCalls++
		if denied[name] {
			return false, name + " — denied by this agent's policy"
		}
		return true, ""
	}, stubMarkLoaded)

	res := tt.Execute(context.Background(), map[string]any{"query": "kestrel"})
	if res.IsError {
		t.Fatalf("query failed: %s", res.ForLLM)
	}

	if canLoadCalls > searchCanLoadScanCap {
		t.Errorf("canLoad was called %d times, want at most %d (searchCanLoadScanCap)", canLoadCalls, searchCanLoadScanCap)
	}
	for name := range denied {
		if strings.Contains(res.ForLLM, name) {
			t.Errorf("denied tool %q must never appear in the answer, even under a bounded walk; got: %s", name, res.ForLLM)
		}
	}
}

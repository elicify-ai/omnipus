// Omnipus — tools(names=load) tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the names/load path of the unified ToolsTool.
// All behavioral assertions from the former ToolSearch_test.go are preserved.

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeResolver builds a canLoad + markLoaded pair for testing.
// available is the set of names that canLoad returns (true, "") for.
// markLoaded always returns a stub schema for each accepted name.
func fakeResolver(available map[string]struct{}) (
	canLoad func(ctx context.Context, name string) (bool, string),
	markLoaded func(ctx context.Context, names []string) (map[string]any, []string),
) {
	canLoad = func(_ context.Context, name string) (bool, string) {
		_, ok := available[name]
		if ok {
			return true, ""
		}
		return false, name + " — not in test available set"
	}
	markLoaded = func(_ context.Context, names []string) (map[string]any, []string) {
		loaded := make(map[string]any, len(names))
		for _, n := range names {
			if _, ok := available[n]; ok {
				loaded[n] = map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        n,
						"description": "stub schema for " + n,
						"parameters":  map[string]any{"type": "object"},
					},
				}
			}
		}
		return loaded, nil
	}
	return canLoad,
		markLoaded
}

// newWiredToolsTool constructs a ToolsTool with resolver wired (no real registry).
func newWiredToolsTool(available map[string]struct{}) *ToolsTool {
	tt := NewToolsTool(nil, 5, 10)
	cl, ml := fakeResolver(available)
	tt.SetResolver(cl, ml)
	return tt
}

// execLoad calls tools{names:names} on tt — the param-inferred load path.
func execLoad(tt *ToolsTool, ctx context.Context, names []any) *ToolResult {
	return tt.Execute(ctx, map[string]any{"names": names})
}

// parseLoadResult decodes the SilentResult ForLLM JSON into a map.
func parseLoadResult(t *testing.T, r *ToolResult) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.ForLLM), &out); err != nil {
		t.Fatalf("failed to parse tools(load) result JSON: %v\nraw: %s", err, r.ForLLM)
	}
	return out
}

func loadedNames(t *testing.T, m map[string]any) []string {
	t.Helper()
	raw, ok := m["loaded"]
	if !ok {
		t.Fatal("result missing 'loaded' key")
	}
	sl, ok := raw.([]any)
	if !ok {
		t.Fatalf("'loaded' is not []any, got %T", raw)
	}
	names := make([]string, len(sl))
	for i, v := range sl {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("'loaded' element %d is not a string, got %T", i, v)
		}
		names[i] = s
	}
	return names
}

func rejectedNames(t *testing.T, m map[string]any) []string {
	t.Helper()
	raw, ok := m["rejected"]
	if !ok {
		return nil
	}
	sl, ok := raw.([]any)
	if !ok {
		return nil
	}
	names := make([]string, len(sl))
	for i, v := range sl {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("'rejected' element %d is not a string, got %T", i, v)
		}
		names[i] = s
	}
	return names
}

func schemas(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	raw, ok := m["schemas"]
	if !ok {
		t.Fatal("result missing 'schemas' key")
	}
	s, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("'schemas' is not map[string]any, got %T", raw)
	}
	return s
}

// --- Tests ---

func TestToolsTool_Load_NilResolver(t *testing.T) {
	tt := NewToolsTool(nil, 5, 10)
	// No SetResolver called.
	r := execLoad(tt, context.Background(), []any{"create_agent"})
	if !r.IsError {
		t.Error("expected error result when resolver is nil")
	}
	if !strings.Contains(r.ForLLM, "resolver not set") {
		t.Errorf("expected 'resolver not set' in error message, got: %s", r.ForLLM)
	}
}

func TestToolsTool_Load_ValidSingleName(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := execLoad(tt, context.Background(), []any{"create_agent"})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", r.ForLLM)
	}
	m := parseLoadResult(t, r)
	ln := loadedNames(t, m)
	if len(ln) != 1 || ln[0] != "create_agent" {
		t.Errorf("expected loaded=[create_agent], got %v", ln)
	}
	sc := schemas(t, m)
	if _, ok := sc["create_agent"]; !ok {
		t.Error("schemas missing create_agent")
	}
	rej := rejectedNames(t, m)
	if len(rej) != 0 {
		t.Errorf("expected no rejections, got %v", rej)
	}
}

func TestToolsTool_Load_MultiName(t *testing.T) {
	avail := map[string]struct{}{
		"create_agent":     {},
		"browser_navigate": {},
		"list_agents":      {},
	}
	tt := newWiredToolsTool(avail)
	r := execLoad(tt, context.Background(), []any{"create_agent", "browser_navigate", "list_agents"})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", r.ForLLM)
	}
	m := parseLoadResult(t, r)
	ln := loadedNames(t, m)
	if len(ln) != 3 {
		t.Errorf("expected 3 loaded tools, got %d: %v", len(ln), ln)
	}
	sc := schemas(t, m)
	if len(sc) != 3 {
		t.Errorf("expected 3 schemas, got %d", len(sc))
	}
}

func TestToolsTool_Load_UnknownNameRejected(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := execLoad(tt, context.Background(), []any{"no_such_tool"})
	if !r.IsError {
		t.Error("expected error result for unknown tool name")
	}
	if !strings.Contains(r.ForLLM, "no_such_tool") {
		t.Errorf("expected rejected tool name in error message, got: %s", r.ForLLM)
	}
}

func TestToolsTool_Load_AllRejectedIsError(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{})
	r := execLoad(tt, context.Background(), []any{"ghost_tool", "phantom_tool"})
	if !r.IsError {
		t.Error("expected error when all names are rejected")
	}
}

func TestToolsTool_Load_PartialRejection(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := execLoad(tt, context.Background(), []any{"create_agent", "unknown_tool"})
	if r.IsError {
		t.Fatalf("expected success when at least one name loads, got error: %s", r.ForLLM)
	}
	m := parseLoadResult(t, r)
	ln := loadedNames(t, m)
	if len(ln) != 1 || ln[0] != "create_agent" {
		t.Errorf("expected loaded=[create_agent], got %v", ln)
	}
	rej := rejectedNames(t, m)
	found := false
	for _, s := range rej {
		if strings.Contains(s, "unknown_tool") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown_tool in rejected list, got %v", rej)
	}
}

func TestToolsTool_Load_Idempotent(t *testing.T) {
	// Re-loading an already-loaded tool should succeed (not error).
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	for i := range 3 {
		r := execLoad(tt, context.Background(), []any{"create_agent"})
		if r.IsError {
			t.Errorf("iteration %d: expected idempotent success, got error: %s", i, r.ForLLM)
		}
	}
}

func TestToolsTool_Load_MissingNamesParam(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	// Neither names nor query provided.
	r := tt.Execute(context.Background(), map[string]any{})
	if !r.IsError {
		t.Error("expected error when neither 'names' nor 'query' param is provided")
	}
}

func TestToolsTool_Load_EmptyNamesArray(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := execLoad(tt, context.Background(), []any{})
	if !r.IsError {
		t.Error("expected error for empty names array (falls through to query path, which is also empty)")
	}
}

func TestToolsTool_Load_InvalidNamesType(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := tt.Execute(context.Background(), map[string]any{
		"names": "not_an_array",
	})
	if !r.IsError {
		t.Error("expected error when names is a string instead of []string")
	}
}

func TestToolsTool_Load_SliceOfStringsDirect(t *testing.T) {
	// Some callers may pass []string (not []any) from typed decoding.
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := tt.Execute(context.Background(), map[string]any{
		"names": []string{"create_agent"},
	})
	if r.IsError {
		t.Fatalf("expected success for []string input, got error: %s", r.ForLLM)
	}
}

func TestToolsTool_Metadata(t *testing.T) {
	tt := NewToolsTool(nil, 5, 10)
	if tt.Name() != "ToolSearch" {
		t.Errorf("Name() = %q, want %q", tt.Name(), "ToolSearch")
	}
	if tt.Scope() != ScopeGeneral {
		t.Errorf("Scope() = %q, want ScopeGeneral", tt.Scope())
	}
	if tt.Category() != CategoryToolDiscovery {
		t.Errorf("Category() = %q, want CategoryToolDiscovery", tt.Category())
	}
	desc := tt.Description()
	if desc == "" {
		t.Error("Description() is empty")
	}
	// New description must mention both 'names' and 'query'.
	if !strings.Contains(desc, "names") {
		t.Errorf("Description() must mention 'names', got: %q", desc)
	}
	if !strings.Contains(desc, "query") {
		t.Errorf("Description() must mention 'query', got: %q", desc)
	}
	params := tt.Parameters()
	if params == nil {
		t.Fatal("Parameters() returned nil")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters() missing 'properties'")
	}
	// New schema: names and query only; no action, no mode.
	if _, ok := props["names"]; !ok {
		t.Error("Parameters() missing 'names' property")
	}
	if _, ok := props["query"]; !ok {
		t.Error("Parameters() missing 'query' property")
	}
	if _, ok := props["action"]; ok {
		t.Error("Parameters() must NOT contain 'action' property (removed)")
	}
	if _, ok := props["mode"]; ok {
		t.Error("Parameters() must NOT contain 'mode' property (removed)")
	}
	// New schema: no required fields (both names and query are optional at schema level).
	req, _ := params["required"].([]string)
	for _, r := range req {
		if r == "action" {
			t.Error("'action' must not be in required list (removed)")
		}
	}
}

// TestToolsTool_NameIsRenamed pins ADR-071 D1 / spec FR-010 (W-D1 test 9):
// the tool's identity answers to its new name, and the retired name is gone.
// Kept as its own dedicated test — distinct from TestToolsTool_Metadata,
// which exercises Name() only incidentally alongside Scope/Category/schema —
// so a future revert of ToolsTool.Name() is caught by a test whose whole
// point is the rename, not one that would keep passing after a
// find-and-replace on an unrelated assertion.
func TestToolsTool_NameIsRenamed(t *testing.T) {
	tt := NewToolsTool(nil, 5, 10)
	if got := tt.Name(); got != "ToolSearch" {
		t.Errorf("Name() = %q, want %q", got, "ToolSearch")
	}
	if tt.Name() == "load_tool" {
		t.Error("Name() must not answer to the retired \"load_tool\" name")
	}
}

func TestToolsTool_TierIsInfra(t *testing.T) {
	if ToolManifestTier("ToolSearch") != ManifestInfra {
		t.Error("'ToolSearch' must have ManifestInfra tier")
	}
}

func TestToolsTool_NotInManifest(t *testing.T) {
	// A manifest built with the 'ToolSearch' infra tool present should not list it as an entry.
	toolList := []Tool{
		NewToolsTool(nil, 5, 10),
		&fakeManifestTool{name: "create_agent", desc: "Create.", cat: CategoryAgents},
	}
	got := BuildCompressedManifest(toolList, nil)
	if strings.Contains(got, "  - ToolSearch") {
		t.Error("BuildCompressedManifest must NOT include 'ToolSearch' (infra) as a manifest entry")
	}
}

func TestToolsTool_NeitherNamesNorQuery_Error(t *testing.T) {
	tt := NewToolsTool(nil, 5, 10)
	r := tt.Execute(context.Background(), map[string]any{})
	if !r.IsError {
		t.Error("expected error when neither names nor query provided")
	}
}

// TestToolsTool_Load_PromotesHiddenTool proves that tools{names:[...]} promotes a
// hidden-registry tool (RegisterHidden) so it becomes Get-able via PromoteTools,
// AND records it in the session loaded-set via markLoaded.
func TestToolsTool_Load_PromotesHiddenTool(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_stub_hidden", desc: "hidden MCP stub"})

	// Before load: not Get-able (TTL=0).
	_, ok := reg.Get("mcp_stub_hidden")
	if ok {
		t.Fatal("hidden tool must not be Get-able before load")
	}

	// Wire canLoad to accept it and markLoaded to return a stub schema.
	var markedLoaded []string
	tt := NewToolsTool(reg, 5, 10)
	tt.SetResolver(
		func(_ context.Context, name string) (bool, string) {
			if name == "mcp_stub_hidden" {
				return true, ""
			}
			return false, name + " — not available in test"
		},
		func(_ context.Context, names []string) (map[string]any, []string) {
			markedLoaded = append(markedLoaded, names...)
			schemas := make(map[string]any, len(names))
			for _, n := range names {
				schemas[n] = map[string]any{"name": n}
			}
			return schemas, nil
		},
	)

	r := execLoad(tt, context.Background(), []any{"mcp_stub_hidden"})
	if r.IsError {
		t.Fatalf("tools(load) failed for hidden tool: %s", r.ForLLM)
	}

	// After load: must be Get-able (promoted by PromoteTools).
	_, ok = reg.Get("mcp_stub_hidden")
	if !ok {
		t.Error("hidden tool must be Get-able after tools{names:[...]} (PromoteTools called)")
	}

	// markLoaded closure must have been called.
	found := false
	for _, n := range markedLoaded {
		if n == "mcp_stub_hidden" {
			found = true
		}
	}
	if !found {
		t.Error("markLoaded was not called for mcp_stub_hidden")
	}
}

// TestToolsTool_Query_DoesNotPromoteWithoutResolver proves that tools{query:...}
// without a resolver is read-only: it finds tools by keyword but does NOT
// call PromoteTools.
func TestToolsTool_Query_DoesNotPromoteWithoutResolver(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_findme2", desc: "find me with query"})

	tt := NewToolsTool(reg, 5, 10)
	// No resolver needed for search without auto-load.

	r := tt.Execute(context.Background(), map[string]any{
		"query": "find me",
	})
	if r.IsError {
		t.Fatalf("tools(query) failed: %s", r.ForLLM)
	}
	if !strings.Contains(r.ForLLM, "mcp_findme2") {
		t.Errorf("tools(query) must return the matching tool name; got: %s", r.ForLLM)
	}

	// Tool must NOT be promoted (TTL must still be 0 after search).
	reg.mu.RLock()
	entry := reg.tools["mcp_findme2"]
	reg.mu.RUnlock()
	if entry != nil && entry.TTL != 0 {
		t.Errorf("tools(query) without resolver must NOT promote (TTL must stay 0); got TTL=%d", entry.TTL)
	}

	// Tool must NOT be Get-able (not promoted).
	_, ok := reg.Get("mcp_findme2")
	if ok {
		t.Error("tools(query) without resolver must NOT make the tool Get-able (no promote)")
	}

	// The response must NOT say UNLOCKED (old behavior).
	if strings.Contains(r.ForLLM, "UNLOCKED") {
		t.Error("tools(query) response must NOT say 'UNLOCKED' (that was the old promote-on-search behavior)")
	}
}

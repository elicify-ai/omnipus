// Omnipus — tools(action=load) tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the load action of the unified ToolsTool. Replaces the former
// load_tool_test.go — all behavioral assertions are preserved.

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeResolver builds a canLoad + markLoaded pair for testing.
// available is the set of names that canLoad returns true for.
// markLoaded always returns a stub schema for each accepted name.
func fakeResolver(available map[string]struct{}) (
	canLoad func(ctx context.Context, name string) bool,
	markLoaded func(ctx context.Context, names []string) (map[string]any, []string),
) {
	canLoad = func(_ context.Context, name string) bool {
		_, ok := available[name]
		return ok
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
	return
}

// newWiredToolsTool constructs a ToolsTool with resolver wired (no real registry).
func newWiredToolsTool(available map[string]struct{}) *ToolsTool {
	tt := NewToolsTool(nil, 5, 10)
	cl, ml := fakeResolver(available)
	tt.SetResolver(cl, ml)
	return tt
}

// execLoad calls tools{action:load,names:names} on tt.
func execLoad(tt *ToolsTool, ctx context.Context, names []any) *ToolResult {
	return tt.Execute(ctx, map[string]any{"action": "load", "names": names})
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
		names[i] = v.(string)
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
		names[i] = v.(string)
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
	r := tt.Execute(context.Background(), map[string]any{"action": "load"})
	if !r.IsError {
		t.Error("expected error when 'names' param is missing")
	}
}

func TestToolsTool_Load_EmptyNamesArray(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := execLoad(tt, context.Background(), []any{})
	if !r.IsError {
		t.Error("expected error for empty names array")
	}
}

func TestToolsTool_Load_InvalidNamesType(t *testing.T) {
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := tt.Execute(context.Background(), map[string]any{
		"action": "load",
		"names":  "not_an_array",
	})
	if !r.IsError {
		t.Error("expected error when names is a string instead of []string")
	}
}

func TestToolsTool_Load_SliceOfStringsDirect(t *testing.T) {
	// Some callers may pass []string (not []any) from typed decoding.
	tt := newWiredToolsTool(map[string]struct{}{"create_agent": {}})
	r := tt.Execute(context.Background(), map[string]any{
		"action": "load",
		"names":  []string{"create_agent"},
	})
	if r.IsError {
		t.Fatalf("expected success for []string input, got error: %s", r.ForLLM)
	}
}

func TestToolsTool_Metadata(t *testing.T) {
	tt := NewToolsTool(nil, 5, 10)
	if tt.Name() != "tools" {
		t.Errorf("Name() = %q, want %q", tt.Name(), "tools")
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
	if !strings.Contains(desc, "search") || !strings.Contains(desc, "load") {
		t.Errorf("Description() must mention both 'search' and 'load', got: %q", desc)
	}
	params := tt.Parameters()
	if params == nil {
		t.Fatal("Parameters() returned nil")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters() missing 'properties'")
	}
	if _, ok := props["action"]; !ok {
		t.Error("Parameters() missing 'action' property")
	}
	if _, ok := props["names"]; !ok {
		t.Error("Parameters() missing 'names' property")
	}
	if _, ok := props["query"]; !ok {
		t.Error("Parameters() missing 'query' property")
	}
	if _, ok := props["mode"]; !ok {
		t.Error("Parameters() missing 'mode' property")
	}
	req, _ := params["required"].([]string)
	found := false
	for _, r := range req {
		if r == "action" {
			found = true
		}
	}
	if !found {
		t.Error("'action' not in required list")
	}
}

func TestToolsTool_TierIsInfra(t *testing.T) {
	if ToolManifestTier("tools") != ManifestInfra {
		t.Error("'tools' must have ManifestInfra tier")
	}
}

func TestToolsTool_NotInManifest(t *testing.T) {
	// A manifest built with the 'tools' infra tool present should not list it as an entry.
	// The manifest header prose now says "action='load'" instead of mentioning "tools" by name.
	toolList := []Tool{
		NewToolsTool(nil, 5, 10),
		&fakeManifestTool{name: "create_agent", desc: "Create.", cat: CategoryAgents},
	}
	got := BuildCompressedManifest(toolList, nil)
	if strings.Contains(got, "  - tools") {
		t.Error("BuildCompressedManifest must NOT include 'tools' (infra) as a manifest entry")
	}
}

func TestToolsTool_UnknownAction(t *testing.T) {
	tt := NewToolsTool(nil, 5, 10)
	r := tt.Execute(context.Background(), map[string]any{"action": "explode"})
	if !r.IsError {
		t.Error("expected error for unknown action")
	}
	if !strings.Contains(r.ForLLM, "unknown action") {
		t.Errorf("expected 'unknown action' in error, got: %s", r.ForLLM)
	}
}

func TestToolsTool_MissingAction(t *testing.T) {
	tt := NewToolsTool(nil, 5, 10)
	r := tt.Execute(context.Background(), map[string]any{})
	if !r.IsError {
		t.Error("expected error when action is missing")
	}
}

// TestToolsTool_Load_PromotesHiddenTool proves that action=load promotes a
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
		func(_ context.Context, name string) bool {
			return name == "mcp_stub_hidden"
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
		t.Error("hidden tool must be Get-able after tools(action=load) (PromoteTools called)")
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

// TestToolsTool_Search_DoesNotPromote proves that action=search is read-only:
// it finds tools by keyword but does NOT call PromoteTools on the results.
func TestToolsTool_Search_DoesNotPromote(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{name: "mcp_findme", desc: "find me with search"})

	tt := NewToolsTool(reg, 5, 10)
	// No resolver needed for search.

	r := tt.Execute(context.Background(), map[string]any{
		"action": "search",
		"query":  "find me",
	})
	if r.IsError {
		t.Fatalf("tools(search) failed: %s", r.ForLLM)
	}
	if !strings.Contains(r.ForLLM, "mcp_findme") {
		t.Errorf("tools(search) must return the matching tool name; got: %s", r.ForLLM)
	}

	// Tool must NOT be promoted (TTL must still be 0 after search).
	reg.mu.RLock()
	entry := reg.tools["mcp_findme"]
	reg.mu.RUnlock()
	if entry != nil && entry.TTL != 0 {
		t.Errorf("tools(search) must NOT promote (TTL must stay 0); got TTL=%d", entry.TTL)
	}

	// Tool must NOT be Get-able (not promoted).
	_, ok := reg.Get("mcp_findme")
	if ok {
		t.Error("tools(search) must NOT make the tool Get-able (no promote)")
	}

	// The response must tell the model to call load, not announce unlocked tools.
	if strings.Contains(r.ForLLM, "UNLOCKED") {
		t.Error("tools(search) response must NOT say 'UNLOCKED' (that was the old promote-on-search behavior)")
	}
	if !strings.Contains(r.ForLLM, "action='load'") {
		t.Errorf("tools(search) response must tell model to use action='load'; got: %s", r.ForLLM)
	}
}

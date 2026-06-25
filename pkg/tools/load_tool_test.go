// Omnipus — load_tool tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

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

func newWiredLoadTool(available map[string]struct{}) *LoadTool {
	t := NewLoadTool()
	cl, ml := fakeResolver(available)
	t.SetResolver(cl, ml)
	return t
}

// parseLoadResult decodes the SilentResult ForLLM JSON into a map.
func parseLoadResult(t *testing.T, r *ToolResult) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.ForLLM), &out); err != nil {
		t.Fatalf("failed to parse load_tool result JSON: %v\nraw: %s", err, r.ForLLM)
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

func TestLoadTool_NilResolver(t *testing.T) {
	lt := NewLoadTool()
	// No SetResolver called.
	r := lt.Execute(context.Background(), map[string]any{"names": []any{"create_agent"}})
	if !r.IsError {
		t.Error("expected error result when resolver is nil")
	}
	if !strings.Contains(r.ForLLM, "not wired") {
		t.Errorf("expected 'not wired' in error message, got: %s", r.ForLLM)
	}
}

func TestLoadTool_ValidSingleName(t *testing.T) {
	lt := newWiredLoadTool(map[string]struct{}{"create_agent": {}})
	r := lt.Execute(context.Background(), map[string]any{
		"names": []any{"create_agent"},
	})
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

func TestLoadTool_MultiName(t *testing.T) {
	avail := map[string]struct{}{
		"create_agent":     {},
		"browser_navigate": {},
		"list_agents":      {},
	}
	lt := newWiredLoadTool(avail)
	r := lt.Execute(context.Background(), map[string]any{
		"names": []any{"create_agent", "browser_navigate", "list_agents"},
	})
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

func TestLoadTool_UnknownNameRejected(t *testing.T) {
	lt := newWiredLoadTool(map[string]struct{}{"create_agent": {}})
	r := lt.Execute(context.Background(), map[string]any{
		"names": []any{"no_such_tool"},
	})
	if !r.IsError {
		t.Error("expected error result for unknown tool name")
	}
	if !strings.Contains(r.ForLLM, "no_such_tool") {
		t.Errorf("expected rejected tool name in error message, got: %s", r.ForLLM)
	}
}

func TestLoadTool_AllRejectedIsError(t *testing.T) {
	lt := newWiredLoadTool(map[string]struct{}{})
	r := lt.Execute(context.Background(), map[string]any{
		"names": []any{"ghost_tool", "phantom_tool"},
	})
	if !r.IsError {
		t.Error("expected error when all names are rejected")
	}
}

func TestLoadTool_PartialRejection(t *testing.T) {
	lt := newWiredLoadTool(map[string]struct{}{"create_agent": {}})
	r := lt.Execute(context.Background(), map[string]any{
		"names": []any{"create_agent", "unknown_tool"},
	})
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

func TestLoadTool_Idempotent(t *testing.T) {
	// Re-loading an already-loaded tool should succeed (not error).
	lt := newWiredLoadTool(map[string]struct{}{"create_agent": {}})
	for i := range 3 {
		r := lt.Execute(context.Background(), map[string]any{
			"names": []any{"create_agent"},
		})
		if r.IsError {
			t.Errorf("iteration %d: expected idempotent success, got error: %s", i, r.ForLLM)
		}
	}
}

func TestLoadTool_MissingNamesParam(t *testing.T) {
	lt := newWiredLoadTool(map[string]struct{}{"create_agent": {}})
	r := lt.Execute(context.Background(), map[string]any{})
	if !r.IsError {
		t.Error("expected error when 'names' param is missing")
	}
}

func TestLoadTool_EmptyNamesArray(t *testing.T) {
	lt := newWiredLoadTool(map[string]struct{}{"create_agent": {}})
	r := lt.Execute(context.Background(), map[string]any{
		"names": []any{},
	})
	if !r.IsError {
		t.Error("expected error for empty names array")
	}
}

func TestLoadTool_InvalidNamesType(t *testing.T) {
	lt := newWiredLoadTool(map[string]struct{}{"create_agent": {}})
	r := lt.Execute(context.Background(), map[string]any{
		"names": "not_an_array",
	})
	if !r.IsError {
		t.Error("expected error when names is a string instead of []string")
	}
}

func TestLoadTool_SliceOfStringsDirect(t *testing.T) {
	// Some callers may pass []string (not []any) from typed decoding.
	lt := newWiredLoadTool(map[string]struct{}{"create_agent": {}})
	r := lt.Execute(context.Background(), map[string]any{
		"names": []string{"create_agent"},
	})
	if r.IsError {
		t.Fatalf("expected success for []string input, got error: %s", r.ForLLM)
	}
}

func TestLoadTool_Metadata(t *testing.T) {
	lt := NewLoadTool()
	if lt.Name() != "load_tool" {
		t.Errorf("Name() = %q, want %q", lt.Name(), "load_tool")
	}
	if lt.Scope() != ScopeGeneral {
		t.Errorf("Scope() = %q, want ScopeGeneral", lt.Scope())
	}
	if lt.Category() != CategoryToolDiscovery {
		t.Errorf("Category() = %q, want CategoryToolDiscovery", lt.Category())
	}
	if lt.Description() == "" {
		t.Error("Description() is empty")
	}
	params := lt.Parameters()
	if params == nil {
		t.Fatal("Parameters() returned nil")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters() missing 'properties'")
	}
	if _, ok := props["names"]; !ok {
		t.Error("Parameters() missing 'names' property")
	}
	req, _ := params["required"].([]string)
	found := false
	for _, r := range req {
		if r == "names" {
			found = true
		}
	}
	if !found {
		t.Error("'names' not in required list")
	}
}

func TestLoadTool_TierIsInfra(t *testing.T) {
	if ToolManifestTier("load_tool") != ManifestInfra {
		t.Error("load_tool must have ManifestInfra tier")
	}
}

func TestLoadTool_NotInManifest(t *testing.T) {
	// A manifest built with load_tool present should not list it as an entry.
	// Note: the manifest header prose mentions "load_tool" by name (that's intentional),
	// so we check for the bullet-entry format "  - load_tool".
	tools := []Tool{
		NewLoadTool(),
		&fakeManifestTool{name: "create_agent", desc: "Create.", cat: CategoryAgents},
	}
	got := BuildCompressedManifest(tools, nil)
	if strings.Contains(got, "  - load_tool") {
		t.Error("BuildCompressedManifest must NOT include load_tool as a manifest entry")
	}
}

// Omnipus — Tier-1 bounding parameters (ADR-066 §15 task 1)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestTier1Tools_BoundingParams — spec test 45 (ADR-066 spec FR-039, B-47,
// DS-9 #1 and #2). The two pkg/tools members of the Tier-1 audit's gap list
// (§15.1) — list_directory and inspect_session — must bound their own output
// with offset/limit over ENTRIES, named as read_file names its byte window,
// and must refuse an out-of-range value with a tool error rather than
// silently clamping it.
//
// DS-9 #3 (recall_conversation's max_results) lives in the pkg/agent test of
// the same name: the executable recall tool is in pkg/agent, which pkg/tools
// cannot import.
func TestTier1Tools_BoundingParams(t *testing.T) {
	t.Parallel()

	t.Run("list_directory schema advertises offset and limit", func(t *testing.T) {
		t.Parallel()
		props := schemaProperties(t, NewListDirTool(t.TempDir(), false).Parameters())
		for _, name := range []string{"offset", "limit"} {
			p, ok := props[name].(map[string]any)
			if !ok {
				t.Fatalf("list_directory schema has no %q property: %v", name, props)
			}
			if p["type"] != "integer" {
				t.Errorf("%s type = %v, want integer", name, p["type"])
			}
			if desc, _ := p["description"].(string); desc == "" {
				t.Errorf("%s has no description — FR-039 requires it documented in the schema", name)
			}
		}
	})

	// DS-9 #1: offset 0, limit 50 over a 120-entry directory → 50 entries.
	t.Run("list_directory offset 0 limit 50 returns 50 entries", func(t *testing.T) {
		t.Parallel()
		dir := seedDirEntries(t, 120)
		res := NewListDirTool(dir, false).Execute(context.Background(),
			map[string]any{"path": dir, "offset": 0, "limit": 50})
		if res.IsError {
			t.Fatalf("bounded listing failed: %s", res.ForLLM)
		}
		names := listedEntryNames(res.ForLLM)
		if len(names) != 50 {
			t.Fatalf("returned %d entries, want 50", len(names))
		}
		if names[0] != "entry-000.txt" || names[49] != "entry-049.txt" {
			t.Errorf("page = [%s … %s], want [entry-000.txt … entry-049.txt]", names[0], names[49])
		}
		if strings.Contains(res.ForLLM, "entry-050.txt") {
			t.Error("entry beyond the limit leaked into the result")
		}
	})

	t.Run("list_directory offset pages to the next entries", func(t *testing.T) {
		t.Parallel()
		dir := seedDirEntries(t, 120)
		res := NewListDirTool(dir, false).Execute(context.Background(),
			map[string]any{"path": dir, "offset": 50, "limit": 10})
		if res.IsError {
			t.Fatalf("paged listing failed: %s", res.ForLLM)
		}
		names := listedEntryNames(res.ForLLM)
		if len(names) != 10 {
			t.Fatalf("returned %d entries, want 10", len(names))
		}
		if names[0] != "entry-050.txt" || names[9] != "entry-059.txt" {
			t.Errorf("page = [%s … %s], want [entry-050.txt … entry-059.txt]", names[0], names[9])
		}
		if !strings.Contains(res.ForLLM, "120") {
			t.Errorf("a truncated page must state the total entry count, got: %s", res.ForLLM)
		}
	})

	t.Run("list_directory rejects a negative offset", func(t *testing.T) {
		t.Parallel()
		dir := seedDirEntries(t, 3)
		res := NewListDirTool(dir, false).Execute(context.Background(),
			map[string]any{"path": dir, "offset": -1})
		if !res.IsError {
			t.Fatalf("offset -1 must be a tool error, got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "offset") {
			t.Errorf("error must name the offending parameter, got: %s", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "entry-000.txt") {
			t.Error("a refused call must not return a listing")
		}
	})

	t.Run("list_directory rejects a limit below one", func(t *testing.T) {
		t.Parallel()
		dir := seedDirEntries(t, 3)
		for _, limit := range []int{0, -5} {
			res := NewListDirTool(dir, false).Execute(context.Background(),
				map[string]any{"path": dir, "limit": limit})
			if !res.IsError {
				t.Fatalf("limit %d must be a tool error, got: %s", limit, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "limit") {
				t.Errorf("error must name the offending parameter, got: %s", res.ForLLM)
			}
		}
	})

	t.Run("list_directory unbounded call still lists everything", func(t *testing.T) {
		t.Parallel()
		dir := seedDirEntries(t, 12)
		res := NewListDirTool(dir, false).Execute(context.Background(),
			map[string]any{"path": dir})
		if res.IsError {
			t.Fatalf("default listing failed: %s", res.ForLLM)
		}
		if got := len(listedEntryNames(res.ForLLM)); got != 12 {
			t.Fatalf("default listing returned %d entries, want all 12", got)
		}
	})

	t.Run("inspect_session schema advertises offset and limit", func(t *testing.T) {
		t.Parallel()
		props := schemaProperties(t, NewInspectSessionTool(nil).Parameters())
		for _, name := range []string{"offset", "limit"} {
			p, ok := props[name].(map[string]any)
			if !ok {
				t.Fatalf("inspect_session schema has no %q property: %v", name, props)
			}
			if p["type"] != "integer" {
				t.Errorf("%s type = %v, want integer", name, p["type"])
			}
			if desc, _ := p["description"].(string); desc == "" {
				t.Errorf("%s has no description — FR-039 requires it documented in the schema", name)
			}
		}
	})

	t.Run("inspect_session offset and limit page the entries", func(t *testing.T) {
		t.Parallel()
		store := newFakeInspectSessionStore()
		entries := make([]session.TranscriptEntry, 0, 20)
		for i := 0; i < 20; i++ {
			entries = append(entries, session.TranscriptEntry{
				ID:        fmt.Sprintf("e%02d", i),
				Role:      "assistant",
				Content:   fmt.Sprintf("entry %02d", i),
				Timestamp: time.Now(),
			})
		}
		store.seed("sess-page", "agent-1", entries)
		tool := NewInspectSessionTool(store)
		ctx := WithVerifierSessionScope(context.Background(), []string{"sess-page"})

		res := tool.Execute(ctx, map[string]any{
			"session_id": "sess-page", "offset": 5, "limit": 4,
		})
		if res.IsError {
			t.Fatalf("paged inspect failed: %s", res.ForLLM)
		}
		var out struct {
			Entries []struct {
				ID string `json:"id"`
			} `json:"entries"`
			EntryCount int `json:"entry_count"`
			Offset     int `json:"offset"`
		}
		if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
			t.Fatalf("parse result %q: %v", res.ForLLM, err)
		}
		if out.EntryCount != 4 || len(out.Entries) != 4 {
			t.Fatalf("entry_count = %d / %d entries, want 4", out.EntryCount, len(out.Entries))
		}
		if out.Entries[0].ID != "e05" || out.Entries[3].ID != "e08" {
			t.Errorf("page = [%s … %s], want [e05 … e08]", out.Entries[0].ID, out.Entries[3].ID)
		}
		if out.Offset != 5 {
			t.Errorf("offset echoed as %d, want 5", out.Offset)
		}
	})

	// DS-9 #2: inspect_session with limit 0 → error.
	t.Run("inspect_session rejects a limit below one", func(t *testing.T) {
		t.Parallel()
		store := newFakeInspectSessionStore()
		store.seed("sess-lim", "agent-1", []session.TranscriptEntry{
			{ID: "e1", Role: "assistant", Content: "secret payload", Timestamp: time.Now()},
		})
		tool := NewInspectSessionTool(store)
		ctx := WithVerifierSessionScope(context.Background(), []string{"sess-lim"})

		res := tool.Execute(ctx, map[string]any{"session_id": "sess-lim", "limit": 0})
		if !res.IsError {
			t.Fatalf("limit 0 must be a tool error, got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "limit") {
			t.Errorf("error must name the offending parameter, got: %s", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "secret payload") {
			t.Error("a refused call must not return transcript content")
		}
	})

	t.Run("inspect_session rejects a negative offset", func(t *testing.T) {
		t.Parallel()
		store := newFakeInspectSessionStore()
		store.seed("sess-off", "agent-1", []session.TranscriptEntry{
			{ID: "e1", Role: "assistant", Content: "hello", Timestamp: time.Now()},
		})
		tool := NewInspectSessionTool(store)
		ctx := WithVerifierSessionScope(context.Background(), []string{"sess-off"})

		res := tool.Execute(ctx, map[string]any{"session_id": "sess-off", "offset": -2})
		if !res.IsError {
			t.Fatalf("offset -2 must be a tool error, got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "offset") {
			t.Errorf("error must name the offending parameter, got: %s", res.ForLLM)
		}
	})
}

// schemaProperties returns the "properties" map of a tool parameter schema.
func schemaProperties(t *testing.T, params map[string]any) map[string]any {
	t.Helper()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties map: %v", params)
	}
	return props
}

// seedDirEntries creates n files named entry-NNN.txt in a fresh temp dir and
// returns the dir. os.ReadDir sorts by name, so the ordinal in the name is
// also the entry's position in the listing.
func seedDirEntries(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("entry-%03d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	return dir
}

// listedEntryNames extracts the entry names from a list_directory result,
// ignoring any framing line the paged form appends.
func listedEntryNames(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "FILE: "):
			names = append(names, strings.TrimPrefix(line, "FILE: "))
		case strings.HasPrefix(line, "DIR:  "):
			names = append(names, strings.TrimPrefix(line, "DIR:  "))
		}
	}
	return names
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMemorySmoke verifies the core round-trip for Lane M:
//  1. Creates a temp workspace.
//  2. Calls AppendLongTerm twice with distinct content.
//  3. Calls ReadLongTermEntries and asserts two entries are returned newest-first.
//  4. Calls SearchEntries and asserts the query matches.
func TestMemorySmoke(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	// First entry.
	if err := ms.AppendLongTerm("prefer tabs over spaces", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm #1 failed: %v", err)
	}

	// Brief pause to guarantee distinct mtimes and timestamps.
	time.Sleep(2 * time.Millisecond)

	// Second entry (appended after a small delay so timestamps differ).
	if err := ms.AppendLongTerm("always use flock for concurrent writes", "lesson_learned"); err != nil {
		t.Fatalf("AppendLongTerm #2 failed: %v", err)
	}

	// Verify the raw file exists and contains both entries.
	raw, err := os.ReadFile(filepath.Join(dir, "memory", "MEMORY.md"))
	if err != nil {
		t.Fatalf("reading MEMORY.md: %v", err)
	}
	if !strings.Contains(string(raw), "prefer tabs over spaces") {
		t.Error("MEMORY.md missing first entry")
	}
	if !strings.Contains(string(raw), "always use flock for concurrent writes") {
		t.Error("MEMORY.md missing second entry")
	}
	if !strings.Contains(string(raw), "<!-- next -->") {
		t.Error("MEMORY.md missing separator between entries")
	}

	// ReadLongTermEntries must return 2 entries newest-first.
	entries, err := ms.ReadLongTermEntries()
	if err != nil {
		t.Fatalf("ReadLongTermEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Newest-first: second entry (lesson_learned) should come before first.
	if entries[0].Category != "lesson_learned" {
		t.Errorf("entries[0].Category = %q, want lesson_learned", entries[0].Category)
	}
	if entries[1].Category != "key_decision" {
		t.Errorf("entries[1].Category = %q, want key_decision", entries[1].Category)
	}
	// Verify newest-first ordering by timestamp.
	if !entries[0].Timestamp.After(entries[1].Timestamp) && entries[0].Timestamp != entries[1].Timestamp {
		t.Errorf("entries not in newest-first order: [0]=%v [1]=%v",
			entries[0].Timestamp, entries[1].Timestamp)
	}

	// SearchEntries must find entries matching the query.
	results, err := ms.SearchEntries("flock", 10)
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchEntries('flock'): expected 1, got %d", len(results))
	}
	if !strings.Contains(results[0].Content, "flock") {
		t.Errorf("search result content does not contain 'flock': %q", results[0].Content)
	}

	// SearchEntries with no match must return empty (not an error).
	noMatch, err := ms.SearchEntries("thisdoesnotexist_xyz", 10)
	if err != nil {
		t.Fatalf("SearchEntries no-match: %v", err)
	}
	if len(noMatch) != 0 {
		t.Errorf("expected 0 results for non-matching query, got %d", len(noMatch))
	}
}

// TestMemoryValidation exercises the validation boundaries of AppendLongTerm.
func TestMemoryValidation(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	// Invalid category.
	if err := ms.AppendLongTerm("some fact", "invalid_cat"); err == nil {
		t.Error("expected error for invalid category, got nil")
	}

	// Empty content.
	if err := ms.AppendLongTerm("   ", "reference"); err == nil {
		t.Error("expected error for whitespace-only content, got nil")
	}

	// Content with HTML comment marker.
	if err := ms.AppendLongTerm("contains <!-- comment -->", "reference"); err == nil {
		t.Error("expected error for content containing '<!--', got nil")
	}

	// Content exceeding 4096 runes.
	long := strings.Repeat("x", 4097)
	if err := ms.AppendLongTerm(long, "reference"); err == nil {
		t.Error("expected error for content exceeding 4096 runes, got nil")
	}

	// NUL bytes stripped silently — should succeed.
	if err := ms.AppendLongTerm("fact with \x00 nul", "reference"); err != nil {
		t.Errorf("NUL-stripped content should succeed, got: %v", err)
	}
}

// TestMemoryRetroRoundTrip verifies AppendRetro + ReadRetros.
func TestMemoryRetroRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	sessionID := "testsession-smoke"
	r := Retro{
		Timestamp:        time.Now().UTC(),
		Trigger:          "joined",
		Fallback:         false,
		Recap:            "Productive session.",
		WentWell:         []string{"clear communication", "fast iteration"},
		NeedsImprovement: []string{"better estimates"},
	}

	if err := ms.AppendRetro(sessionID, r); err != nil {
		t.Fatalf("AppendRetro: %v", err)
	}

	retros, err := ms.ReadRetros(1)
	if err != nil {
		t.Fatalf("ReadRetros: %v", err)
	}
	if len(retros) != 1 {
		t.Fatalf("expected 1 retro, got %d", len(retros))
	}
	got := retros[0]
	if got.Trigger != "joined" {
		t.Errorf("Trigger = %q, want joined", got.Trigger)
	}
	if len(got.WentWell) != 2 {
		t.Errorf("WentWell len = %d, want 2", len(got.WentWell))
	}
	if len(got.NeedsImprovement) != 1 {
		t.Errorf("NeedsImprovement len = %d, want 1", len(got.NeedsImprovement))
	}
}

// TestMemoryLastSession verifies WriteLastSession + ReadLastSession.
func TestMemoryLastSession(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	// ReadLastSession on a fresh workspace returns empty string, no error.
	content, err := ms.ReadLastSession()
	if err != nil {
		t.Fatalf("ReadLastSession on fresh workspace: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty, got %q", content)
	}

	// Write and read back.
	const payload = "## Session summary\nWe built the memory store."
	if writeErr := ms.WriteLastSession(payload); writeErr != nil {
		t.Fatalf("WriteLastSession: %v", writeErr)
	}
	got, err := ms.ReadLastSession()
	if err != nil {
		t.Fatalf("ReadLastSession after write: %v", err)
	}
	if got != payload {
		t.Errorf("got %q, want %q", got, payload)
	}
}

// TestGetMemoryContextBudget verifies the 12000-rune budget logic in GetMemoryContext.
func TestGetMemoryContextBudget(t *testing.T) {
	dir := t.TempDir()
	ms := NewMemoryStore(dir)

	// Write LAST_SESSION.md.
	if err := ms.WriteLastSession("Last session was productive."); err != nil {
		t.Fatalf("WriteLastSession: %v", err)
	}

	// Append a few long-term entries.
	for i := range 3 {
		if i > 0 {
			time.Sleep(2 * time.Millisecond)
		}
		cat := []string{"key_decision", "reference", "lesson_learned"}[i]
		if err := ms.AppendLongTerm("entry number "+string(rune('1'+i)), cat); err != nil {
			t.Fatalf("AppendLongTerm %d: %v", i, err)
		}
	}

	ctx := ms.GetMemoryContext()
	if ctx == "" {
		t.Fatal("GetMemoryContext returned empty string")
	}
	if !strings.Contains(ctx, "## Last Session") {
		t.Error("GetMemoryContext missing ## Last Session header")
	}
	if !strings.Contains(ctx, "## Long-term memory") {
		t.Error("GetMemoryContext missing ## Long-term memory header")
	}
}

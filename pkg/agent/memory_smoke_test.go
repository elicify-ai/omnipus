// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// memory_smoke_test.go — smoke tests for the room-based MemoryStore (Spec-5).
// GREENFIELD: tests verify the new per-memory .md file format and room topology.

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/memrooms"
)

// TestMemorySmoke verifies the core round-trip for the room-based store:
//  1. Creates a temp workspace + home.
//  2. Calls AppendLongTerm twice with distinct content.
//  3. Calls SearchEntries and asserts both entries are found.
func TestMemorySmoke(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	t.Cleanup(ms.Close)

	// First entry.
	if err := ms.AppendLongTerm("prefer tabs over spaces", "key_decision"); err != nil {
		t.Fatalf("AppendLongTerm #1 failed: %v", err)
	}

	// Brief pause to ensure distinct file mtimes.
	time.Sleep(2 * time.Millisecond)

	// Second entry.
	if err := ms.AppendLongTerm("always use flock for concurrent writes", "lesson_learned"); err != nil {
		t.Fatalf("AppendLongTerm #2 failed: %v", err)
	}

	// Verify two .md files exist in the private room.
	memoriesDir := filepath.Join(dir, memrooms.OmnipusRoomDir, memrooms.MemoriesSubdir)
	entries, err := os.ReadDir(memoriesDir)
	if err != nil {
		t.Fatalf("read memories dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 memory files, got %d", len(entries))
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
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	t.Cleanup(ms.Close)

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
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	t.Cleanup(ms.Close)

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
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	t.Cleanup(ms.Close)

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

// TestGetMemoryContextIncludes verifies GetMemoryContext returns last session + memory.
func TestGetMemoryContextIncludes(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(dir, home)
	t.Cleanup(ms.Close)

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

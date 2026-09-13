// Omnipus — the coverage-caveat tests for the recovery path (Codex review
// 2026-09-14, finding 6): a store whose recovery could not read every file
// stays USABLE — partial results remain available — but the fact travels into
// every answer, which is complete:false and names the files.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenFindStore_UnreadableNoteYieldsUsableStoreAndCaveat — the exact case
// the finding names: an unreadable deal note must not disappear silently from
// a typed query whose answer reports complete:true. The store stays open, the
// readable notes are still findable, and the caveat names the unreadable file.
func TestOpenFindStore_UnreadableNoteYieldsUsableStoreAndCaveat(t *testing.T) {
	skipWithoutSQLite(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root: a 0000-mode file is still readable, so this test cannot make a note unreadable")
	}

	ctx := context.Background()
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
		"Plants/Orchid.md":                  orchidNote,
	})
	home := syncHome(t)

	// Make one note unreadable to the indexing pass. It still STATS (the walk
	// sees it, so it counts in every scan) but its content cannot be read, so
	// Sync reports it unreadable and it never gets a row.
	locked := filepath.Join(root, "Plants", "Orchid.md")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(locked, 0o600) }()

	store, closer, reason, caveat := openFindStore(ctx, home, root)
	defer func() { _ = closer() }()
	if store == nil {
		t.Fatalf("the store must stay USABLE when only one file is unreadable (reason=%q)", reason)
	}
	if reason != "" {
		t.Fatalf("a usable store must not also carry a refusal reason; got %q", reason)
	}
	if !strings.Contains(caveat, "Plants/Orchid.md") {
		t.Fatalf("the caveat must name the unreadable file; got %q", caveat)
	}
	if !strings.Contains(caveat, "not fully evaluated") {
		t.Fatalf("the caveat must distinguish 'every readable file is indexed' from 'the collection was fully evaluated'; got %q", caveat)
	}
}

// TestFindTool_UnreadableNoteMarksTheAnswerIncomplete — the caveat reaches the
// rendered knowledge_find answer: the query still returns the readable
// records, and the verdict is COMPLETE: no with the unreadable file named,
// never a confident COMPLETE: yes over a silently narrower corpus.
func TestFindTool_UnreadableNoteMarksTheAnswerIncomplete(t *testing.T) {
	skipWithoutSQLite(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root: a 0000-mode file is still readable, so this test cannot make a note unreadable")
	}

	root := syncVault(t, map[string]string{
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Plants/Fern.md":                    fernNote,
		"Plants/Orchid.md":                  orchidNote,
	})
	home := syncHome(t)

	locked := filepath.Join(root, "Plants", "Orchid.md")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(locked, 0o600) }()

	text := findViaTool(t, home, root, map[string]any{
		"type": "plant",
	})

	if !strings.Contains(text, "Fern") {
		t.Fatalf("the readable record must still be returned; got:\n%s", text)
	}
	if !strings.Contains(text, "COMPLETE: no") {
		t.Fatalf("an answer over a not-fully-evaluated collection must not claim completeness; got:\n%s", text)
	}
	if !strings.Contains(text, "Plants/Orchid.md") {
		t.Fatalf("the answer must name the file that cannot appear in it; got:\n%s", text)
	}
}

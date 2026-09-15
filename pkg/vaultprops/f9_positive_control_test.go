// Omnipus — positive control for the Populated() coverage fix
// (find_tool.go's findTextSearcher.Populated, Finding 2).
//
// TestF9Reopened_* (f9_regression_test.go) proves the fix REFUSES when it
// should. On its own that is satisfiable by a Populated() that always
// returns false — an "always refuse" regression that would be just as wrong
// as the bug it replaces, quietly making every genuinely complete zero-hit
// knowledge_find answer over a fully-synced vault into a false refusal. This
// file is the other half: a REAL Sync, over BOTH indexes, then a words=
// query for a term that is genuinely absent from the corpus, must answer a
// true "complete: true, 0 records" — not a refusal.
//
// Confirmed as a real mutation catch, not merely reasoned: with
// findTextSearcher.Populated hard-coded to `return false, nil`, this test
// fails while f9_regression_test.go's own test still passes — proving
// f9_regression_test.go alone does not pin this half of the contract.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestF9PositiveControl' ./pkg/vaultprops/
package vaultprops

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// TestF9PositiveControl_FullySyncedCollectionAnswersATrueZero builds a
// collection, runs a REAL full sync over both the text index
// (knowledge.Index.Sync) and the properties index (vaultprops.Sync) — the
// same two calls the boot sweep performs — and then asks knowledge_find for
// a term that appears in none of the notes. The answer must be a genuine,
// unrefused "complete: true, 0 records", proving Populated()'s new coverage
// check does not degenerate into refusing everything.
func TestF9PositiveControl_FullySyncedCollectionAnswersATrueZero(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"one.md":   "# One\n\nsomething about apples.\n",
		"two.md":   "# Two\n\nsomething about oranges.\n",
		"three.md": "# Three\n\nsomething about pears.\n",
	})

	ctx := context.Background()
	ix, err := knowledge.OpenIndex(home, root)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	if _, err := ix.Sync(ctx); err != nil {
		t.Fatalf("text index Sync: %v", err)
	}
	if err := ix.Close(); err != nil {
		t.Fatalf("closing text index: %v", err)
	}
	if _, err := Sync(ctx, home, root, SyncOptions{}); err != nil {
		t.Fatalf("properties Sync: %v", err)
	}

	const absentTerm = "quixomatic9nonexistent"
	got := findViaTool(t, home, root, map[string]any{"words": absentTerm})

	if strings.Contains(got, "REFUSED") {
		t.Fatalf("a fully-synced collection's genuine zero-hit query was refused instead of answered.\ngot: %s", got)
	}
	if !strings.Contains(got, "COMPLETE: yes") {
		t.Fatalf("expected a complete answer over a fully-synced collection.\ngot: %s", got)
	}
	if !strings.Contains(got, "0 records matched") {
		t.Fatalf("expected an honest zero-match answer.\ngot: %s", got)
	}
}

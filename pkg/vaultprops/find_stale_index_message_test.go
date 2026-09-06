// A2(d) end-to-end: through the REAL knowledge_find tool surface, a zero-hit
// words query over a vault whose text index is STALE (fully built, then a note
// added on disk without re-indexing) must be told the index is stale and by how
// much — not "never built" (a lie) and not a bare "0 records" (silent). This is
// the reachability proof for the freshness signal: the whole chain from
// findTextSearcher.IndexFreshness through checkTextIndexPopulated to the tool's
// rendered refusal.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

func TestKnowledgeFind_StaleTextIndex_ZeroHitWordsReportsStaleNotNeverBuilt(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"one.md":   "# One\n\nsomething about apples.\n",
		"two.md":   "# Two\n\nsomething about oranges.\n",
		"three.md": "# Three\n\nsomething about pears.\n",
	})

	ctx := context.Background()
	// A REAL full sync of both indexes — the boot-sweep pair — so the vault is
	// genuinely, provably indexed before it goes stale.
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

	// Now add a fourth note on disk and DO NOT re-index. The text index is
	// stale: it reflects 3 of the 4 files. A words query for a term only that
	// new note carries finds nothing in the (stale) index.
	if err := os.WriteFile(filepath.Join(root, "four.md"),
		[]byte("# Four\n\nan entirely new note mentioning zarquonfruit.\n"), 0o600); err != nil {
		t.Fatalf("write four.md: %v", err)
	}

	got := findViaTool(t, home, root, map[string]any{"words": "zarquonfruit"})

	if !strings.Contains(got, "REFUSED") {
		t.Fatalf("a zero-hit query over a STALE index was not refused; a partial/absent answer here is the exact silent failure A2(d) closes.\ngot: %s", got)
	}
	// The freshness signal A2(d) adds: concrete coverage — 3 of 4 indexed, 1
	// pending — is what lets a caller tell an index that is behind from content
	// that is genuinely absent (a FRESH index's zero never reaches this
	// refusal). The label is deliberately coverage, not "stale" vs "never
	// built": an instant-index write leaves a 1-entry manifest, so those two
	// states are not distinguishable from coverage and the message does not
	// pretend they are.
	if !strings.Contains(got, "4 files") || !strings.Contains(got, "3 of") {
		t.Fatalf("the refusal did not quote the coverage counts (3 of 4 files).\ngot: %s", got)
	}
	if !strings.Contains(got, "1 not yet indexed") {
		t.Fatalf("the refusal did not quote the pending count (1 not yet indexed).\ngot: %s", got)
	}
}

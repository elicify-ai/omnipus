// Omnipus — positive control for Finding B's fix
// (find_tool.go's openFindStore, propertiesStoreCoversCollection).
//
// TestF9B_TypedOnly_* (f9b_typed_only_reproduction_test.go) proves the fix
// REFUSES when it should. On its own that is satisfiable by a coverage check
// that always reports "not covered" — an "always refuse" regression that
// would be just as wrong as the bug it replaces, quietly making every
// genuinely complete zero-hit typed knowledge_find answer over a
// fully-synced vault into a false refusal. This file is the other half: a
// REAL Sync, over BOTH indexes, then a type= query (no words=) for a
// property value that is genuinely absent from the corpus, must answer a
// true "complete: true, 0 records" — not a refusal.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestF9BPositiveControl' ./pkg/vaultprops/
package vaultprops

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// TestF9BPositiveControl_FullySyncedCollectionAnswersATrueZeroForATypedQuery
// builds a collection with three "deal" notes, runs a REAL full sync over
// both the text index (knowledge.Index.Sync) and the properties index
// (vaultprops.Sync) — the same two calls the boot sweep performs — and then
// asks knowledge_find for type=deal / status=lost, a status none of the
// three notes declare. The answer must be a genuine, unrefused "complete:
// yes, 0 records", proving the coverage fix does not degenerate into
// refusing every typed query.
func TestF9BPositiveControl_FullySyncedCollectionAnswersATrueZeroForATypedQuery(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		".omnipus-vault/records/deal.yaml": "schema_version: 1\n" +
			"type: deal\n" +
			"properties:\n" +
			"  status: { type: enum, values: [prospect, won, lost] }\n",
		"one.md":   "---\ntype: deal\nstatus: prospect\n---\n# One\n",
		"two.md":   "---\ntype: deal\nstatus: won\n---\n# Two\n",
		"three.md": "---\ntype: deal\nstatus: prospect\n---\n# Three\n",
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

	got := findViaTool(t, home, root, map[string]any{
		"type": "deal",
		"filter": map[string]any{
			"property": "status",
			"op":       "=",
			"value":    "lost",
		},
	})

	if strings.Contains(got, "REFUSED") {
		t.Fatalf("a fully-synced collection's genuine zero-hit typed query was refused instead of answered.\ngot: %s", got)
	}
	if !strings.Contains(got, "COMPLETE: yes") {
		t.Fatalf("expected a complete answer over a fully-synced collection.\ngot: %s", got)
	}
	if !strings.Contains(got, "0 records matched") {
		t.Fatalf("expected an honest zero-match answer.\ngot: %s", got)
	}
}

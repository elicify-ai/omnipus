// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 59, 27 and 60.
//
// Both are BLOCKED per the qa-lead RED protocol: each depends on a mechanism
// that does not exist anywhere in the tree today, confirmed by reading the
// actual handlers (not merely by absence of a name match).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestDiscovery_ZeroWalksOnWarmCache$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryCopy_AutoRenamesCollidingViewName$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestDiscovery_CacheInvalidatedOnWrite$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

// TestDiscovery_ZeroWalksOnWarmCache is TDD Plan test 59 (R2-MAJ-006,
// FR-VA-027, SC-VA-014): a SECOND listing of the same folder (no intervening
// write) must perform ZERO WalkContained calls, verified by an instrumented
// counter against the real GET /api/v1/library/{ws}/entries handler
// (D-VIEW-INDEX's warm-cache latency budget).
//
// BLOCKED: annotateKnowledgeBaseEntries (pkg/gateway/rest_library.go) is the
// only per-entry annotation the entries-listing handler runs today, and it
// answers a DIFFERENT question (is_knowledge_base, via a cheap two-marker
// stat) — there is no is_view/view annotation step at all, and therefore no
// per-collection view-index cache and no WalkContained call in this path to
// instrument or count. `grep -rn "DiscoverViewFiles\|ViewIndex\|WalkContained" pkg/gateway`
// returns zero matches — the mechanism this test would measure does not
// exist yet.
func TestDiscovery_ZeroWalksOnWarmCache(t *testing.T) {
	t.Fatal("BLOCKED: no is_view/view annotation step, and no per-collection D-VIEW-INDEX cache, " +
		"exists in the GET /api/v1/library/{workspace_id}/entries handler " +
		"(pkg/gateway/rest_library.go::handleLibraryEntriesList/annotateKnowledgeBaseEntries) — " +
		"required before a second listing's WalkContained call count can be measured at all, per " +
		"FR-VA-027 / SC-VA-014 / TDD test 59.")
}

// TestLibraryCopy_AutoRenamesCollidingViewName is TDD Plan test 27 (FD-2,
// CRIT-002, US-9 AS-3, Dataset G-1): copying a view whose `name` collides
// with an existing view, through the Library's real copy action
// (POST /api/v1/library/copy -> library.CopyInto), must auto-suffix the
// copy's `name` before it lands, so the original and the copy both resolve
// independently — never a silent D-DEDUP switch-off of the copied-from view.
//
// BLOCKED: library.CopyInto (pkg/library/transfer.go) copies bytes verbatim
// with no per-file rewrite step of any kind — confirmed by reading its full
// body (copyFile/copyDirRecursive, no `.view`/`.base` special-casing
// anywhere) — and the planned shared rewrite helper
// (pkg/records::RewriteCopiedViewIdentity, spec §2 D-DUPLICATE) does not
// exist (`grep -rn "RewriteCopiedViewIdentity" pkg/` — zero matches).
// Separately, `.view` is not yet a recognized extension at all (absent from
// pkg/library/entries.go's extMimeTypes/textExtensions tables and from
// records.LoadViews's fixed-directory scan), so there is no "existing view"
// for a copy to collide with by NAME outside the legacy control directory.
func TestLibraryCopy_AutoRenamesCollidingViewName(t *testing.T) {
	t.Fatal("BLOCKED: library.CopyInto has no per-file view-identity rewrite step, and the planned " +
		"shared helper (pkg/records::RewriteCopiedViewIdentity, D-DUPLICATE) does not exist — " +
		"required before a Library copy of a name-colliding view can be shown to auto-suffix the " +
		"copy's name, per FR-VA-019 / Dataset G-1 / TDD test 27.")
}

// TestDiscovery_CacheInvalidatedOnWrite is TDD Plan test 60 (R2-MAJ-006,
// D-VIEW-INDEX): a `.view` write must invalidate only that path's cache
// entry; a listing immediately after must reflect the write without a
// whole-collection re-walk.
//
// BLOCKED: same root cause as test 59 — there is no D-VIEW-INDEX cache at
// all (confirmed: `grep -rn "DiscoverViewFiles\|ViewIndex\|WalkContained"
// pkg/gateway` returns zero matches), so there is nothing for a write to
// invalidate and nothing for a listing to read from yet.
func TestDiscovery_CacheInvalidatedOnWrite(t *testing.T) {
	t.Fatal("BLOCKED: no per-collection D-VIEW-INDEX cache exists anywhere in pkg/gateway — required " +
		"before a write's per-path invalidation, and a subsequent listing's cache-vs-rewalk behavior, " +
		"can be shown at all, per D-VIEW-INDEX / TDD test 60.")
}

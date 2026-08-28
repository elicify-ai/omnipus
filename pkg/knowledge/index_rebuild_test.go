// Tests for the forced rebuild of an index that must not be searched —
// ADR-068 D20 wave W0, and the spike's finding F-0 / S-2.
//
// Oracles come from the ADR and the spike, never from the implementation:
//
//	D20 W0   an index built under zapx v17.1.2 is REBUILT on upgrade rather
//	         than opened, and the search that panicked returns results
//	F-0      segments already written under the corrupting writer stay corrupt;
//	         pinning a fixed zapx repairs nothing already on disk
//	S-2 §4.1 bleve.OpenUsing takes no mapping argument, so an index opened with
//	         a changed mapping answers 0 hits with err=nil — the silent no-op
//	S-2 §4.2 two guards: G1 an index format version, G2 a mapping assertion
//	S-2 §4.3 row E — G1 alone does NOT catch a forgotten version bump; row F —
//	         G2 does
//	S-2 §4.4 a NAME-ONLY mapping comparison passes an analyzer change; the
//	         settings comparison is what makes G2 real
//	S-2 §4.3 the control: with nothing drifted, NEITHER guard may fire
//
// WHAT THESE TESTS DO NOT DO, stated plainly rather than implied: they do not
// reproduce the corrupt segments themselves. Reproducing F-0 needs
// zapx v17.1.2, and this module pins v17.2.3 — the writer that produced the bad
// bytes is no longer linked into this binary and cannot be, so no test in this
// package can build a genuinely corrupt index. What is tested is the property
// W0 actually delivers: an index written by the code that came BEFORE this
// change — which is exactly an index with no format record — is discarded and
// rebuilt rather than opened, and a search after the rebuild returns the same
// results as before it.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// w0Collection writes a small collection whose three notes have distinct terms,
// so a search proves WHICH documents are in the index rather than how many.
func w0Collection(t *testing.T) (home, root string) {
	t.Helper()
	home, root = t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "alpha.md", "the quicksilver ledger sits in the alpha note")
	b2WriteFile(t, root, "bravo.md", "the bravo note mentions quicksilver once")
	b2WriteFile(t, root, "sub/charlie.md", "charlie holds nothing in common with the others")
	return home, root
}

// w0IndexDir is the on-disk directory holding a collection's index, manifest and
// format sidecar.
func w0IndexDir(t *testing.T, home, root string) string {
	t.Helper()
	dir, err := IndexDirFor(home, root)
	if err != nil {
		t.Fatalf("IndexDirFor: %v", err)
	}
	return dir
}

// w0BuildAndClose builds a complete index for the collection and closes it,
// returning the paths that make up the on-disk state and the hits a reference
// search produced while it was open.
func w0BuildAndClose(t *testing.T, home, root string) (dir string, docCount uint64, hits []string) {
	t.Helper()
	ix, err := OpenIndex(home, root)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	if reason := ix.RebuildReason(); reason != "" {
		t.Fatalf("first open of a collection that never had an index reported a rebuild: %s", reason)
	}
	if _, syncErr := ix.SyncWith(context.Background(), SyncOptions{}); syncErr != nil {
		t.Fatalf("Sync: %v", syncErr)
	}
	docCount, err = ix.DocCount()
	if err != nil {
		t.Fatalf("DocCount: %v", err)
	}
	found, err := ix.Search("quicksilver", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	hits = b2HitPaths(found)
	sort.Strings(hits)
	if closeErr := ix.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	return w0IndexDir(t, home, root), docCount, hits
}

// w0WriteFormat writes an arbitrary format sidecar, standing in for an index
// stamped by a different version of this code.
func w0WriteFormat(t *testing.T, dir string, version int) {
	t.Helper()
	data, err := json.Marshal(map[string]int{"version": version})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, indexFormatFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// w0ReadFormat returns the version recorded in the sidecar, failing if it is
// absent or unreadable.
func w0ReadFormat(t *testing.T, dir string) int {
	t.Helper()
	got, err := readIndexFormat(filepath.Join(dir, indexFormatFileName))
	if err != nil {
		t.Fatalf("readIndexFormat: %v", err)
	}
	return got
}

// w0Exists reports whether a path is present.
func w0Exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
	return err == nil
}

// w0BuildIndexWithMapping creates a bleve index at path with a DIFFERENT
// mapping from the one the code builds, and puts one document in it. This is
// how an index that predates a mapping change is manufactured: the mapping is
// persisted inside the store at creation and bleve.OpenUsing never replaces it.
func w0BuildIndexWithMapping(t *testing.T, path string, m *bleveMapping.IndexMappingImpl) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	idx, err := bleve.NewUsing(path, m, scorch.Name, scorch.Name, map[string]any{"bolt_timeout": boltOpenTimeout})
	if err != nil {
		t.Fatalf("NewUsing: %v", err)
	}
	if err := idx.Index("legacy.md\x1f0", indexDoc{
		Path: "legacy.md", Name: "legacy", Kind: "note", Offset: 0, Body: "quicksilver",
	}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// w0OpenAndInspect opens the index and reports the state a caller can observe
// straight after the open, before any Sync.
type w0OpenState struct {
	reason        string
	docCount      uint64
	manifestThere bool
	formatVersion int
}

func w0OpenAndInspect(t *testing.T, home, root string) (*Index, w0OpenState) {
	t.Helper()
	ix, err := OpenIndex(home, root)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	count, err := ix.DocCount()
	if err != nil {
		t.Fatalf("DocCount: %v", err)
	}
	return ix, w0OpenState{
		reason:        ix.RebuildReason(),
		docCount:      count,
		manifestThere: w0Exists(t, ix.ManifestPath()),
		formatVersion: w0ReadFormat(t, ix.Dir()),
	}
}

// ---------------------------------------------------------------------------
// G1 — the format version. The W0 exit criterion.
// ---------------------------------------------------------------------------

// TestIndexRebuild_UntrackedFormatIsRebuiltNotOpened is the W0 exit criterion,
// at the size this package can run.
//
// An index written by the code that shipped before W0 has no format sidecar —
// that is not a simulation of the pre-W0 state, it IS the pre-W0 state, because
// no version of this code before W0 wrote such a file. The assertions are the
// two halves of the ADR's criterion: REBUILT RATHER THAN OPENED (the documents
// that were in it are gone, and the manifest that would have made the next Sync
// skip them is gone with them), and THE SEARCH RETURNS RESULTS (identical hits
// after the following Sync).
//
// The document-count assertion is what makes this test unable to pass on a
// no-op: an index that was merely opened still holds its documents.
func TestIndexRebuild_UntrackedFormatIsRebuiltNotOpened(t *testing.T) {
	home, root := w0Collection(t)
	dir, docsBefore, hitsBefore := w0BuildAndClose(t, home, root)

	if docsBefore == 0 {
		t.Fatalf("fixture built an empty index; nothing to rebuild")
	}
	if len(hitsBefore) != 2 {
		t.Fatalf("reference search returned %v, want the two notes mentioning quicksilver", hitsBefore)
	}

	// Age the index to the pre-W0 shape: everything as it is, minus the stamp.
	formatPath := filepath.Join(dir, indexFormatFileName)
	if !w0Exists(t, formatPath) {
		t.Fatalf("no format sidecar was written at %s; G1 has nothing to read", formatPath)
	}
	if err := os.Remove(formatPath); err != nil {
		t.Fatal(err)
	}
	if !w0Exists(t, filepath.Join(dir, ManifestFileName)) {
		t.Fatalf("fixture left no manifest; the rebuild's removal of it would prove nothing")
	}

	ix, state := w0OpenAndInspect(t, home, root)

	if state.reason == "" {
		t.Fatalf("an index with no format record was opened as it stood — this is the silent no-op W0 exists to " +
			"make impossible")
	}
	if !strings.Contains(state.reason, "no format record") {
		t.Errorf("RebuildReason = %q, want it to name the missing format record so an operator can act on it",
			state.reason)
	}
	if state.docCount != 0 {
		t.Errorf("index holds %d documents straight after the rebuild, want 0 — it was opened, not rebuilt",
			state.docCount)
	}
	if state.manifestThere {
		t.Errorf("the manifest survived the rebuild; the next Sync would skip every file as unchanged against "+
			"documents that no longer exist (%s)", ix.ManifestPath())
	}
	if state.formatVersion != indexFormatVersion {
		t.Errorf("format sidecar records version %d after the rebuild, want %d", state.formatVersion, indexFormatVersion)
	}

	// The other half of the criterion: the search works afterwards.
	stats, err := ix.SyncWith(context.Background(), SyncOptions{})
	if err != nil {
		t.Fatalf("Sync after rebuild: %v", err)
	}
	if stats.Indexed != 3 {
		t.Errorf("Sync after rebuild indexed %d files, want all 3 re-read from the collection", stats.Indexed)
	}
	found, err := ix.Search("quicksilver", 10)
	if err != nil {
		t.Fatalf("Search after rebuild: %v", err)
	}
	got := b2HitPaths(found)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(hitsBefore, ",") {
		t.Errorf("search after rebuild = %v, want the same results as before %v", got, hitsBefore)
	}
	if after, cErr := ix.DocCount(); cErr != nil || after != docsBefore {
		t.Errorf("document count after rebuild = %d (err=%v), want %d", after, cErr, docsBefore)
	}
}

// TestIndexRebuild_ForeignFormatVersionIsRebuilt covers the other G1 branch: a
// stamp that exists and disagrees. Written as indexFormatVersion+1 so the test
// keeps meaning something after a future bump.
func TestIndexRebuild_ForeignFormatVersionIsRebuilt(t *testing.T) {
	home, root := w0Collection(t)
	dir, _, _ := w0BuildAndClose(t, home, root)
	w0WriteFormat(t, dir, indexFormatVersion+1)

	_, state := w0OpenAndInspect(t, home, root)
	if state.reason == "" {
		t.Fatalf("an index stamped format %d was opened while the code is at %d",
			indexFormatVersion+1, indexFormatVersion)
	}
	if state.docCount != 0 {
		t.Errorf("index holds %d documents after a format mismatch, want 0", state.docCount)
	}
	if state.formatVersion != indexFormatVersion {
		t.Errorf("format sidecar records %d after the rebuild, want %d", state.formatVersion, indexFormatVersion)
	}
}

// TestIndexRebuild_UnreadableFormatIsRebuilt — an unreadable record of what
// wrote the index is no better than no record, and must not be read as consent
// to open it.
func TestIndexRebuild_UnreadableFormatIsRebuilt(t *testing.T) {
	home, root := w0Collection(t)
	dir, _, _ := w0BuildAndClose(t, home, root)
	if err := os.WriteFile(filepath.Join(dir, indexFormatFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, state := w0OpenAndInspect(t, home, root)
	if state.reason == "" {
		t.Fatalf("an index with an unparseable format record was opened as it stood")
	}
	if state.docCount != 0 {
		t.Errorf("index holds %d documents after an unreadable format record, want 0", state.docCount)
	}
}

// TestIndexRebuild_CurrentFormatIsOpenedNotRebuilt is the control, and it is
// the assertion that stops every test above from passing against a
// rebuild-always implementation, which would satisfy them all and destroy
// FR-039 (the index persists across restarts without rebuilding).
func TestIndexRebuild_CurrentFormatIsOpenedNotRebuilt(t *testing.T) {
	home, root := w0Collection(t)
	_, docsBefore, hitsBefore := w0BuildAndClose(t, home, root)

	ix, state := w0OpenAndInspect(t, home, root)
	if state.reason != "" {
		t.Fatalf("an index in the current format was rebuilt for no reason: %s", state.reason)
	}
	if state.docCount != docsBefore {
		t.Errorf("document count on reopen = %d, want %d preserved (FR-039)", state.docCount, docsBefore)
	}
	if !state.manifestThere {
		t.Errorf("the manifest was removed on a clean reopen; the next Sync would re-read the whole collection")
	}

	stats, err := ix.SyncWith(context.Background(), SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Indexed != 0 || stats.Unchanged != 3 {
		t.Errorf("Sync after a clean reopen indexed=%d unchanged=%d, want 0 and 3 (FR-039: no rebuild)",
			stats.Indexed, stats.Unchanged)
	}
	found, err := ix.Search("quicksilver", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	got := b2HitPaths(found)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(hitsBefore, ",") {
		t.Errorf("search after a clean reopen = %v, want %v", got, hitsBefore)
	}
}

// ---------------------------------------------------------------------------
// G2 — the mapping assertion, for the case G1 cannot see
// ---------------------------------------------------------------------------

// w0MappingCases are mappings that differ from buildIndexMapping in one way
// each, with the substring the reported drift must contain.
func w0MappingCases() []struct {
	name    string
	mutate  func(*bleveMapping.IndexMappingImpl)
	wantSub string
} {
	return []struct {
		name    string
		mutate  func(*bleveMapping.IndexMappingImpl)
		wantSub string
	}{
		{
			name: "analyzer changed on an existing field",
			mutate: func(m *bleveMapping.IndexMappingImpl) {
				fm := bleve.NewTextFieldMapping()
				fm.Analyzer = "keyword"
				fm.Store, fm.IncludeTermVectors, fm.IncludeInAll, fm.DocValues = false, false, false, false
				m.DefaultMapping.Properties[fieldName] = bleveMapping.NewDocumentMapping()
				m.DefaultMapping.AddFieldMappingsAt(fieldName, fm)
			},
			wantSub: `field "name" uses analyzer`,
		},
		{
			name: "a field the code declares is missing",
			mutate: func(m *bleveMapping.IndexMappingImpl) {
				delete(m.DefaultMapping.Properties, fieldKind)
			},
			wantSub: `field "kind" is absent from the persisted mapping`,
		},
		{
			name: "a field the code no longer declares is present",
			mutate: func(m *bleveMapping.IndexMappingImpl) {
				extra := bleve.NewTextFieldMapping()
				extra.Analyzer = "keyword"
				m.DefaultMapping.AddFieldMappingsAt("record_type", extra)
			},
			wantSub: `field "record_type" is in the persisted mapping and the code no longer declares it`,
		},
		{
			name: "an indexed field became unindexed",
			mutate: func(m *bleveMapping.IndexMappingImpl) {
				fm := bleve.NewTextFieldMapping()
				fm.Analyzer = "en"
				fm.Index, fm.Store, fm.IncludeTermVectors, fm.IncludeInAll, fm.DocValues = false, false, false, false, false
				m.DefaultMapping.Properties[fieldBody] = bleveMapping.NewDocumentMapping()
				m.DefaultMapping.AddFieldMappingsAt(fieldBody, fm)
			},
			wantSub: `field "body" has index=`,
		},
		{
			name: "dynamic mapping was on when the index was built",
			mutate: func(m *bleveMapping.IndexMappingImpl) {
				m.DefaultMapping.Dynamic = true
				m.IndexDynamic = true
			},
			wantSub: "dynamic=",
		},
	}
}

// TestMappingDrift_ReportsEveryKindOfDivergence checks G2 against a persisted
// mapping that has genuinely round-tripped through a real index — not against
// an in-memory struct, because the round trip is where a comparison of
// omitempty booleans would quietly stop working.
func TestMappingDrift_ReportsEveryKindOfDivergence(t *testing.T) {
	for _, tc := range w0MappingCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := buildIndexMapping()
			tc.mutate(m)

			path := filepath.Join(t.TempDir(), "bleve")
			w0BuildIndexWithMapping(t, path, m)
			idx, err := bleve.OpenUsing(path, map[string]any{"bolt_timeout": boltOpenTimeout})
			if err != nil {
				t.Fatalf("OpenUsing: %v", err)
			}
			defer func() {
				if cErr := idx.Close(); cErr != nil {
					t.Errorf("Close: %v", cErr)
				}
			}()

			got := mappingDrift(idx.Mapping())
			if got == "" {
				t.Fatalf("mappingDrift found nothing wrong with a mapping that differs: %s", tc.name)
			}
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("mappingDrift = %q, want it to contain %q", got, tc.wantSub)
			}
		})
	}
}

// TestMappingDrift_SilentOnTheShippingMapping is G2's control: an index built
// by this code, reopened by this code, must produce NO drift. Without it G2
// could be a rebuild-always guard and every other G2 test would still pass.
func TestMappingDrift_SilentOnTheShippingMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bleve")
	w0BuildIndexWithMapping(t, path, buildIndexMapping())
	idx, err := bleve.OpenUsing(path, map[string]any{"bolt_timeout": boltOpenTimeout})
	if err != nil {
		t.Fatalf("OpenUsing: %v", err)
	}
	defer func() {
		if cErr := idx.Close(); cErr != nil {
			t.Errorf("Close: %v", cErr)
		}
	}()
	if got := mappingDrift(idx.Mapping()); got != "" {
		t.Errorf("mappingDrift on an unchanged mapping = %q, want \"\" — a guard that always fires is a guard "+
			"that rebuilds the index on every restart", got)
	}
}

// TestMappingDrift_NameOnlyComparisonWouldMissTheAnalyzer is the spike's §4.4
// mutation, kept as a test rather than a claim.
//
// It asserts two things at once: that G2 catches an analyzer change, and that
// the WEAKER guard someone might write instead — comparing field names — could
// not have. The second assertion is what stops G2 from being quietly reduced to
// a name check later: weaken it that way and this test fails, because the two
// mappings' field-name sets are proved identical here.
func TestMappingDrift_NameOnlyComparisonWouldMissTheAnalyzer(t *testing.T) {
	drifted := buildIndexMapping()
	fm := bleve.NewTextFieldMapping()
	fm.Analyzer = "keyword"
	fm.Store, fm.IncludeTermVectors, fm.IncludeInAll, fm.DocValues = false, false, false, false
	drifted.DefaultMapping.Properties[fieldName] = bleveMapping.NewDocumentMapping()
	drifted.DefaultMapping.AddFieldMappingsAt(fieldName, fm)

	shipping := buildIndexMapping()
	names := func(m *bleveMapping.IndexMappingImpl) string {
		out := make([]string, 0, len(m.DefaultMapping.Properties))
		for n := range m.DefaultMapping.Properties {
			out = append(out, n)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	if names(drifted) != names(shipping) {
		t.Fatalf("the mutation changed the field-name set (%s vs %s); it no longer demonstrates what a name-only "+
			"guard misses", names(drifted), names(shipping))
	}

	path := filepath.Join(t.TempDir(), "bleve")
	w0BuildIndexWithMapping(t, path, drifted)
	idx, err := bleve.OpenUsing(path, map[string]any{"bolt_timeout": boltOpenTimeout})
	if err != nil {
		t.Fatalf("OpenUsing: %v", err)
	}
	defer func() {
		if cErr := idx.Close(); cErr != nil {
			t.Errorf("Close: %v", cErr)
		}
	}()
	got := mappingDrift(idx.Mapping())
	if !strings.Contains(got, `field "name" uses analyzer "keyword"`) {
		t.Errorf("mappingDrift = %q, want it to name the analyzer difference a name-only comparison cannot see", got)
	}
}

// TestIndexRebuild_MappingDriftSurvivesAForgottenVersionBump is the spike's
// row F, end to end through OpenIndex.
//
// The index is stamped with the CURRENT format version — the developer changed
// the mapping and did not bump the constant — so G1 is satisfied and cannot
// help. Row E of the spike is what happens without G2: 0 hits, no error. The
// assertion here is that the rebuild happens anyway.
func TestIndexRebuild_MappingDriftSurvivesAForgottenVersionBump(t *testing.T) {
	home, root := w0Collection(t)
	dir := w0IndexDir(t, home, root)

	// An index built by an older mapping, stamped current.
	drifted := buildIndexMapping()
	delete(drifted.DefaultMapping.Properties, fieldKind)
	w0BuildIndexWithMapping(t, filepath.Join(dir, indexBleveSubdir), drifted)
	w0WriteFormat(t, dir, indexFormatVersion)

	ix, state := w0OpenAndInspect(t, home, root)
	if state.reason == "" {
		t.Fatalf("an index whose mapping no longer matches the code was opened as it stood — every query against " +
			"the missing field would return 0 hits and no error")
	}
	if !strings.Contains(state.reason, `field "kind" is absent`) {
		t.Errorf("RebuildReason = %q, want it to name the field that differs", state.reason)
	}
	if state.docCount != 0 {
		t.Errorf("index holds %d documents after a mapping-drift rebuild, want 0", state.docCount)
	}

	if _, err := ix.SyncWith(context.Background(), SyncOptions{}); err != nil {
		t.Fatalf("Sync after rebuild: %v", err)
	}
	found, err := ix.Search("quicksilver", 10)
	if err != nil {
		t.Fatalf("Search after rebuild: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("search after the mapping rebuild returned %v, want the two notes from the collection",
			b2HitPaths(found))
	}
}

// ---------------------------------------------------------------------------
// The neighbouring silent no-op: a manifest that outlives its index
// ---------------------------------------------------------------------------

// TestIndexRebuild_ManifestWithoutAnIndexIsDiscarded.
//
// The manifest is what makes Sync incremental. If the bleve directory is gone
// but the manifest is not — a half-finished cleanup, a restore that missed a
// directory — every file matches its recorded size and mtime, so Sync reports
// three files "unchanged" and indexes nothing, leaving an empty index that
// believes it is complete. The oracle is FR-033 read the right way round: the
// manifest describes what the INDEX knows, and an index that knows nothing may
// not carry a manifest that says otherwise.
func TestIndexRebuild_ManifestWithoutAnIndexIsDiscarded(t *testing.T) {
	home, root := w0Collection(t)
	dir, _, _ := w0BuildAndClose(t, home, root)

	if err := os.RemoveAll(filepath.Join(dir, indexBleveSubdir)); err != nil {
		t.Fatal(err)
	}
	if !w0Exists(t, filepath.Join(dir, ManifestFileName)) {
		t.Fatalf("fixture has no manifest; there is nothing to be stale")
	}

	ix, state := w0OpenAndInspect(t, home, root)
	if state.manifestThere {
		t.Errorf("the manifest outlived its index; Sync will now skip every file as unchanged")
	}
	stats, err := ix.SyncWith(context.Background(), SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Indexed != 3 {
		t.Fatalf("Sync indexed %d files after the index vanished, want all 3 — an empty index reporting itself "+
			"complete is the silent no-op in its purest form", stats.Indexed)
	}
	if found, sErr := ix.Search("quicksilver", 10); sErr != nil || len(found) != 2 {
		t.Errorf("search after recovery = %v (err=%v), want the two matching notes", b2HitPaths(found), sErr)
	}
}

// ---------------------------------------------------------------------------
// Scale — the closest this package can get to the ADR's 100,000 documents
// ---------------------------------------------------------------------------

// TestIndexRebuild_AtSegmentMergeScale runs the same rebuild against an index
// large enough to have gone through scorch's batching and merging rather than
// sitting in one small segment — which is the structural precondition F-0
// needs, even though the corrupting writer itself is no longer linked.
//
// It is 4,000 notes, not 100,000. At 4,000 the index is committed in 32 batches
// of 128 (indexBatchMaxDocs) and the merger runs; at 100,000 the same code path
// runs 782 times. What the larger number would ADD is the specific segment
// layout that triggers F-0 — and that cannot be reached from here at any size,
// because zapx v17.1.2 is not in this build. Running for minutes to reach a
// trigger the build cannot fire would buy nothing, so it is not run.
//
// Skipped under -short.
func TestIndexRebuild_AtSegmentMergeScale(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a multi-thousand-document index")
	}
	const notes = 4000

	home, root := t.TempDir(), t.TempDir()
	for i := range notes {
		name := fmt.Sprintf("n/%05d.md", i)
		b2WriteFile(t, root, name, fmt.Sprintf(
			"note %05d of the corpus; every note mentions quicksilver exactly once", i))
	}

	dir, docsBefore, _ := w0BuildAndClose(t, home, root)
	if docsBefore != notes {
		t.Fatalf("built %d documents, want %d", docsBefore, notes)
	}

	if err := os.Remove(filepath.Join(dir, indexFormatFileName)); err != nil {
		t.Fatal(err)
	}

	ix, state := w0OpenAndInspect(t, home, root)
	if state.reason == "" {
		t.Fatalf("a %d-document index with no format record was opened as it stood", notes)
	}
	if state.docCount != 0 {
		t.Fatalf("index holds %d documents after the rebuild, want 0", state.docCount)
	}

	if _, err := ix.SyncWith(context.Background(), SyncOptions{}); err != nil {
		t.Fatalf("Sync after rebuild: %v", err)
	}
	after, err := ix.DocCount()
	if err != nil {
		t.Fatalf("DocCount: %v", err)
	}
	if after != notes {
		t.Errorf("document count after the rebuild = %d, want %d", after, notes)
	}
	found, err := ix.Search("quicksilver", 5)
	if err != nil {
		t.Fatalf("Search after rebuild: %v — this is the call that panics on a corrupt index", err)
	}
	if len(found) != 5 {
		t.Errorf("search after rebuild returned %d hits, want the 5 requested", len(found))
	}
}

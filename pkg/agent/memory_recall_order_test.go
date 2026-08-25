// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for sortMemoriesNewestFirst (pkg/agent/memory.go).
//
// Confirmed gap: inverting sortMemoriesNewestFirst's mtime comparison
// (memory.go — the `return ti.After(tj)` line inside the sort.SliceStable
// closure — flipped to `return tj.After(ti)`, i.e. oldest-first) left every
// existing memory test green. Two tests that once covered this
// (TestMemoryStore_ReadLongTermEntries_NewestFirst and
// TestMemoryStore_SearchEntries_NewestFirstLiteral) were deleted during a
// format migration and replaced by TestMemoryStore_SearchEntries_
// LiteralSubstring, which dropped the ordering property from both its name
// and its body. memory_context_integration_test.go:43-47 explicitly
// declines to assert ordering, citing mtime ties in a temp directory.
//
// This file closes the gap directly against sortMemoriesNewestFirst using
// its own doc comment as the specification ("sorts MemoryFile slice by file
// mtime descending. Falls back to ID sort when mtime is unavailable") —
// oracle-independent of whatever the function currently does. It also
// proves the same ordering holds end-to-end through the public recall API
// (MemoryStore.SearchEntries) in the one real condition under which
// sortMemoriesNewestFirst actually fires there: BM25 (bleve) unavailable for
// the room, so every candidate scores 0 and SearchEntriesInScope's
// allZero branch calls sortMemoriesNewestFirst on the full result set.
// (With a healthy bleve index, an empty-query recall is a match-all query
// that bleve scores uniformly non-zero, so the allZero branch — and
// therefore sortMemoriesNewestFirst — is never reached; verified by manual
// probe, not asserted here since that is a separate, pre-existing property
// of the bleve-healthy path and out of scope for this defect.)
//
// Traces to: pkg/agent/memory.go::sortMemoriesNewestFirst,
// pkg/agent/memory.go::SearchEntriesInScope (allZero branch).
//
// Build: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestSortMemoriesNewestFirst|^TestMemoryStore_SearchEntries_NewestFirst' -p 1 ./pkg/agent/

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
)

// touchMemoryFile creates an empty <dir>/<id>.md file and sets its mtime to
// ts. sortMemoriesNewestFirst reads mtimes purely from the filesystem via
// memoryFileMtimes, so the file's on-disk mtime — not any timestamp field on
// the MemoryFile struct — is the only thing that matters here.
func touchMemoryFile(t *testing.T, dir, id string, ts time.Time) {
	t.Helper()
	path := filepath.Join(dir, id+".md")
	require.NoError(t, os.WriteFile(path, []byte("stub"), 0o600))
	require.NoError(t, os.Chtimes(path, ts, ts))
}

// memoryFileWithID builds the minimal memrooms.MemoryFile sortMemoriesNewestFirst
// actually reads (Frontmatter.ID) — the rest of the struct is irrelevant to
// the function under test.
func memoryFileWithID(id string) memrooms.MemoryFile {
	return memrooms.MemoryFile{Frontmatter: memrooms.MemoryFrontmatter{ID: id}}
}

// TestSortMemoriesNewestFirst_OrdersByMtimeDescending is the primary
// regression test: given three memories with distinct, known mtimes, in
// scrambled input order, sortMemoriesNewestFirst must reorder them to
// exactly newest-to-oldest.
//
// BDD:
//
//	Given three memory files on disk with mtimes 1h, 2h, and 3h after a base
//	  time, passed to sortMemoriesNewestFirst in the scrambled input order
//	  [1h, 3h, 2h],
//	When sortMemoriesNewestFirst sorts the slice in place,
//	Then the resulting order is exactly [3h, 2h, 1h] — newest first.
//
// Traces to: pkg/agent/memory.go::sortMemoriesNewestFirst (doc comment:
// "sorts MemoryFile slice by file mtime descending").
func TestSortMemoriesNewestFirst_OrdersByMtimeDescending(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	touchMemoryFile(t, dir, "mem-oldest", base.Add(1*time.Hour))
	touchMemoryFile(t, dir, "mem-newest", base.Add(3*time.Hour))
	touchMemoryFile(t, dir, "mem-middle", base.Add(2*time.Hour))

	// Scrambled input order deliberately does NOT match either the expected
	// output order or filesystem/insertion order, so a test that passed by
	// accidentally preserving input order would be caught.
	memories := []memrooms.MemoryFile{
		memoryFileWithID("mem-oldest"),
		memoryFileWithID("mem-newest"),
		memoryFileWithID("mem-middle"),
	}

	sortMemoriesNewestFirst([]string{dir}, memories)

	got := []string{memories[0].Frontmatter.ID, memories[1].Frontmatter.ID, memories[2].Frontmatter.ID}
	require.Equal(t, []string{"mem-newest", "mem-middle", "mem-oldest"}, got,
		"sortMemoriesNewestFirst must order strictly newest-to-oldest by file mtime")
}

// TestSortMemoriesNewestFirst_MultipleDirsMergeByMtime proves the function
// correctly merges mtimes across multiple memories directories — the real
// shape SearchEntriesInScope passes (dirs = [privateRoom.MemoriesDir,
// sharedRoom.MemoriesDir] under RoomScopeBoth), not just a single directory.
//
// Traces to: pkg/agent/memory.go::sortMemoriesNewestFirst,
// pkg/agent/memory.go::SearchEntriesInScope (dirs built from targets, both
// rooms under RoomScopeBoth).
func TestSortMemoriesNewestFirst_MultipleDirsMergeByMtime(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	touchMemoryFile(t, dirA, "from-a-old", base.Add(1*time.Hour))
	touchMemoryFile(t, dirB, "from-b-new", base.Add(5*time.Hour))
	touchMemoryFile(t, dirA, "from-a-mid", base.Add(3*time.Hour))

	memories := []memrooms.MemoryFile{
		memoryFileWithID("from-a-old"),
		memoryFileWithID("from-b-new"),
		memoryFileWithID("from-a-mid"),
	}

	sortMemoriesNewestFirst([]string{dirA, dirB}, memories)

	got := []string{memories[0].Frontmatter.ID, memories[1].Frontmatter.ID, memories[2].Frontmatter.ID}
	require.Equal(t, []string{"from-b-new", "from-a-mid", "from-a-old"}, got,
		"mtimes from every dir in the dirs slice must be merged into one newest-first ordering")
}

// TestSortMemoriesNewestFirst_FallsBackToIDDescendingWhenMtimeUnavailable
// covers the documented fallback: when neither memory's mtime is known (no
// matching file on disk in any of dirs), sortMemoriesNewestFirst falls back
// to descending ID order.
//
// Traces to: pkg/agent/memory.go::sortMemoriesNewestFirst (doc comment:
// "Falls back to ID sort when mtime is unavailable"); the `if ti.IsZero() &&
// tj.IsZero() { return memories[i].Frontmatter.ID > memories[j].Frontmatter.ID }`
// branch.
func TestSortMemoriesNewestFirst_FallsBackToIDDescendingWhenMtimeUnavailable(t *testing.T) {
	dir := t.TempDir() // empty — no memory files exist, so every mtime lookup misses.

	memories := []memrooms.MemoryFile{
		memoryFileWithID("aaa"),
		memoryFileWithID("ccc"),
		memoryFileWithID("bbb"),
	}

	sortMemoriesNewestFirst([]string{dir}, memories)

	got := []string{memories[0].Frontmatter.ID, memories[1].Frontmatter.ID, memories[2].Frontmatter.ID}
	require.Equal(t, []string{"ccc", "bbb", "aaa"}, got,
		"with no mtime available for any entry, the fallback must sort by ID descending")
}

// TestSortMemoriesNewestFirst_KnownMtimeSortsBeforeUnknownMtime covers the
// mixed case: one memory has a real, known mtime; the other's file is
// missing (mtime unavailable). The code's asymmetric IsZero() branches
// (`if ti.IsZero() { return false }` / `if tj.IsZero() { return true }`)
// mean the known-mtime entry must always sort ahead of the unknown one,
// regardless of ID ordering — this test deliberately picks IDs that would
// sort the OPPOSITE way under plain ID-descending, so a broken/removed
// IsZero guard would be caught rather than accidentally matching.
//
// Traces to: pkg/agent/memory.go::sortMemoriesNewestFirst, the two
// `if ti.IsZero() {...}` / `if tj.IsZero() {...}` guards.
func TestSortMemoriesNewestFirst_KnownMtimeSortsBeforeUnknownMtime(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	// "aaa" has a real mtime; "zzz" has none (no file written for it) even
	// though "zzz" > "aaa" lexicographically, which is the opposite of the
	// expected output — proving the assertion isn't passing by ID-sort
	// coincidence.
	touchMemoryFile(t, dir, "aaa", base.Add(1*time.Hour))

	memories := []memrooms.MemoryFile{
		memoryFileWithID("zzz"), // unknown mtime
		memoryFileWithID("aaa"), // known mtime
	}

	sortMemoriesNewestFirst([]string{dir}, memories)

	got := []string{memories[0].Frontmatter.ID, memories[1].Frontmatter.ID}
	require.Equal(t, []string{"aaa", "zzz"}, got,
		"a memory with a known mtime must sort before one with no mtime at all, "+
			"even when ID-descending order would put them the other way round")
}

// TestSortMemoriesNewestFirst_EmptyAndSingleElementDoNotPanic covers the two
// smallest boundary cases (n=0, n=1): sortMemoriesNewestFirst must not panic
// and must leave a single-element slice as a no-op.
//
// Traces to: pkg/agent/memory.go::sortMemoriesNewestFirst.
func TestSortMemoriesNewestFirst_EmptyAndSingleElementDoNotPanic(t *testing.T) {
	dir := t.TempDir()

	empty := []memrooms.MemoryFile{}
	assert.NotPanics(t, func() { sortMemoriesNewestFirst([]string{dir}, empty) })
	assert.Empty(t, empty)

	single := []memrooms.MemoryFile{memoryFileWithID("only-one")}
	assert.NotPanics(t, func() { sortMemoriesNewestFirst([]string{dir}, single) })
	require.Len(t, single, 1)
	assert.Equal(t, "only-one", single[0].Frontmatter.ID)
}

// TestMemoryStore_SearchEntries_NewestFirstWhenBM25Unavailable proves the
// ordering contract end-to-end through the public recall API, in the real
// condition under which sortMemoriesNewestFirst actually fires on that path:
// BM25 (bleve) unavailable for the room. roomIndexLocked (memory.go) never
// caches a nil *memindex.RoomIndex on a real OpenOrCreate failure — it just
// returns nil without writing to ms.indexCache, so every call retries and
// (self-healingly) usually succeeds. Presetting the cache entry to nil here
// reaches the same `t.ri == nil` branch SearchEntriesInScope takes on a
// genuine bleve failure, without needing to fabricate a real one — it is a
// package-internal test (not exported) reaching into MemoryStore's own
// unexported indexCache field for exactly this reason. ms.Close() is
// deliberately NOT called: Close() iterates indexCache and calls ri.Close()
// unconditionally, which is safe for every real entry (roomIndexLocked never
// stores nil) but not for this test-only nil entry — skipping Close() here
// avoids asserting anything about that unrelated, not-reachable-in-production
// codepath. The temp dirs are cleaned up by t.TempDir() regardless.
//
// BDD:
//
//	Given a MemoryStore whose private room has BM25 unavailable, and three
//	  long-term memories written with distinct, known mtimes,
//	When SearchEntries("", N) is called (the exact call GetMemoryContext
//	  makes),
//	Then the returned entries are ordered exactly newest-to-oldest by mtime.
//
// Traces to: pkg/agent/memory.go::GetMemoryContext (SearchEntries("", 20)),
// pkg/agent/memory.go::SearchEntriesInScope (allZero branch calling
// sortMemoriesNewestFirst).
func TestMemoryStore_SearchEntries_NewestFirstWhenBM25Unavailable(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	ms := NewMemoryStore(workspace, home)

	// Force BM25 unavailable for the private room for the lifetime of this
	// test (see doc comment above) — matches the graceful-degradation
	// scenario memory.go already logs as "BM25 disabled for room".
	ms.indexCache[ms.privateRoom.Root] = nil

	require.NoError(t, ms.AppendLongTerm("recall_order_probe_oldest", "reference"))
	require.NoError(t, ms.AppendLongTerm("recall_order_probe_middle", "reference"))
	require.NoError(t, ms.AppendLongTerm("recall_order_probe_newest", "reference"))

	dir := ms.PrivateRoom().MemoriesDir
	written, err := memrooms.ScanMemories(dir)
	require.NoError(t, err)
	require.Len(t, written, 3, "test setup: all three memories must have been written")

	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	offsets := map[string]time.Duration{
		"recall_order_probe_oldest": 1 * time.Hour,
		"recall_order_probe_middle": 2 * time.Hour,
		"recall_order_probe_newest": 3 * time.Hour,
	}
	for _, mf := range written {
		body := strings.TrimSpace(mf.Body)
		offset, ok := offsets[body]
		require.True(t, ok, "unexpected memory body %q", mf.Body)
		ts := base.Add(offset)
		path := filepath.Join(dir, mf.Frontmatter.ID+".md")
		require.NoError(t, os.Chtimes(path, ts, ts))
	}

	entries, err := ms.SearchEntries("", 10)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	got := []string{
		strings.TrimSpace(entries[0].Content),
		strings.TrimSpace(entries[1].Content),
		strings.TrimSpace(entries[2].Content),
	}
	require.Equal(t,
		[]string{"recall_order_probe_newest", "recall_order_probe_middle", "recall_order_probe_oldest"},
		got,
		"SearchEntries(\"\", N) — the exact call GetMemoryContext makes — must return memories "+
			"newest-first when BM25 is unavailable and the scan-fallback / sortMemoriesNewestFirst "+
			"path is exercised",
	)
}

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// Chief's SFH R2-F1/F2 and pr-test F03: healthy notes survive with an explicit
// incomplete-read warning; repaired storage is retryable without another write.
func TestMemoryRound3Storage(t *testing.T) {
	t.Run("PartialReads", round3PartialReads)
	t.Run("InitiallyCorrupt", round3InitiallyCorrupt)
	t.Run("AllNotesUnreadable", round3AllNotesUnreadable)
	t.Run("ConfirmedDeletion", round3ConfirmedDeletion)
	t.Run("InitialIndexBuild", round3InitialIndexBuild)
	t.Run("InitialStoreRetry", round3InitialStoreRetry)
	t.Run("RefreshRetry", round3RefreshRetry)
	t.Run("SharedHandleRetry", round3SharedHandleRetry)
	t.Run("CitationAppendRetry", round3CitationAppendRetry)
}

func round3WriteNote(t *testing.T, dir, id, body string) {
	t.Helper()
	require.NoError(t, memrooms.WriteMemoryFile(dir, memrooms.MemoryFile{
		Frontmatter: memrooms.MemoryFrontmatter{ID: id, Title: "roomnote " + id,
			Type: memrooms.MemoryTypeReference, Status: memrooms.MemoryStatusActive, Author: "test-agent"},
		Body: "roomnote " + body,
	}))
}

func round3PartialReads(t *testing.T) {
	for _, mode := range []string{"IndexedCanonical", "IndexedAlternateName", "ScanFallback"} {
		t.Run(mode, func(t *testing.T) {
			ms, _ := newTestMemoryStore(t)
			t.Cleanup(ms.Close)
			dir := ms.PrivateRoom().MemoriesDir
			round3WriteNote(t, dir, "round3-good", "HEALTHY_NOTE")
			round3WriteNote(t, dir, "round3-bad", "UNREADABLE_ORIGINAL_NOTE")
			badPath := filepath.Join(dir, memrooms.Filename("round3-bad"))
			if mode == "IndexedAlternateName" {
				alias := filepath.Join(dir, "externally-named.md")
				require.NoError(t, os.Rename(badPath, alias))
				badPath = alias
			}
			if mode == "ScanFallback" {
				// A real index-location failure, not a fake search implementation.
				require.NoError(t, os.WriteFile(filepath.Join(ms.PrivateRoom().Root, ".index"), []byte("not a directory"), 0o600))
			}
			initial, err := ms.SearchEntries("roomnote", 20)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{"round3-good", "round3-bad"}, round3EntryIDs(initial))
			if mode != "ScanFallback" {
				require.NotNil(t, ms.indexCache[ms.PrivateRoom().Root], "positive control: index really is warm")
			}
			before, err := os.Stat(dir)
			require.NoError(t, err)
			// Existing-file overwrite keeps directory mtime unchanged: the next
			// indexed read must detect its own file failure, not a rebuild signal.
			require.NoError(t, os.WriteFile(badPath, []byte("malformed note without frontmatter"), 0o600))
			require.NoError(t, os.Chtimes(dir, before.ModTime(), before.ModTime()))
			_, parseErr := readMemoryByID(dir, "round3-bad")
			require.Error(t, parseErr, "positive control: individual note really cannot be decoded")
			_, listErr := os.ReadDir(dir)
			require.NoError(t, listErr, "directory remains readable")
			log := gateCaptureMemoryWarnings(t)
			entries, readErr := ms.SearchEntries("roomnote", 20)
			require.Error(t, readErr, "failed individual read must not claim complete results")
			require.Contains(t, strings.ToLower(readErr.Error()), "partial")
			require.Equal(t, []string{"round3-good"}, round3EntryIDs(entries), "retain exactly the healthy result")
			require.Equal(t, "roomnote HEALTHY_NOTE\n", entries[0].Content)
			result := tools.NewRecallMemoryTool(NewMemoryStoreAdapter(ms)).Execute(t.Context(), map[string]any{"query": "roomnote", "room": "private"})
			require.True(t, result.IsError, "partial recall must never be clean success")
			require.Contains(t, strings.ToLower(result.ForLLM), "warning")
			require.Contains(t, strings.ToLower(result.ForLLM), "partial")
			require.Contains(t, result.ForLLM, "roomnote HEALTHY_NOTE")
			require.NotContains(t, result.ForLLM, "UNREADABLE_ORIGINAL_NOTE")
			prompt := ms.GetMemoryContextForScope(ms.scopeForWorkspace("alpha"))
			require.Contains(t, strings.ToLower(prompt), "partial")
			require.Contains(t, prompt, "roomnote HEALTHY_NOTE")
			require.True(t, gateHasWarning(t, log, ""), "partial prompt must log a warning")
		})
	}
}

func round3InitiallyCorrupt(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	dir := ms.PrivateRoom().MemoriesDir
	round3WriteNote(t, dir, "round3-good", "HEALTHY_NOTE")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "round3-bad.md"), []byte("malformed note without frontmatter"), 0o600))
	entries, err := ms.SearchEntries("roomnote", 20)
	require.Error(t, err, "initial index build must not silently omit a malformed source")
	require.Equal(t, []string{"round3-good"}, round3EntryIDs(entries))
	require.NotContains(t, ms.indexCache, ms.PrivateRoom().Root, "partial index cannot be cached as ready")
	result := tools.NewRecallMemoryTool(NewMemoryStoreAdapter(ms)).Execute(t.Context(), map[string]any{"query": "roomnote", "room": "private"})
	require.True(t, result.IsError)
	require.Contains(t, strings.ToLower(result.ForLLM), "partial")
	require.Contains(t, result.ForLLM, "roomnote HEALTHY_NOTE")
}

func round3AllNotesUnreadable(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	dir := ms.PrivateRoom().MemoriesDir
	round3WriteNote(t, dir, "round3-only", "ONLY_NOTE")
	before, err := ms.SearchEntries("roomnote", 20)
	require.NoError(t, err)
	require.Equal(t, []string{"round3-only"}, round3EntryIDs(before))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "round3-only.md"), []byte("malformed note without frontmatter"), 0o600))
	result := tools.NewRecallMemoryTool(NewMemoryStoreAdapter(ms)).Execute(t.Context(), map[string]any{"query": "roomnote", "room": "private"})
	require.True(t, result.IsError, "zero readable matches is still a failed read, not evidence of absence")
	require.Contains(t, strings.ToLower(result.ForLLM), "warning")
	require.Contains(t, strings.ToLower(result.ForLLM), "partial")
	require.NotContains(t, result.ForLLM, "no matching entries")
}

func round3EntryIDs(entries []LongTermEntry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func round3ConfirmedDeletion(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	dir := ms.PrivateRoom().MemoriesDir
	round3WriteNote(t, dir, "round3-good", "HEALTHY_NOTE")
	round3WriteNote(t, dir, "round3-deleted", "REMOVED_NOTE")
	entries, err := ms.SearchEntries("roomnote", 20)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.NoError(t, os.Remove(filepath.Join(dir, memrooms.Filename("round3-deleted"))))
	entries, err = ms.SearchEntries("roomnote", 20)
	require.NoError(t, err, "confirmed deletion is not a hidden read failure")
	require.Equal(t, []string{"round3-good"}, round3EntryIDs(entries))
}

func round3CitationAppendRetry(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	t.Cleanup(ms.Close)
	fixed := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	ms.SetClock(func() time.Time { return fixed })
	round3WriteNote(t, ms.PrivateRoom().MemoriesDir, "round3-cited", "CITATION_NOTE")
	tracker := newCitationTracker(ms, "alpha")
	tracker.RecordRecalled([]tools.MemoryEntry{{ID: "round3-cited", Title: "roomnote round3-cited"}})
	counterPath := ms.PrivateRoom().CountersPath
	before, err := os.ReadFile(counterPath)
	if errors.Is(err, os.ErrNotExist) {
		before = nil
	} else {
		require.NoError(t, err)
		require.NoError(t, os.Remove(counterPath))
	}
	require.NoError(t, os.Mkdir(counterPath, 0o700))
	want := memrooms.CounterRecord{TS: fixed, MemoryID: "round3-cited", Op: memrooms.CounterOpCited, By: "test-agent"}
	require.Error(t, memrooms.AppendCounterRecord(counterPath, want), "instrument must fail at counter append")
	_, noteErr := memrooms.ReadMemoryFile(ms.PrivateRoom().MemoriesDir, "round3-cited")
	require.NoError(t, noteErr, "the note itself remains readable")
	log := gateCaptureMemoryWarnings(t)
	tracker.EmitCitations("id:round3-cited")
	require.False(t, tracker.emitted["round3-cited"], "failed append must remain pending")
	require.True(t, gateHasWarning(t, log, ""), "counter append failure must warn")
	info, err := os.Stat(counterPath)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "failed counter write did not replace its destination")
	require.NoError(t, os.Remove(counterPath))
	require.NoError(t, os.WriteFile(counterPath, before, 0o600))
	tracker.EmitCitations("id:round3-cited")
	tracker.EmitCitations("id:round3-cited")
	require.Equal(t, []memrooms.CounterRecord{want}, readCounterRecords(t, counterPath), "one exact successful citation after repair; duplicate emission is suppressed")
	require.True(t, tracker.emitted["round3-cited"])
}

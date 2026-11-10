// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package session

// S1 M13: the session-core slice added ~18k lines of test code with almost no
// REAL concurrency coverage — the existing "race" tests are deterministic
// orderings of sequential calls. These two drive genuine goroutines released by
// a barrier. The interleaving is the scheduler's; the ORACLE is deterministic
// under every interleaving, so nothing here depends on timing or load.
//
// ORACLE:
//   - AppendTranscriptOnce is the retry-dedupe seam: its contract is "append
//     this entry at most once". Under N concurrent callers carrying the SAME
//     entry id, exactly ONE must be told it appended and the transcript must
//     hold exactly one copy.
//   - Distinct ids appended concurrently must ALL land: the per-session lock
//     serializes writers, it does not drop them.

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func concurrentAppendStore(t *testing.T) (*UnifiedStore, string) {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "fixture: open a real archive store")
	t.Cleanup(func() { _ = store.Close() })
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "fixture: create the session")
	return store, meta.ID
}

// TestUnifiedStore_ConcurrentAppendOnce_ExactlyOneWins releases eight callers
// carrying the same entry id at the same instant. Any interleaving must leave
// exactly one winner and exactly one stored copy.
func TestUnifiedStore_ConcurrentAppendOnce_ExactlyOneWins(t *testing.T) {
	const callers = 8
	store, sid := concurrentAppendStore(t)
	entry := TranscriptEntry{ID: "dup-entry-1", Role: "user", Content: "the same retried message", AgentID: "mia"}

	start := make(chan struct{})
	appended := make([]bool, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // barrier: every caller enters the seam together
			appended[i], errs[i] = store.AppendTranscriptOnce(sid, entry)
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i := 0; i < callers; i++ {
		require.NoError(t, errs[i], "caller %d must not fail", i)
		if appended[i] {
			winners++
		}
	}
	assert.Equal(t, 1, winners, "exactly one concurrent caller may be told it appended the entry")

	stored, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	copies := 0
	for _, e := range stored {
		if e.ID == entry.ID {
			copies++
		}
	}
	assert.Equal(t, 1, copies, "the transcript must hold exactly one copy of a deduped entry")
}

// TestUnifiedStore_ConcurrentDistinctAppends_NoneLost releases eight callers
// with DISTINCT ids: the per-session lock must serialize them, never drop one.
func TestUnifiedStore_ConcurrentDistinctAppends_NoneLost(t *testing.T) {
	const callers = 8
	store, sid := concurrentAppendStore(t)

	start := make(chan struct{})
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = store.AppendTranscriptStrict(sid, TranscriptEntry{
				ID:      concurrentEntryID(i),
				Role:    "assistant",
				Content: concurrentEntryBody(i),
				AgentID: "mia",
			})
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 0; i < callers; i++ {
		require.NoError(t, errs[i], "caller %d must not fail", i)
	}

	stored, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	seen := map[string]int{}
	for _, e := range stored {
		seen[e.ID]++
	}
	for i := 0; i < callers; i++ {
		id := concurrentEntryID(i)
		assert.Equal(t, 1, seen[id], "entry %s must be stored exactly once", id)
	}
	// The content must be the whole record, never a torn or interleaved line.
	for i := 0; i < callers; i++ {
		want := concurrentEntryBody(i)
		var found bool
		for _, e := range stored {
			if e.ID == concurrentEntryID(i) {
				assert.Equal(t, want, e.Content, "entry %s must round-trip its exact content", e.ID)
				found = true
			}
		}
		assert.True(t, found, "entry %s is missing from the transcript", concurrentEntryID(i))
	}
}

func concurrentEntryID(i int) string   { return "concurrent-" + string(rune('a'+i)) }
func concurrentEntryBody(i int) string { return "body-" + string(rune('a'+i)) + "-tail" }

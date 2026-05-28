//go:build !cgo

// tool_result_store_concurrency_test.go — T5: Nil-safety and concurrency tests
// for toolResultStore.
//
// These tests complement the happy-path tests in replay_since_cursor_test.go by
// exercising:
//  1. Concurrent saves by 100 goroutines — all must succeed, no corruption.
//  2. Duplicate-ref semantics — document and lock down the "second write wins"
//     or "first write wins" behavior of saveJSON.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T5.

package gateway

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolResultStore_ConcurrentSaves launches 100 goroutines that each save a
// distinct JSON payload with a unique session+content, then reads every saved
// ref back and asserts the content matches.
//
// BDD:
//
//	Given a toolResultStore with a valid home directory
//	When 100 goroutines each call saveJSON concurrently with distinct content
//	Then all 100 saves succeed (ok == true, ref != "")
//	And reading each ref back returns exactly the bytes that were written (no corruption)
//
// Traces to: spa-streaming-refactor.md Phase 2D, T5
func TestToolResultStore_ConcurrentSaves(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	const numGoroutines = 100

	// bodies[i] is the payload that goroutine i will write.
	bodies := make([][]byte, numGoroutines)
	for i := range bodies {
		bodies[i] = []byte(fmt.Sprintf(`{"goroutine":%d,"marker":"concurrent-save-test"}`, i))
	}

	type saveResult struct {
		goroutine int
		ref       string
		ok        bool
	}

	results := make([]saveResult, numGoroutines)
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		// capture
		go func() {
			defer wg.Done()
			ref, ok := store.saveJSON(fmt.Sprintf("session-concurrent-%d", i), bodies[i])
			results[i] = saveResult{goroutine: i, ref: ref, ok: ok}
		}()
	}
	wg.Wait()

	// Verify all 100 saves succeeded.
	for _, r := range results {
		if !r.ok {
			t.Errorf("T5-concurrent: goroutine %d saveJSON returned ok=false", r.goroutine)
			continue
		}
		if r.ref == "" {
			t.Errorf("T5-concurrent: goroutine %d saveJSON returned empty ref", r.goroutine)
		}
	}

	// Verify read-back equals what was written for every successful save.
	for _, r := range results {
		if !r.ok || r.ref == "" {
			continue
		}
		got, err := store.readByRef(fmt.Sprintf("session-concurrent-%d", r.goroutine), r.ref)
		require.NoError(t, err,
			"T5-concurrent: readByRef failed for goroutine %d ref %q", r.goroutine, r.ref)
		assert.Equal(t, bodies[r.goroutine], got,
			"T5-concurrent: goroutine %d read-back content does not match written content", r.goroutine)
	}
}

// TestToolResultStore_DifferentRefsPerSave verifies that each call to saveJSON
// produces a distinct ref — refs are random 32-hex strings, so two saves of
// the same content must produce different refs.
//
// BDD:
//
//	Given a toolResultStore
//	When saveJSON is called twice with identical content
//	Then the returned refs differ (refs are randomly generated, not content-addressed)
//
// Traces to: spa-streaming-refactor.md Phase 2D, T5 (ref uniqueness)
func TestToolResultStore_DifferentRefsPerSave(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	body := []byte(`{"value":"identical-content"}`)

	ref1, ok1 := store.saveJSON("sess-dedup", body)
	require.True(t, ok1, "T5-dedup: first saveJSON must succeed")
	require.NotEmpty(t, ref1)

	ref2, ok2 := store.saveJSON("sess-dedup", body)
	require.True(t, ok2, "T5-dedup: second saveJSON must succeed")
	require.NotEmpty(t, ref2)

	// Refs are random — two calls must produce different identifiers.
	if ref1 == ref2 {
		t.Errorf(
			"T5-dedup: two saveJSON calls returned the same ref %q — refs must be randomly generated, not content-addressed",
			ref1,
		)
	}

	// Both refs must be readable and return the original content.
	got1, err1 := store.readByRef("sess-dedup", ref1)
	require.NoError(t, err1)
	assert.Equal(t, body, got1, "T5-dedup: ref1 content mismatch")

	got2, err2 := store.readByRef("sess-dedup", ref2)
	require.NoError(t, err2)
	assert.Equal(t, body, got2, "T5-dedup: ref2 content mismatch")
}

// TestToolResultStore_ConcurrentSaves_NoPartialWrites verifies that a concurrent
// read started immediately after a write never returns partial (corrupted) JSON.
//
// BDD:
//
//	Given 50 goroutines each saving valid JSON
//	When 50 goroutines concurrently read the refs immediately after receiving them
//	Then every read-back is valid JSON (no partial writes, no truncation)
//
// Traces to: spa-streaming-refactor.md Phase 2D, T5 (no file corruption)
func TestToolResultStore_ConcurrentSaves_NoPartialWrites(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	const numPairs = 50

	var mu sync.Mutex
	var readErrors []string
	var wg sync.WaitGroup
	wg.Add(numPairs)

	for i := 0; i < numPairs; i++ {
		go func() {
			defer wg.Done()
			// Write a payload with a distinct numeric marker.
			body := []byte(fmt.Sprintf(`{"index":%d,"padding":"%s"}`, i, strings.Repeat("x", 200)))
			ref, ok := store.saveJSON(fmt.Sprintf("sess-partial-%d", i), body)
			if !ok {
				mu.Lock()
				readErrors = append(readErrors, fmt.Sprintf("goroutine %d: saveJSON failed", i))
				mu.Unlock()
				return
			}
			// Read it back immediately.
			got, err := store.readByRef(fmt.Sprintf("sess-partial-%d", i), ref)
			if err != nil {
				mu.Lock()
				readErrors = append(readErrors, fmt.Sprintf("goroutine %d: readByRef %q: %v", i, ref, err))
				mu.Unlock()
				return
			}
			// The read-back must equal the written body exactly.
			if string(got) != string(body) {
				mu.Lock()
				readErrors = append(
					readErrors,
					fmt.Sprintf("goroutine %d: content mismatch: wrote %d bytes, got %d bytes", i, len(body), len(got)),
				)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	for _, e := range readErrors {
		t.Errorf("T5-no-partial: %s", e)
	}
}

// TestToolResultStore_NilSafety verifies that every public method on a nil
// *toolResultStore is a no-op and does not panic.
//
// BDD:
//
//	Given a nil *toolResultStore
//	When saveJSON or readByRef is called
//	Then no panic occurs and the result is the "disabled" zero value
//
// Traces to: spa-streaming-refactor.md Phase 2D, T5 (nil safety)
func TestToolResultStore_NilSafety(t *testing.T) {
	var store *toolResultStore

	t.Run("saveJSON returns empty ref and ok=false", func(t *testing.T) {
		ref, ok := store.saveJSON("any-session", []byte(`{}`))
		assert.False(t, ok, "nil store: saveJSON must return ok=false")
		assert.Empty(t, ref, "nil store: saveJSON must return empty ref")
	})

	t.Run("readByRef returns non-nil error", func(t *testing.T) {
		_, err := store.readByRef("any-session", "validref0123456789abcdef01234567")
		assert.Error(t, err, "nil store: readByRef must return an error")
	})

	t.Run("maybeOffloadResult returns (nil, false)", func(t *testing.T) {
		large := make([]byte, InlineToolResultMaxBytes+100)
		sentinel, offloaded := maybeOffloadResult(store, "sid", large)
		assert.False(t, offloaded, "nil store: maybeOffloadResult must return offloaded=false")
		assert.Nil(t, sentinel, "nil store: maybeOffloadResult must return nil sentinel")
	})
}

// TestToolResultStore_ContentTest verifies that saveJSON persists specific field
// values and readByRef restores them exactly.
// This is the content assertion that catches "accept but don't persist" bugs.
//
// BDD:
//
//	Given a toolResultStore
//	When a JSON object with a specific "answer" field is saved and read back
//	Then the read-back JSON contains the same "answer" field with the same value
//
// Traces to: spa-streaming-refactor.md Phase 2D, T5 (content test)
func TestToolResultStore_ContentTest(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	bodies := [][]byte{
		[]byte(`{"answer":42,"label":"first"}`),
		[]byte(`{"answer":99,"label":"second"}`),
	}

	for _, body := range bodies {
		ref, ok := store.saveJSON("content-session", body)
		require.True(t, ok, "saveJSON must succeed")
		require.NotEmpty(t, ref)

		got, err := store.readByRef("content-session", ref)
		require.NoError(t, err)

		// Content equality — not just "no error".
		assert.Equal(t, body, got, "T5-content: read-back body must equal written body")
	}
}

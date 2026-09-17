// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A session's split meta files (meta.json, stats.json, loop.json,
// pending_ask.json) are written with fileutil.WriteFileAtomic — a temp file
// renamed over the target — so that nobody ever reads half a file. Every read
// of them that does not hold this store's session shard relies on that: a
// second store over the same directory, another process, or a store reopened
// while the first is still flushing. A present file that is not a complete
// JSON document is reported as corrupt (FR-056) and the session is excluded
// until restart.
//
// The cross-process lock around each write used to be taken on the target
// file itself. fileutil.WithFlock opens its path with O_CREATE, so the FIRST
// write of each file created an empty target and left it there — through the
// temp-file write and its fsync — until the rename replaced it. A reader in
// that window read zero bytes and failed with "unexpected end of JSON input"
// (TestStatsThrottle_ExactCountersAfterInterval on a slow CI runner), and a
// crash in that window would have left the empty file behind for good.
//
// These tests park the first atomic write of each file inside its lock, so
// the window is held open deterministically instead of raced.

// holdFirstWrite parks the first writeFileAtomicFn call for a file named name
// until release is called, and reports that call's path on the returned
// channel once it is parked.
func holdFirstWrite(t *testing.T, name string) (parked <-chan string, release func()) {
	t.Helper()
	parkedCh := make(chan string, 1)
	releaseCh := make(chan struct{})
	var holdOnce sync.Once
	orig := writeFileAtomicFn
	t.Cleanup(func() { writeFileAtomicFn = orig })
	writeFileAtomicFn = func(path string, data []byte, perm os.FileMode) error {
		if filepath.Base(path) == name {
			hold := false
			holdOnce.Do(func() { hold = true })
			if hold {
				parkedCh <- path
				<-releaseCh
			}
		}
		return orig(path, data, perm)
	}
	var releaseOnce sync.Once
	release = func() { releaseOnce.Do(func() { close(releaseCh) }) }
	// Registered after the restore above, so it runs first: a writer still
	// parked when the test fails is released before the seam is restored.
	t.Cleanup(release)
	return parkedCh, release
}

func waitParked(t *testing.T, parked <-chan string) string {
	t.Helper()
	select {
	case path := <-parked:
		return path
	case <-time.After(10 * time.Second):
		t.Fatal("the write never reached its locked section")
		return ""
	}
}

func waitWriteDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the write never finished after it was released")
	}
}

// requireAbsentOrComplete fails when path exists but does not hold a complete
// JSON document — the state a reader outside the store's locks must never see.
func requireAbsentOrComplete(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	require.NoError(t, err)
	require.Truef(t, json.Valid(data),
		"%s exists while its first write is still in flight, but holds %d bytes that are not a complete JSON "+
			"document (%q) — a reader outside this store's locks reads a corrupt session",
		filepath.Base(path), len(data), data)
}

func TestSessionFileWrites_FirstWriteNeverExposesAnIncompleteFile(t *testing.T) {
	t.Run("meta.json", func(t *testing.T) {
		store := u6NewTestStore(t)
		parked, release := holdFirstWrite(t, "meta.json")

		done := make(chan error, 1)
		go func() {
			_, err := store.NewSession(SessionTypeChat, "", "agent-1")
			done <- err
		}()
		path := waitParked(t, parked)
		requireAbsentOrComplete(t, path)

		release()
		waitWriteDone(t, done)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.True(t, json.Valid(data), "meta.json must be a complete document once its write returns")
	})

	cases := []struct {
		file    string
		prepare func(t *testing.T, store *UnifiedStore, sessionID string)
		write   func(store *UnifiedStore, sessionID string) error
	}{
		{
			file: "stats.json",
			prepare: func(t *testing.T, store *UnifiedStore, sessionID string) {
				require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{Role: "user", Content: "x", Tokens: 3}))
			},
			write: func(store *UnifiedStore, sessionID string) error {
				return store.FlushSessionStats(sessionID)
			},
		},
		{
			file: "loop.json",
			write: func(store *UnifiedStore, sessionID string) error {
				mode := "interval"
				return store.SetMeta(sessionID, MetaPatch{LoopMode: &mode, LoopRunCount: intPtr(1)})
			},
		},
		{
			file: "pending_ask.json",
			write: func(store *UnifiedStore, sessionID string) error {
				ask := "ship-it"
				return store.SetMeta(sessionID, MetaPatch{PendingAskJSON: &ask})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			store := u6NewTestStore(t)
			store.SetStatsFlushInterval(time.Hour) // only the write under test may touch stats.json
			meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
			require.NoError(t, err)
			if tc.prepare != nil {
				tc.prepare(t, store, meta.ID)
			}
			target := filepath.Join(store.BaseDir(), meta.ID, tc.file)
			require.NoFileExists(t, target, "precondition: the write under test must be this file's first")

			parked, release := holdFirstWrite(t, tc.file)
			done := make(chan error, 1)
			go func() { done <- tc.write(store, meta.ID) }()
			require.Equal(t, target, waitParked(t, parked))
			requireAbsentOrComplete(t, target)

			release()
			waitWriteDone(t, done)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Truef(t, json.Valid(data), "%s must be a complete document once its write returns", tc.file)
		})
	}
}

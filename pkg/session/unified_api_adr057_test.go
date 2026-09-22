// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U2 (Wave C) — coverage for AppendTranscriptStrict (FR-001, W3
// store half) and CreateSessionWithID (FR-005/FR-006/FR-082/FR-096, W1 store
// half).
//
// Per binding Rule 1, every assertion here runs against a REAL *UnifiedStore
// rooted at a t.TempDir() and real on-disk state — no spy or fake ever
// stands in for the store. Per Rule 4/FR-085, every negative/exclusion gate
// below states and asserts its positive lower bound before its exclusion.
// Per the spec's "Corollary — distinct ids everywhere", every parent/child
// pair used below is constructed as two distinct, non-equal values.

package session

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// u2NewTestStore mirrors the package's existing newTestStore/
// newUnifiedStoreForTest/newTestStoreForLockTests helpers — a small,
// self-contained constructor so this file has no ordering dependency on any
// of them. Prefixed u2 per ownership Rule 6 (this file and U5's
// unified_meta_files.go land in the same package in the same wave).
func u2NewTestStore(t *testing.T) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	// Every NewUnifiedStore(WithHome) unconditionally starts a background
	// stats-flusher goroutine (unified_stats_flush.go's startStatsFlusher,
	// called from the constructor). Without this Close, that goroutine
	// outlives the test and keeps calling through the package-level
	// sessionLockAcquireFn/sessionLockReleaseFn FR-101 seam for as long as
	// the test binary runs — including while a LATER test in this package
	// has its own recorder installed (installLockRecorder), injecting
	// foreign lock events into that later test's trace. Close (via
	// stopStatsFlusher) synchronously stops and joins the goroutine before
	// this test returns, so it can never survive into another test. See
	// issue #634.
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// ---------------------------------------------------------------------
// Test #4: TestCreateSessionWithID_UsesExactIDAndCopiesOwner
// ---------------------------------------------------------------------

// TestCreateSessionWithID_UsesExactIDAndCopiesOwner is test #4 (BDD-06,
// BDD-07): the created session's directory/id is EXACTLY the supplied
// childID, and its Owner is copied verbatim from the parent's — verified
// absent from createSessionLocked's own UnifiedMeta literal, so the Owner on
// the result can only have come from CreateSessionWithID's explicit copy.
func TestCreateSessionWithID_UsesExactIDAndCopiesOwner(t *testing.T) {
	store := u2NewTestStore(t)
	const parentID = "parent-session-4"
	const childID = "child-session-4"
	require.NotEqual(t, parentID, childID)

	_, err := store.GetOrCreateScheduledSession(parentID, "agent-parent")
	require.NoError(t, err)
	ownerVal := "alice"
	require.NoError(t, store.SetMeta(parentID, MetaPatch{Owner: &ownerVal}))

	meta, err := store.CreateSessionWithID(childID, parentID, SessionTypeChat, "", "agent-child")
	require.NoError(t, err)
	require.NotNil(t, meta)

	assert.Equal(t, childID, meta.ID, "the created session's id must be EXACTLY the supplied childID")
	sessionDir := filepath.Join(store.BaseDir(), childID)
	_, statErr := os.Stat(filepath.Join(sessionDir, "meta.json"))
	require.NoError(t, statErr, "meta.json must exist at <baseDir>/<childID>")

	assert.Equal(t, ownerVal, meta.Owner, "the child's Owner must equal the parent's, verbatim")

	// Re-read from disk (not just the in-memory return value) to prove the
	// owner copy is durable, not merely held in the returned struct.
	onDisk, err := store.GetMeta(childID)
	require.NoError(t, err)
	assert.Equal(t, ownerVal, onDisk.Owner, "the owner copy must be persisted to meta.json, not just returned")
}

// TestCreateSessionWithID_EmptyParentOwnerIsLoggedNotSilent is BDD-08: a
// parent with an empty Owner must not silently disable ownership stamping —
// the absence is observable in a log record at spawn time, and the child is
// still created successfully with an empty (not fabricated) Owner.
func TestCreateSessionWithID_EmptyParentOwnerIsLoggedNotSilent(t *testing.T) {
	store := u2NewTestStore(t)
	const parentID = "parent-session-8"
	const childID = "child-session-8"
	require.NotEqual(t, parentID, childID)

	_, err := store.GetOrCreateScheduledSession(parentID, "agent-parent")
	require.NoError(t, err) // Owner left at its zero value: "".

	var logBuf bytes.Buffer
	oldHandler := slog.Default().Handler()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(slog.New(oldHandler))

	meta, err := store.CreateSessionWithID(childID, parentID, SessionTypeChat, "", "agent-child")
	require.NoError(t, err, "an owner-less parent must not block child creation")
	assert.Equal(t, "", meta.Owner, "an empty parent owner must not be silently fabricated into something else")

	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "no owner to inherit", "the absence of a parent owner must be logged, not silent")
	assert.Contains(t, logOutput, childID, "the log record must name the child id")
	assert.Contains(t, logOutput, parentID, "the log record must name the parent id")
}

// ---------------------------------------------------------------------
// Test #111: TestCreateSessionWithID_RejectsCollidingDirectory
// ---------------------------------------------------------------------

// TestCreateSessionWithID_RejectsCollidingDirectory is test #111 (BDD-107,
// STRIDE: tampering): a childID that collides with something already on
// disk is a LOUD failure. Covers both collision shapes FR-096's reasoning
// names: an existing REAL session, and an orphan directory with no
// meta.json — a meta-only check would miss the second shape.
func TestCreateSessionWithID_RejectsCollidingDirectory(t *testing.T) {
	t.Run("existing_real_session", func(t *testing.T) {
		store := u2NewTestStore(t)
		const parentID = "parent-session-111a"
		const childID = "child-session-111a"
		require.NotEqual(t, parentID, childID)

		_, err := store.GetOrCreateScheduledSession(parentID, "agent-parent")
		require.NoError(t, err)

		// The "existing" session at childID is unrelated to parentID — a
		// different owner and agent — so an adoption would be observable.
		existing, err := store.GetOrCreateScheduledSession(childID, "agent-victim")
		require.NoError(t, err)
		victimOwner := "victim-owner"
		require.NoError(t, store.SetMeta(childID, MetaPatch{Owner: &victimOwner}))
		require.NoError(t, store.AppendTranscriptStrict(childID, TranscriptEntry{Role: "user", Content: "victim-line"}))
		_ = existing

		parentOwner := "attacker-owner"
		require.NoError(t, store.SetMeta(parentID, MetaPatch{Owner: &parentOwner}))

		_, err = store.CreateSessionWithID(childID, parentID, SessionTypeChat, "", "agent-attacker")
		require.Error(t, err, "a colliding childID must be refused, not adopted")

		// The victim session's own meta/owner/transcript must be COMPLETELY
		// untouched — never adopted, never merged, never overwritten.
		after, err := store.GetMeta(childID)
		require.NoError(t, err)
		assert.Equal(t, victimOwner, after.Owner, "the pre-existing session's owner must survive a rejected collision untouched")
		assert.Equal(t, "agent-victim", after.AgentID, "the pre-existing session's agent must survive untouched")

		transcript, err := store.ReadTranscript(childID)
		require.NoError(t, err)
		require.Len(t, transcript, 1)
		assert.Equal(t, "victim-line", transcript[0].Content, "the pre-existing session's transcript must be untouched")
	})

	t.Run("orphan_directory_no_meta_json", func(t *testing.T) {
		store := u2NewTestStore(t)
		const parentID = "parent-session-111b"
		const childID = "child-session-111b"
		require.NotEqual(t, parentID, childID)

		_, err := store.GetOrCreateScheduledSession(parentID, "agent-parent")
		require.NoError(t, err)

		// Simulate an orphan directory left by the lenient AppendTranscript's
		// MkdirAll bug this migration is closing elsewhere: a directory with a
		// stray transcript.jsonl but no meta.json at all.
		sessionDir := filepath.Join(store.BaseDir(), childID)
		require.NoError(t, os.MkdirAll(sessionDir, 0o700))
		strayPath := filepath.Join(sessionDir, "transcript.jsonl")
		require.NoError(t, os.WriteFile(strayPath, []byte(`{"role":"user","content":"orphan-line"}`+"\n"), 0o600))

		_, err = store.CreateSessionWithID(childID, parentID, SessionTypeChat, "", "agent-attacker")
		require.Error(t, err, "an orphan directory with no meta.json must ALSO be refused, not silently adopted")

		// The stray file must be untouched, and no meta.json may have been
		// written into the orphan directory.
		strayBytes, readErr := os.ReadFile(strayPath)
		require.NoError(t, readErr)
		assert.Contains(t, string(strayBytes), "orphan-line", "the pre-existing stray file must be untouched")

		_, statErr := os.Stat(filepath.Join(sessionDir, "meta.json"))
		assert.True(t, errors.Is(statErr, os.ErrNotExist), "no meta.json may be written into the rejected orphan directory")
	})
}

// ---------------------------------------------------------------------
// Test #88: TestCreateSessionWithID_NeverHoldsTwoSessionShards
// ---------------------------------------------------------------------

// TestCreateSessionWithID_NeverHoldsTwoSessionShards is this unit's half of
// test #88 (BDD-92/SC-038/FR-082): unlike U4's
// TestSessionLock_SequentialParentThenChildNeverOverlaps (unified_lock_adr057_test.go),
// which SIMULATES the protocol CreateSessionWithID must follow because the
// method did not exist yet in U4's wave, this test drives the REAL
// production CreateSessionWithID and asserts the SAME invariant against its
// actual lock-acquisition trace, recorded via U4's FR-101 seam
// (installLockRecorder/lockEvent, unified_lock_adr057_test.go — an
// already-published, in-package test contract this file consumes without
// editing that file, per this wave's "coordinate by contract" discipline).
//
// Per this spec's stated exception to binding rules 1-2 (lock-acquisition
// order has no on-disk artefact to assert on, and -race is not a lock-order
// checker), this test asserts on the RECORDED invocation order of a
// production seam required by FR-101 — not on invocation as a substitute for
// the thing under test.
func TestCreateSessionWithID_NeverHoldsTwoSessionShards(t *testing.T) {
	store := u2NewTestStore(t)
	const parentID = "parent-session-88-api"
	const childID = "child-session-88-api"
	require.NotEqual(t, parentID, childID, "FR-074 corollary: parent and child ids must be distinct")

	// Real parent, with a real non-empty Owner, created and configured BEFORE
	// the lock recorder is installed so only CreateSessionWithID's own
	// acquisitions are captured.
	_, err := store.GetOrCreateScheduledSession(parentID, "agent-parent")
	require.NoError(t, err)
	ownerVal := "bob"
	require.NoError(t, store.SetMeta(parentID, MetaPatch{Owner: &ownerVal}))

	events, restore := installLockRecorder(t)
	t.Cleanup(restore)

	meta, err := store.CreateSessionWithID(childID, parentID, SessionTypeChat, "", "agent-child")
	require.NoError(t, err)
	require.Equal(t, ownerVal, meta.Owner)

	recorded := *events
	require.Len(t, recorded, 4,
		"expected exactly 4 recorded lock events: acquire(parent) release(parent) acquire(child) release(child) — got %+v", recorded)

	parentShard := u4ShardFor(parentID)
	childShard := u4ShardFor(childID)

	assert.True(t, recorded[0].acquire && recorded[0].shard == parentShard, "event 0 must be acquire(shard(parent))")
	assert.True(t, !recorded[1].acquire && recorded[1].shard == parentShard, "event 1 must be release(shard(parent))")
	assert.True(t, recorded[2].acquire && recorded[2].shard == childShard, "event 2 must be acquire(shard(child))")
	assert.True(t, !recorded[3].acquire && recorded[3].shard == childShard, "event 3 must be release(shard(child))")

	// The core BDD-92 assertion: at NO instant are two session shards held
	// simultaneously — walk the event log tracking currently-held shards.
	held := make(map[uint32]bool)
	for idx, ev := range recorded {
		if ev.acquire {
			held[ev.shard] = true
			require.LessOrEqualf(t, len(held), 1, "two session shards held simultaneously at event %d: %+v", idx, held)
		} else {
			delete(held, ev.shard)
		}
	}
}

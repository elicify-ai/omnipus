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
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	return store
}

// ---------------------------------------------------------------------
// Test #1: TestAppendTranscriptStrict_UnknownSession_ErrorsAndCreatesNothing
// ---------------------------------------------------------------------

// TestAppendTranscriptStrict_UnknownSession_ErrorsAndCreatesNothing is test
// #1 (BDD-01, dataset row 2): a UUID with no meta.json must return a
// non-nil error and create NOTHING on disk.
//
// Red/green evidence (required by this unit's task): this test's assertions
// were DELIBERATELY the exact assertions that FAILED against the
// PRE-ADR-057-U5 lenient UnifiedStore.AppendTranscript for the same input —
// see TestAppendTranscript_StrictAfterFR002_MatchesAppendTranscriptStrict
// below (// ADR-057-U5-inverted), which originally ran the identical setup
// against AppendTranscript and asserted the OPPOSITE outcome (nil error,
// directory created) as a RED pin, and is now INVERTED to assert the FR-002
// end state once U5 (Wave C) made AppendTranscript itself strict.
func TestAppendTranscriptStrict_UnknownSession_ErrorsAndCreatesNothing(t *testing.T) {
	store := u2NewTestStore(t)
	const unknownID = "unknown-session-adr057-1"

	err := store.AppendTranscriptStrict(unknownID, TranscriptEntry{Role: "user", Content: "hello"})
	require.Error(t, err, "AppendTranscriptStrict against an unknown session MUST return a non-nil error")

	sessionDir := filepath.Join(store.BaseDir(), unknownID)
	_, statErr := os.Stat(sessionDir)
	require.Truef(t, os.IsNotExist(statErr),
		"AppendTranscriptStrict MUST create no directory for an unknown session — os.Stat(%q) returned err=%v", sessionDir, statErr)
}

// TestAppendTranscript_StrictAfterFR002_MatchesAppendTranscriptStrict is the
// GREEN half of this unit's required red/green pair, INVERTED from
// TestAppendTranscript_LenientBehavior_DemonstratesTheDefect
// (// ADR-057-U5-inverted) once ADR-057 U5 (Wave C, FR-002/W3a) deleted
// AppendTranscript's lenient slog.Warn+return-nil branch — `[grill2 C2-3]`:
// AC-1's frozen text is a property of AppendTranscript ITSELF, not of a
// strict sibling, so leaving the plain method lenient while only
// AppendTranscriptStrict was strict would satisfy AC-1 only for callers that
// had switched to the sibling. The original version of this test (preserved
// verbatim in git history at commit acfd0e5a) asserted the OPPOSITE outcome
// — nil error, orphan directory created — as a deliberate RED pin proving
// the pre-fix defect was real, not vacuous. Verified red before this
// inversion: `go test -run TestAppendTranscript_LenientBehavior` failed with
// "the LENIENT AppendTranscript returns nil for an unknown session (the
// defect)" once FR-002 landed, which is exactly the signal that the old
// assertions now describe a defect that no longer exists — the correct
// response is inversion (FR-072's principle), not silent deletion.
//
// This test now pins FR-002's actual end state: AppendTranscript and
// AppendTranscriptStrict are "a name, not a second behavior" (FR-002) — both
// must refuse an unknown session identically.
func TestAppendTranscript_StrictAfterFR002_MatchesAppendTranscriptStrict(t *testing.T) {
	store := u2NewTestStore(t)
	const unknownID = "unknown-session-adr057-1-lenient"

	err := store.AppendTranscript(unknownID, TranscriptEntry{Role: "user", Content: "hello"})
	require.Error(t, err, "AppendTranscript itself must now be strict (FR-002) — no lenient sibling survives")

	sessionDir := filepath.Join(store.BaseDir(), unknownID)
	_, statErr := os.Stat(sessionDir)
	require.Truef(t, os.IsNotExist(statErr),
		"AppendTranscript must create NO directory for an unknown session (os.Stat(%q) err=%v) — "+
			"FR-002 deleted the orphan-create branch this test used to pin as a defect", sessionDir, statErr)

	transcriptPath := filepath.Join(sessionDir, "transcript.jsonl")
	_, statErr2 := os.Stat(transcriptPath)
	require.True(t, os.IsNotExist(statErr2), "no transcript.jsonl may be written for an unknown session")

	// FR-002's own text: AppendTranscriptStrict is "a name, not a second
	// behavior" — both entry points must now agree on this exact input.
	const secondUnknownID = "unknown-session-adr057-1-lenient-strict-parity"
	strictErr := store.AppendTranscriptStrict(secondUnknownID, TranscriptEntry{Role: "user", Content: "hello"})
	require.Error(t, strictErr)
	assert.Equal(t, err != nil, strictErr != nil, "AppendTranscript and AppendTranscriptStrict must agree on an unknown session")
}

// ---------------------------------------------------------------------
// Test #2: TestAppendTranscriptStrict_KnownSession_AppendsExactlyOneLine
// ---------------------------------------------------------------------

// TestAppendTranscriptStrict_KnownSession_AppendsExactlyOneLine is test #2
// (BDD-02, dataset row 3): a real, existing session gets exactly one more
// line, and nil is returned.
func TestAppendTranscriptStrict_KnownSession_AppendsExactlyOneLine(t *testing.T) {
	store := u2NewTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)

	before, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	beforeLen := len(before)

	err = store.AppendTranscriptStrict(meta.ID, TranscriptEntry{Role: "user", Content: "hi"})
	require.NoError(t, err)

	after, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, beforeLen+1, len(after), "AppendTranscriptStrict must append EXACTLY one line")
	assert.Equal(t, "hi", after[len(after)-1].Content)
}

// TestAppendTranscriptStrict_StatsThrottled is the 14-reviewer fix-wave
// finding #4 regression guard: AppendTranscriptStrict's stats bookkeeping
// must go through the SAME FR-061 in-memory-only throttle AppendTranscript's
// own hot path already uses (u6MarkStatsDirtyLocked), not a synchronous
// writeMetaLocked call. Proven exactly like TestStatsThrottle_NoFileWriteWithinInterval
// (unified_stats_flush_adr057_test.go) proves it for the non-strict sibling:
// immediately after the call, the transcript line is durably on disk (never
// throttled — FR-062) but stats.json is NOT, while the cache (GetMeta) is
// already current; a forced flush then persists it.
func TestAppendTranscriptStrict_StatsThrottled(t *testing.T) {
	store := u2NewTestStore(t)
	store.SetStatsFlushInterval(time.Hour) // isolate from the periodic ticker

	meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscriptStrict(sessionID, TranscriptEntry{Role: "user", Content: "hi", Tokens: 5}))

	// The transcript line itself is never throttled (FR-062) — real bytes on
	// disk immediately.
	transcript, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, transcript, 1)

	// GREEN 1: stats.json must NOT exist yet — the delta is in-memory only.
	statsPath := filepath.Join(store.BaseDir(), sessionID, "stats.json")
	_, statErr := os.Stat(statsPath)
	assert.True(t, os.IsNotExist(statErr), "stats.json must not exist immediately after AppendTranscriptStrict — the FR-061 throttle must apply here too")

	// GREEN 2: the cache is already current (GetMeta never touches disk on a
	// cache hit) — a caller reading back through the store sees the update
	// immediately despite the disk write being deferred.
	cached, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, 5, cached.Stats.TokensIn)

	// GREEN 3: a forced flush persists the deferred delta — the throttle
	// defers, it never drops.
	require.NoError(t, store.FlushSessionStats(sessionID))
	data, err := os.ReadFile(statsPath)
	require.NoError(t, err)
	var onDisk struct {
		TokensIn int `json:"tokens_in"`
	}
	require.NoError(t, json.Unmarshal(data, &onDisk))
	assert.Equal(t, 5, onDisk.TokensIn)
}

// ---------------------------------------------------------------------
// Dataset: AppendTranscriptStrict session-id resolution (spec §"Test
// Datasets", all 7 boundary rows)
// ---------------------------------------------------------------------

// TestAppendTranscriptStrict_SessionIDResolutionDataset exercises every row
// of the spec's "AppendTranscriptStrict session-id resolution" dataset.
// Assertions are on the OBSERVABLE contract BDD-01/BDD-02 actually state —
// non-nil error and no directory created for every negative row, nil and a
// line-count delta of exactly 1 for the happy row — rather than on which
// internal branch (validateSessionID vs. the readMetaLocked existence
// check) produced a given negative result.
//
// VERIFIED DISCREPANCY (evidence-based, not assumed): the spec's dataset row
// 6 (".hidden") claims the rejection comes from validateSessionID
// ("pre-existing... Leading-dot reject"). Reading pkg/session/unified.go's
// validateSessionID (owned by the U4->U5->U6 chain, not this unit) shows it
// rejects only id=="", a path separator, a literal "..", id=="." and
// id==".context" — it does NOT special-case an arbitrary leading dot, so
// validateSessionID(".hidden") returns nil. AppendTranscriptStrict(".hidden",
// ...) still returns a non-nil error and still creates no directory — via
// the readMetaLocked "session does not exist" branch instead — so the
// EXTERNALLY OBSERVABLE contract this unit owns (FR-001/BDD-01) holds
// regardless. This is flagged here rather than "fixed" because
// validateSessionID is chain-owned (ownership Rule 2 — a unit that needs a
// change in a file it does not own must request it from the owner, never
// edit it itself); the row's error-SOURCE claim, not its error-PRESENCE
// claim, is what does not match the verified tree.
func TestAppendTranscriptStrict_SessionIDResolutionDataset(t *testing.T) {
	type row struct {
		name      string
		id        string
		wantError bool
	}
	rows := []row{
		{name: "row1_empty", id: "", wantError: true},
		{name: "row2_missing_entity", id: "dataset-row2-fresh-uuid", wantError: true},
		{name: "row5_path_traversal", id: "../escape", wantError: true},
		{name: "row6_leading_dot", id: ".hidden", wantError: true}, // see discrepancy note above
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			store := u2NewTestStore(t)
			err := store.AppendTranscriptStrict(r.id, TranscriptEntry{Role: "user", Content: "x"})
			if r.wantError {
				require.Error(t, err, "id %q must be refused", r.id)
			} else {
				require.NoError(t, err)
			}
			// None of these ids may ever gain a directory (validateSessionID's
			// own precondition for "/" ids like "../escape" makes computing a
			// meaningful join unsafe/misleading, so only check paths that stay
			// within baseDir).
			if r.id != "" && r.id != "../escape" {
				sessionDir := filepath.Join(store.BaseDir(), r.id)
				_, statErr := os.Stat(sessionDir)
				assert.Truef(t, os.IsNotExist(statErr), "id %q must create no directory", r.id)
			}
		})
	}

	t.Run("row3_existing_session_happy_path", func(t *testing.T) {
		store := u2NewTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, TranscriptEntry{Role: "user", Content: "x"}))
	})

	t.Run("row4_directory_with_no_meta_json", func(t *testing.T) {
		store := u2NewTestStore(t)
		const corruptID = "dataset-row4-corrupt"
		sessionDir := filepath.Join(store.BaseDir(), corruptID)
		require.NoError(t, os.MkdirAll(sessionDir, 0o700))
		// Deliberately no meta.json written.

		err := store.AppendTranscriptStrict(corruptID, TranscriptEntry{Role: "user", Content: "x"})
		require.Error(t, err, "an existing directory with no meta.json must still be refused (the D11 asymmetry)")

		transcriptPath := filepath.Join(sessionDir, "transcript.jsonl")
		_, statErr := os.Stat(transcriptPath)
		assert.True(t, os.IsNotExist(statErr), "no transcript.jsonl may be written into the corrupt directory")
	})

	t.Run("row7_deleted_between_resolve_and_append", func(t *testing.T) {
		store := u2NewTestStore(t)
		meta, err := store.NewSession(SessionTypeChat, "", "agent-1")
		require.NoError(t, err)
		require.NoError(t, store.DeleteSession(meta.ID))

		err = store.AppendTranscriptStrict(meta.ID, TranscriptEntry{Role: "user", Content: "x"})
		require.Error(t, err, "a deleted session id must not be silently re-created by a subsequent strict append")

		_, statErr := os.Stat(filepath.Join(store.BaseDir(), meta.ID))
		assert.True(t, os.IsNotExist(statErr), "AppendTranscriptStrict must not resurrect a deleted session's directory")
	})
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
		assert.True(t, os.IsNotExist(statErr), "no meta.json may be written into the rejected orphan directory")
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

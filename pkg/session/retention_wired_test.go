// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// pack 6 item 3 — the FR-007 expiry acceptance tests driven against the WIRED
// retention entry point, not the unwired helper the earlier pack exercised
// (QA-CHECK-final-gate Q02 / S3 H1).
//
// The wired path under test is:
//
//	UnifiedStore.RetentionSweep                       (the one owner)
//	  -> expireArchiveSessions                        (per session dir)
//	    -> archiveBackend.expireSession
//	      -> ArchiveDayStore.SweepExpired             (delete aged partitions)
//	      -> archiveBackend.advancePastExpiredLocked  (move the live cursor)
//
// Oracles come from the spec — FR-007 / BDD-02.3 ("expire old main content
// despite protected identity"; "the mark advances to the first retained
// complete group ... or empty same-ID window") — and from the three defects the
// wired path must NOT have (S3-CONSOLIDATED H1/H2/H3): the window must advance
// to the retained complete group; a transient read error is NOT expiry and must
// never shorten a persisted window; only a genuinely torn tail truncates.

package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wiredRetentionSession builds a REAL chat session on a real UnifiedStore whose
// persisted window starts at an aged day-1 complete group (assistant call +
// tool result + user message) with a live day-10 group after it. The archive
// clock is left at 2026-04-10T00:00:00Z, so a 7-day sweep derives the cutoff
// 2026-04-03T00:00:00Z — one fixed clock, both sides.
//
// It returns the store, the session id, the session's archive day store, the
// rolled day-1 partition path, and the four model addresses in model order.
func wiredRetentionSession(t *testing.T) (*UnifiedStore, string, *ArchiveDayStore, string, []ArchiveAddress) {
	t.Helper()
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "fixture: create the session")
	day, err := store.backend.store(meta.ID)
	require.NoError(t, err, "fixture: resolve the session archive")

	atDay(day, "2026-04-01")
	asst, err := day.Append(assistantCallRecord(t, "d1-asst", "2026-04-01", "call_0"))
	require.NoError(t, err, "fixture: append the day-1 assistant call")
	tool, err := day.Append(toolResultRecord(t, "d1-tool", "2026-04-01", "call_0", "d1-asst"))
	require.NoError(t, err, "fixture: append the day-1 tool result")
	usr, err := day.Append(payloadRecord(t, "d1-user", "2026-04-01", "day one user"))
	require.NoError(t, err, "fixture: append the day-1 user message")

	atDay(day, "2026-04-10")
	d2, err := day.Append(payloadRecord(t, "d2-user", "2026-04-10", "day ten user"))
	require.NoError(t, err, "fixture: append the live day-10 user message")

	require.NoError(t, day.SaveWindow(AddressedWindow{
		Start:    asst,
		HasStart: true,
		Model:    []ArchiveAddress{asst, tool, usr, d2},
	}), "fixture: persist the live window")

	return store, meta.ID, day, filepath.Join(day.dir(), "2026-04-01.jsonl"),
		[]ArchiveAddress{asst, tool, usr, d2}
}

// TestRetention_WiredAgedPartitionBehindALiveWindowIsRepaired is row 1: a real
// session whose MODEL window still points into an aged rolled partition, with a
// newer complete group retained. The entries are appended through the ordinary
// transcript path, so the ordinal index and the backend cursor are built by
// production code, not by the test.
//
// FR-007 / BDD-02.3: the cursor advances to the retained complete group and the
// next window read resolves, instead of failing "no partition for key" (S3 H1).
func TestRetention_WiredAgedPartitionBehindALiveWindowIsRepaired(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "fixture: create the session")
	day, err := store.backend.store(meta.ID)
	require.NoError(t, err, "fixture: resolve the session archive")
	ctx := context.Background()

	// The model content goes in through the checked model-append path, so the
	// ordinal index and the backend cursor are published by production code.
	atDay(day, "2026-04-01")
	asst, _, err := store.AppendModelMessage(ctx, meta.ID, ModelAppend{
		Message: providers.Message{Role: "assistant", Content: "day one assistant",
			ToolCalls: []providers.ToolCall{{ID: "call_0", Type: "function",
				Function: &providers.FunctionCall{Name: "t", Arguments: "{}"}}}},
		ViewMembership: ViewMembershipBoth,
		Source:         EntrySource{Kind: "agent", Principal: "mia"},
		AgentID:        "mia",
	})
	require.NoError(t, err, "fixture: append the day-1 assistant call")
	_, _, err = store.AppendModelMessage(ctx, meta.ID, ModelAppend{
		Message:        providers.Message{Role: "tool", ToolCallID: "call_0", Content: "day one tool result"},
		ViewMembership: ViewMembershipBoth,
		Source:         EntrySource{Kind: "agent", Principal: "mia"},
		ToolResultFor:  &asst.Addr,
		AgentID:        "mia",
	})
	require.NoError(t, err, "fixture: append the day-1 tool result")
	_, _, err = store.AppendModelMessage(ctx, meta.ID, ModelAppend{
		Message:        providers.Message{Role: "user", Content: "day one user"},
		ViewMembership: ViewMembershipBoth,
		Source:         EntrySource{Kind: "user", Principal: "daniel"},
		AgentID:        "mia",
	})
	require.NoError(t, err, "fixture: append the day-1 user message")

	atDay(day, "2026-04-10")
	_, _, err = store.AppendModelMessage(ctx, meta.ID, ModelAppend{
		Message:        providers.Message{Role: "user", Content: "day ten user"},
		ViewMembership: ViewMembershipBoth,
		Source:         EntrySource{Kind: "user", Principal: "daniel"},
		AgentID:        "mia",
	})
	require.NoError(t, err, "fixture: append the live day-10 user message")

	rolled := filepath.Join(day.dir(), "2026-04-01.jsonl")
	ageFileTo(t, rolled, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err, "the wired sweep must complete")
	require.Equal(t, 1, removed, "exactly the one aged rolled partition expires")

	if _, statErr := os.Stat(rolled); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the aged rolled partition must be gone; stat err=%v", statErr)
	}

	view, err := store.WindowView(ctx, meta.ID)
	require.NoError(t, err,
		"S3 H1: the next window read must resolve, not fail \"no partition for key\"")
	msgs, ordinals := view.History()
	require.Len(t, msgs, 1, "the repaired window holds exactly the retained message")
	assert.Equal(t, []int{3}, ordinals, "the retained slot keeps its ordinal in the index")
	assert.Equal(t, "day ten user", msgs[0].Content)
}

// TestRetention_WiredTransientReadErrorIsNotExpiry is row 2 (S3 H2): one slot
// read fails with a NON-not-exist error during repair. The window must be
// unchanged and the error must surface; a shortened window is never persisted
// over a transient failure.
func TestRetention_WiredTransientReadErrorIsNotExpiry(t *testing.T) {
	store, _, day, rolled, _ := wiredRetentionSession(t)
	before, found, err := day.LoadWindow()
	require.NoError(t, err)
	require.True(t, found)

	// Portable injection (root- and Windows-proof): put a DIRECTORY where the
	// partition file belongs. It is not a "provably absent partition", so the
	// repair must surface the read failure rather than call the content
	// expired. (chmod 0o000 is ignored by root and on Windows.)
	require.NoError(t, os.Remove(rolled))
	require.NoError(t, os.Mkdir(rolled, 0o700))

	removed, err := store.RetentionSweep(7)
	require.Error(t, err, "S3 H2: a transient read failure must surface, never be swallowed")
	assert.Zero(t, removed, "a failed repair removes nothing")

	after, found, err := day.LoadWindow()
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, before, after,
		"S3 H2: the shortened window must never be persisted over a transient error")
}

// TestRetention_WiredConfiguredModesAndCutoffBoundary is row 5: the three
// configured modes each do what FR-007 says, with the cutoff boundary proven
// against ONE fixed clock (the archive clock the sweep itself reads).
func TestRetention_WiredConfiguredModesAndCutoffBoundary(t *testing.T) {
	// wiredRetentionSession leaves the archive clock at 2026-04-10T00:00:00Z, so
	// a 7-day sweep derives exactly this cutoff.
	base := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)
	cutoff := base.Add(-7 * 24 * time.Hour)

	t.Run("disabled retention deletes nothing", func(t *testing.T) {
		store, _, _, rolled, _ := wiredRetentionSession(t)
		ageFileTo(t, rolled, base.Add(-365*24*time.Hour))

		removed, err := store.RetentionSweep(0)
		require.NoError(t, err)
		assert.Zero(t, removed, "retentionDays <= 0 is the disabled case")
		if _, statErr := os.Stat(rolled); statErr != nil {
			t.Fatalf("disabled retention must touch nothing: %v", statErr)
		}
	})

	t.Run("strict age expires older content and keeps younger content", func(t *testing.T) {
		older := newUnifiedStoreForTest(t)
		olderMeta, err := older.NewSession(SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		olderDay, err := older.backend.store(olderMeta.ID)
		require.NoError(t, err)
		atDay(olderDay, "2026-04-01")
		_, err = olderDay.Append(payloadRecord(t, "old-1", "2026-04-01", "older than the cutoff"))
		require.NoError(t, err)
		atDay(olderDay, "2026-04-10")
		_, err = olderDay.Append(payloadRecord(t, "new-1", "2026-04-10", "younger than the cutoff"))
		require.NoError(t, err)
		olderRolled := filepath.Join(olderDay.dir(), "2026-04-01.jsonl")
		ageFileTo(t, olderRolled, cutoff.Add(-24*time.Hour))

		removed, err := older.RetentionSweep(7)
		require.NoError(t, err)
		assert.Equal(t, 1, removed, "a partition older than the cutoff must expire")
		if _, statErr := os.Stat(olderRolled); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("an older-than-cutoff partition must be deleted; stat err=%v", statErr)
		}
	})

	t.Run("a partition exactly at the cutoff is retained", func(t *testing.T) {
		store, _, _, rolled, _ := wiredRetentionSession(t)
		ageFileTo(t, rolled, cutoff)

		removed, err := store.RetentionSweep(7)
		require.NoError(t, err)
		assert.Zero(t, removed, "BDD-02.3 B05: an equal mtime is retained, not expired")
		if _, statErr := os.Stat(rolled); statErr != nil {
			t.Fatalf("an at-cutoff partition must survive: %v", statErr)
		}
	})
}

// TestRetention_WiredLiveWindowSessionSurvives is row 7: a session whose window
// is entirely inside the retention window survives the sweep untouched.
func TestRetention_WiredLiveWindowSessionSurvives(t *testing.T) {
	store, sid, day, rolled, _ := wiredRetentionSession(t)
	ageFileTo(t, rolled, time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC)) // one day old
	before, found, err := day.LoadWindow()
	require.NoError(t, err)
	require.True(t, found)

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	assert.Zero(t, removed, "nothing inside the retention window may expire")

	if _, statErr := os.Stat(rolled); statErr != nil {
		t.Fatalf("a partition inside the retention window must survive: %v", statErr)
	}
	after, found, err := day.LoadWindow()
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, before, after, "an unexpired session's window must be unchanged")
	if _, err := store.GetMeta(sid); err != nil {
		t.Fatalf("the session identity must survive: %v", err)
	}
}

// TestRetention_WiredMainKeepsItsIdentityWhileItsContentExpires is row 6: a
// main chat's old rolled content still expires (FR-007, "expire old main
// content despite protected identity"), but its identity and directory survive
// — a main chat is deleted only with its agent.
func TestRetention_WiredMainKeepsItsIdentityWhileItsContentExpires(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeMain, "webchat", "mia")
	require.NoError(t, err, "fixture: create the main session")
	day, err := store.backend.store(meta.ID)
	require.NoError(t, err, "fixture: resolve the main's archive")

	atDay(day, "2026-04-01")
	asst, err := day.Append(assistantCallRecord(t, "m-asst", "2026-04-01", "call_m"))
	require.NoError(t, err)
	tool, err := day.Append(toolResultRecord(t, "m-tool", "2026-04-01", "call_m", "m-asst"))
	require.NoError(t, err)
	atDay(day, "2026-04-10")
	d2, err := day.Append(payloadRecord(t, "m-live", "2026-04-10", "the live main message"))
	require.NoError(t, err)
	require.NoError(t, day.SaveWindow(AddressedWindow{
		Start:    asst,
		HasStart: true,
		Model:    []ArchiveAddress{asst, tool, d2},
	}))
	rolled := filepath.Join(day.dir(), "2026-04-01.jsonl")
	ageFileTo(t, rolled, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	assert.Equal(t, 1, removed, "FR-007: a main chat's aged content still expires")

	if _, statErr := os.Stat(day.dir()); statErr != nil {
		t.Fatalf("a main chat is deleted only with its agent — its directory must survive: %v", statErr)
	}
	m, err := store.GetMeta(meta.ID)
	require.NoError(t, err, "the main's identity must still resolve after the sweep")
	assert.Equal(t, meta.ID, m.ID, "FR-007: the same session id, not a re-minted one")

	w, found, err := day.LoadWindow()
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, w.HasStart)
	assert.Equal(t, "m-live", w.Start.EntryID,
		"the mark advances to the first retained complete group")
}

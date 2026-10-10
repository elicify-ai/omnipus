// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the ordinal index and the bounded WindowView (session-core U2
// caller-migration step 1; spec Decisions B and C, FR-005/FR-006). Oracles are
// the spec's model-order example and work bound, not the implementation.
package session

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

var modelSource = EntrySource{Kind: "agent"}

// appendChatOnly writes a saved, not yet consumed input straight to the archive:
// view_membership=chat with its prepared model_message and trusted source.
func appendChatOnly(t *testing.T, b *archiveBackend, key, text string) ArchiveAddress {
	t.Helper()
	store, err := b.store(key)
	require.NoError(t, err)
	mp, err := EncodeModelPayload(providers.Message{Role: "user", Content: text})
	require.NoError(t, err)
	id, err := newArchivePayloadID()
	require.NoError(t, err)
	addr, row, err := store.AppendIndexed(ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: id, Role: "user", Content: text, ViewMembership: ViewMembershipChat, Timestamp: time.Now().UTC()},
		ModelMessage:    &mp,
		Source:          &EntrySource{Kind: "user"},
	})
	require.NoError(t, err)
	require.Nil(t, row, "a chat-only saved input must take no model ordinal")
	return addr
}

func appendModel(t *testing.T, b *archiveBackend, key string, msg providers.Message, issuer *ArchiveAddress) ModelSlot {
	t.Helper()
	membership := ViewMembershipBoth
	if msg.Role == "tool" || msg.Role == "system" {
		membership = ViewMembershipModel
	}
	slot, _, err := b.AppendModelMessage(context.Background(), key, ModelAppend{
		Message: msg, ViewMembership: membership, Source: modelSource, ToolResultFor: issuer,
	})
	require.NoError(t, err)
	return slot
}

func assistantCall(id string) providers.Message {
	return providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: id, Name: "t", Arguments: map[string]any{}}}}
}

// Decision B's example. Physical append order:
//
//	U1(chat), ref(U1), assistant(call), U2(chat), result, ref(U2)
//
// must give MODEL order U1, call, result, U2 — ordinals per model placement.
func TestOrdinals_PlacementOrderNotPhysicalOrder(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-b"
	ctx := context.Background()

	u1 := appendChatOnly(t, b, key, "U1")
	s1, _, err := b.PlaceSavedInput(ctx, key, u1)
	require.NoError(t, err)
	call := appendModel(t, b, key, assistantCall("call_0"), nil)
	u2 := appendChatOnly(t, b, key, "U2") // arrives while the tool group is open
	result := appendModel(t, b, key, providers.Message{Role: "tool", ToolCallID: "call_0", Content: "ok"}, &call.Addr)
	s2, view, err := b.PlaceSavedInput(ctx, key, u2)
	require.NoError(t, err)

	assert.Equal(t, []int{0, 1, 2, 3}, []int{s1.Ordinal, call.Ordinal, result.Ordinal, s2.Ordinal})
	msgs, lines := view.History()
	require.Len(t, msgs, 4)
	assert.Equal(t, []string{"user", "assistant", "tool", "user"},
		[]string{msgs[0].Role, msgs[1].Role, msgs[2].Role, msgs[3].Role})
	assert.Equal(t, "U1", msgs[0].Content)
	assert.Equal(t, "U2", msgs[3].Content)
	assert.Equal(t, []int{0, 1, 2, 3}, lines)

	// One placement per source.
	_, _, err = b.PlaceSavedInput(ctx, key, u1)
	assert.ErrorIs(t, err, ErrAlreadyConsumed)
	again, err := b.store(key)
	require.NoError(t, err)
	n, err := again.OrdinalCount()
	require.NoError(t, err)
	assert.Equal(t, 4, n, "a refused second placement must publish nothing")
}

// A placement of a source that already holds its own model slot would be a
// second copy of the same message.
func TestOrdinals_PlacementRefusesASourceWithItsOwnSlot(t *testing.T) {
	b := newTestBackend(t)
	slot := appendModel(t, b, "sess", userMsg("hello"), nil)
	_, _, err := b.PlaceSavedInput(context.Background(), "sess", slot.Addr)
	require.Error(t, err)
}

// Exact append identity: two identical tool results with the same call id in
// different turns each name THEIR OWN assistant occurrence.
func TestOrdinals_RepeatedCallIDNamesItsOwnIssuer(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-id"
	a1 := appendModel(t, b, key, assistantCall("call_0"), nil)
	r1 := appendModel(t, b, key, providers.Message{Role: "tool", ToolCallID: "call_0", Content: "same"}, &a1.Addr)
	a2 := appendModel(t, b, key, assistantCall("call_0"), nil)
	r2 := appendModel(t, b, key, providers.Message{Role: "tool", ToolCallID: "call_0", Content: "same"}, &a2.Addr)

	store, err := b.store(key)
	require.NoError(t, err)
	rec1, err := store.ReadAt(r1.Addr)
	require.NoError(t, err)
	rec2, err := store.ReadAt(r2.Addr)
	require.NoError(t, err)
	assert.Equal(t, a1.Addr.EntryID, rec1.ToolResultFor.AssistantEntryID)
	assert.Equal(t, a2.Addr.EntryID, rec2.ToolResultFor.AssistantEntryID)
	assert.NotEqual(t, rec1.ToolResultFor.AssistantEntryID, rec2.ToolResultFor.AssistantEntryID)
}

// An append without a provable issuer fails visibly and publishes nothing.
func TestOrdinals_ToolResultWithoutProvableIssuerIsRefused(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-orphan"
	notACall := appendModel(t, b, key, asstMsg("just text"), nil)
	store, err := b.store(key)
	require.NoError(t, err)
	before, err := store.OrdinalCount()
	require.NoError(t, err)

	for name, in := range map[string]ModelAppend{
		"issuer declares no such call": {Message: providers.Message{Role: "tool", ToolCallID: "call_9", Content: "x"},
			ViewMembership: ViewMembershipModel, Source: modelSource, ToolResultFor: &notACall.Addr},
		"no issuer address": {Message: providers.Message{Role: "tool", ToolCallID: "call_9", Content: "x"},
			ViewMembership: ViewMembershipModel, Source: modelSource},
		"no membership": {Message: userMsg("x"), Source: modelSource},
		"no source":     {Message: userMsg("x"), ViewMembership: ViewMembershipBoth},
	} {
		_, _, err := b.AppendModelMessage(context.Background(), key, in)
		require.Error(t, err, name)
	}
	after, err := store.OrdinalCount()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// The legacy SessionWriter finds the issuer among the window's rows.
func TestOrdinals_LegacyToolAppendFindsIssuerInWindow(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-legacy"
	b.AddFullMessage(key, assistantCall("call_0"))
	b.AddFullMessage(key, providers.Message{Role: "tool", ToolCallID: "call_0", Content: "ok"})
	require.Len(t, b.GetHistory(key), 2)
	b.AddFullMessage(key, providers.Message{Role: "tool", ToolCallID: "call_missing", Content: "x"})
	assert.Len(t, b.GetHistory(key), 2, "an unprovable result must not be stored")
}

// WindowView: turn counting, live slots, anchor, excluded spans.
func TestWindowView_TurnsAnchorAndExclusion(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-view"
	ctx := context.Background()
	for i, m := range []providers.Message{
		userMsg("u0"), asstMsg("a0"), userMsg("u1"), asstMsg("a1"), userMsg("u2"), asstMsg("a2"),
	} {
		appendModel(t, b, key, m, nil)
		_ = i
	}
	view, err := b.WindowView(ctx, key)
	require.NoError(t, err)
	require.Len(t, view.Live, 6)
	turn, ok := view.TurnBefore(0)
	assert.True(t, ok)
	assert.Equal(t, 1, turn)
	turn, _ = view.TurnBefore(2) // users strictly before ordinal 2: u0
	assert.Equal(t, 2, turn)
	turn, _ = view.TurnBefore(6) // all three users
	assert.Equal(t, 4, turn)

	// Evict through ordinal 3 with u1 (ordinal 2) pinned as the anchor.
	after := view.State.Clone()
	after.Skip = 4
	anchor := 2
	after.AnchorLine = &anchor
	require.NoError(t, b.CommitWindow(ctx, key, view.State, after))
	view, err = b.WindowView(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, view.Anchor)
	assert.Equal(t, "u1", view.Anchor.Message.Content)
	assert.Equal(t, 2, view.PriorUserTurns, "u0 and u1 precede Skip=4")
	require.Len(t, view.Live, 2)
	msgs, lines := view.History()
	assert.Equal(t, []int{2, 4, 5}, lines, "anchor first, then the live suffix")
	assert.Equal(t, []string{"u1", "u2", "a2"}, []string{msgs[0].Content, msgs[1].Content, msgs[2].Content})
	turn, ok = view.TurnBefore(4)
	assert.True(t, ok)
	assert.Equal(t, 3, turn)
	_, ok = view.TurnBefore(3)
	assert.False(t, ok, "an evicted ordinal needs an indexed range read, not a guess")
	_, ok = view.Slot(3)
	assert.False(t, ok, "a slot outside the window is an explicit miss")
	slot, ok := view.Slot(5)
	assert.True(t, ok)
	assert.Equal(t, "a2", slot.Message.Content)

	// A rollback excludes the appended span but keeps its row and turn counts.
	start := view.State.Clone()
	appendModel(t, b, key, userMsg("aborted"), nil)
	require.NoError(t, b.RollbackWindow(ctx, key, start))
	view, err = b.WindowView(ctx, key)
	require.NoError(t, err)
	require.Len(t, view.Live, 2, "the aborted slot is excluded")
	assert.Equal(t, []memory.ArchiveSpan{{Start: 6, End: 7}}, view.Excluded)
	turn, ok = view.TurnBefore(7)
	assert.True(t, ok)
	assert.Equal(t, 5, turn, "the excluded user message still counts, as it always did")
	appendModel(t, b, key, asstMsg("after"), nil)
	view, err = b.WindowView(ctx, key)
	require.NoError(t, err)
	assert.Len(t, view.Live, 3, "a later append is visible again; the aborted span never resurrects")
}

// A reopened backend serves the same view from disk (marks, not memory).
func TestWindowView_SurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	b := newArchiveBackend(dir)
	const key = "sess-reopen"
	for _, m := range []providers.Message{userMsg("u"), asstMsg("a")} {
		appendModel(t, b, key, m, nil)
	}
	again := newArchiveBackend(dir)
	view, err := again.WindowView(context.Background(), key)
	require.NoError(t, err)
	require.Len(t, view.Live, 2)
	assert.Equal(t, "a", view.Live[1].Message.Content)
}

type readLog struct {
	mu    sync.Mutex
	recs  int
	scans int
}

func (r *readLog) hook(kind string, _ ArchiveAddress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if kind == "scan" {
		r.scans++
	} else {
		r.recs++
	}
}

func (r *readLog) reset() { r.mu.Lock(); r.recs, r.scans = 0, 0; r.mu.Unlock() }

func (r *readLog) get() (recs, scans int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recs, r.scans
}

// FR-005 / Decision C work bound: with a long evicted prefix, the bounded
// operations read only the active slots plus the anchor and the new record —
// never a whole-archive scan and never the evicted prefix.
func TestWindowView_WorkIsProportionalToTheWindow(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-bound"
	ctx := context.Background()
	const total, live = 120, 5
	for i := 0; i < total; i++ {
		m := asstMsg("a")
		if i%2 == 0 {
			m = userMsg("u")
		}
		appendModel(t, b, key, m, nil)
	}
	view, err := b.WindowView(ctx, key)
	require.NoError(t, err)
	after := view.State.Clone()
	after.Skip = total - live
	anchor := 0
	after.AnchorLine = &anchor
	require.NoError(t, b.CommitWindow(ctx, key, view.State, after))

	store, err := b.store(key)
	require.NoError(t, err)
	log := &readLog{}
	store.onRead = log.hook

	view, err = b.WindowView(ctx, key)
	require.NoError(t, err)
	recs, scans := log.get()
	require.Len(t, view.Live, live)
	assert.Zero(t, scans, "a window read must never walk the whole archive")
	// The evicted lead-in an open group still owns is read too: here the slot
	// before Skip is an assistant, so exactly one.
	assert.Equal(t, live+2, recs, "active slots plus the addressed anchor plus the one-slot lead-in")

	log.reset()
	_, view, err = b.AppendModelMessage(ctx, key, ModelAppend{Message: asstMsg("new"), ViewMembership: ViewMembershipBoth, Source: modelSource})
	require.NoError(t, err)
	recs, scans = log.get()
	assert.Zero(t, scans)
	assert.Equal(t, live+3, recs, "append returns the bounded view: window + anchor + lead-in + the appended record")
	assert.Len(t, view.Live, live+1)

	log.reset()
	start := view.State.Clone()
	require.NoError(t, b.RollbackWindow(ctx, key, start))
	require.NoError(t, b.CommitWindow(ctx, key, mustView(t, b, key).State, mustView(t, b, key).State))
	_, scans = log.get()
	assert.Zero(t, scans, "rollback and commit never walk the archive")
}

func mustView(t *testing.T, b *archiveBackend, key string) WindowView {
	t.Helper()
	v, err := b.WindowView(context.Background(), key)
	require.NoError(t, err)
	return v
}

// ReadModelSlots seeks through the index: an evicted range costs its width.
func TestReadModelSlots_IsAnAddressedRangeRead(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-range"
	for i := 0; i < 50; i++ {
		appendModel(t, b, key, asstMsg(string(rune('a'+i%26))), nil)
	}
	store, err := b.store(key)
	require.NoError(t, err)
	log := &readLog{}
	store.onRead = log.hook

	var got []int
	err = b.ReadModelSlots(context.Background(), key, 10, 13, func(s ModelSlot, raw []byte) error {
		got = append(got, s.Ordinal)
		assert.Contains(t, string(raw), `"role":"assistant"`, "the literal model_message JSON is quoted")
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []int{10, 11, 12, 13}, got)
	recs, scans := log.get()
	assert.Zero(t, scans)
	assert.Equal(t, 4, recs)
	require.Error(t, b.ReadModelSlots(context.Background(), key, 49, 50, func(ModelSlot, []byte) error { return nil }))
}

// Crash repair from the saved tail: a row without an offset is completed, a torn
// trailing row is cut, and an archive record whose row was never published is
// retained residue that no window can reach.
func TestOrdinalIndex_RepairsFromTheSavedTail(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-crash"
	for _, m := range []providers.Message{userMsg("u0"), asstMsg("a0"), userMsg("u1")} {
		appendModel(t, b, key, m, nil)
	}
	store, err := b.store(key)
	require.NoError(t, err)

	// (a) the offset of the last row is lost: the row is complete, so it counts.
	offs := store.ordinalOffsetsPath()
	data, err := os.ReadFile(offs)
	require.NoError(t, err)
	require.Len(t, data, 3*8)
	require.NoError(t, os.WriteFile(offs, data[:2*8], 0o600))
	n, err := store.OrdinalCount()
	require.NoError(t, err)
	assert.Equal(t, 3, n, "a complete row with no offset is completed from the tail")
	data, err = os.ReadFile(offs)
	require.NoError(t, err)
	assert.Equal(t, int64(0), int64(binary.BigEndian.Uint64(data[:8])))
	assert.Len(t, data, 3*8)

	// (b) a torn trailing row was never published.
	f, err := os.OpenFile(store.ordinalRowsPath(), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"o":3,"a":{"partition_key":"x"`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	n, err = store.OrdinalCount()
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	slot := appendModel(t, b, key, asstMsg("a1"), nil)
	assert.Equal(t, 3, slot.Ordinal, "the next ordinal continues from the repaired tail")

	// (c) an archive record with no published row is invisible residue.
	mp, err := EncodeModelPayload(asstMsg("ghost"))
	require.NoError(t, err)
	_, err = store.Append(ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: "msg_ghost", Role: "assistant", ViewMembership: ViewMembershipBoth, Timestamp: time.Now().UTC()},
		ModelMessage:    &mp, Source: &EntrySource{Kind: "agent"},
	})
	require.NoError(t, err)
	view := mustView(t, b, key)
	assert.Len(t, view.Live, 4, "an unpublished record is excluded residue, not a model slot")
	assert.FileExists(t, filepath.Join(store.dir(), ordinalRowsFile))
}

// The offsets table is derived and not fsynced: a lost or zero-filled entry is
// detected (every row carries its own ordinal) and rebuilt from the row log, so
// the window still reads correctly — never a wrong record, never a silent gap.
func TestOrdinalIndex_RebuildsAnInconsistentOffsetsTable(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-offsets"
	for _, m := range []providers.Message{userMsg("u0"), asstMsg("a0"), userMsg("u1"), asstMsg("a1")} {
		appendModel(t, b, key, m, nil)
	}
	store, err := b.store(key)
	require.NoError(t, err)
	data, err := os.ReadFile(store.ordinalOffsetsPath())
	require.NoError(t, err)
	require.Len(t, data, 4*8)
	good := append([]byte(nil), data...)
	copy(data[8:16], make([]byte, 8)) // a power loss zero-filled ordinal 1's entry
	require.NoError(t, os.WriteFile(store.ordinalOffsetsPath(), data, 0o600))

	rows, err := store.readOrdinalRows(1, 4) // starts at the corrupted entry
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, 1, rows[0].Ordinal)
	view := mustView(t, b, key)
	require.Len(t, view.Live, 4)
	assert.Equal(t, "a1", view.Live[3].Message.Content)
	rebuilt, err := os.ReadFile(store.ordinalOffsetsPath())
	require.NoError(t, err)
	assert.Equal(t, good, rebuilt, "the table is rebuilt to exactly what the row log implies")
	slot := appendModel(t, b, key, userMsg("u2"), nil)
	assert.Equal(t, 4, slot.Ordinal)
	assert.Equal(t, 3, slot.UserTurn)
}

// LocateToolResult finds a result from index rows alone: the most recent match,
// the addressed line, its issuer and the FR-018 turn number.
func TestLocateToolResult_MostRecentAddressedAndIssuer(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-locate"
	us := &UnifiedStore{backend: b}
	ctx := context.Background()
	appendModel(t, b, key, userMsg("first question"), nil)                                                        // 0
	a1 := appendModel(t, b, key, assistantCall("call_0"), nil)                                                    // 1
	r1 := appendModel(t, b, key, providers.Message{Role: "tool", ToolCallID: "call_0", Content: "one"}, &a1.Addr) // 2
	appendModel(t, b, key, userMsg("second question"), nil)                                                       // 3
	a2 := appendModel(t, b, key, assistantCall("call_0"), nil)                                                    // 4
	r2 := appendModel(t, b, key, providers.Message{Role: "tool", ToolCallID: "call_0", Content: "two"}, &a2.Addr) // 5

	loc, err := us.LocateToolResult(ctx, key, "call_0", -1)
	require.NoError(t, err)
	assert.Equal(t, ToolResultLocation{Found: true, Ordinal: r2.Ordinal, Issuer: a2.Ordinal, TurnNum: 3}, loc, // two user messages precede it
		"the most recent result wins, its issuer is the assistant that declared THAT occurrence")

	loc, err = us.LocateToolResult(ctx, key, "call_0", r1.Ordinal)
	require.NoError(t, err)
	assert.Equal(t, ToolResultLocation{Found: true, Ordinal: r1.Ordinal, Issuer: a1.Ordinal, TurnNum: 2}, loc)

	loc, err = us.LocateToolResult(ctx, key, "call_0", 3) // a user slot
	require.NoError(t, err)
	assert.False(t, loc.Found, "an addressed line that is not that result is not found")
	loc, err = us.LocateToolResult(ctx, key, "call_9", -1)
	require.NoError(t, err)
	assert.False(t, loc.Found)
	loc, err = us.LocateToolResult(ctx, key, "call_0", 99)
	require.NoError(t, err)
	assert.False(t, loc.Found)

	store, err := b.store(key)
	require.NoError(t, err)
	reads := &readLog{}
	store.onRead = reads.hook
	_, err = us.LocateToolResult(ctx, key, "call_0", -1)
	require.NoError(t, err)
	recs, scans := reads.get()
	assert.Zero(t, recs+scans, "locating a result reads index rows, never archive records")
}

// ScanArchive streams slots in ordinal order and stops when asked.
func TestScanArchive_StreamsInOrdinalOrderAndStops(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-scan"
	for i := 0; i < 300; i++ { // crosses the row-chunk boundary
		appendModel(t, b, key, asstMsg("m"), nil)
	}
	var seen []int
	require.NoError(t, b.ScanArchive(context.Background(), key, func(idx int, _ memory.ArchivedMessage) bool {
		seen = append(seen, idx)
		return idx < 140
	}))
	require.Len(t, seen, 141)
	assert.Equal(t, 0, seen[0])
	assert.Equal(t, 140, seen[140])
}

// A slot converted from a hydrated source says so: recall refuses its bytes.
func TestReadModelSlots_CarriesTheConvRebuiltOrigin(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-origin"
	store, err := b.store(key)
	require.NoError(t, err)
	mp, err := EncodeModelPayload(asstMsg("rebuilt from the UI"))
	require.NoError(t, err)
	_, _, err = store.AppendIndexed(ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: "msg_rebuilt", Role: "assistant", ViewMembership: ViewMembershipBoth, Timestamp: time.Now().UTC()},
		ModelMessage:    &mp, Source: &EntrySource{Kind: "agent"}, ModelOrigin: ModelOriginConvRebuilt,
	})
	require.NoError(t, err)
	appendModel(t, b, key, asstMsg("live"), nil)
	var origins []string
	require.NoError(t, b.ReadModelSlots(context.Background(), key, 0, 1, func(s ModelSlot, _ []byte) error {
		origins = append(origins, s.Origin)
		return nil
	}))
	assert.Equal(t, []string{ModelOriginConvRebuilt, ""}, origins)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tool-call effects (session-core U2 effects design D5): a settle, a projection
// batch and a retract are APPENDED records merged on read. The transcript is
// append-only (FR-006), so an address issued by an append stays valid for the
// life of the session, across a UTC day rollover (A1) and under concurrent
// writers (A2).
package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// setArchiveDay makes the store's append clock return day, so the UTC day an
// append is written under (the rollover rule) is deterministic.
func setArchiveDay(s *UnifiedStore, day *time.Time) {
	s.archiveNow = func() time.Time { return *day }
}

func newEffectsSession(t *testing.T) (*UnifiedStore, string) {
	t.Helper()
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	return s, meta.ID
}

// appendCall appends one chat tool_call record at the given time and returns its address.
func appendCall(t *testing.T, s *UnifiedStore, sid, callID, status string, at time.Time) ArchiveAddress {
	t.Helper()
	addr, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{
		ID: callID, Type: EntryTypeToolCall, Timestamp: at,
		ToolCalls: []ToolCall{{ID: ToolCallID(callID), Tool: "web_search", Status: status, Result: map[string]any{"text": "original " + callID}}},
	})
	require.NoError(t, err)
	return addr
}

func toolCallOf(t *testing.T, s *UnifiedStore, sid, callID string) ToolCall {
	t.Helper()
	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	for _, e := range entries {
		for _, tc := range e.ToolCalls {
			if string(tc.ID) == callID {
				return tc
			}
		}
	}
	t.Fatalf("tool call %q not in the merged transcript", callID)
	return ToolCall{}
}

func partitionBytes(t *testing.T, s *UnifiedStore, sid string) map[string][]byte {
	t.Helper()
	dir := filepath.Join(s.baseDir, sid)
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	require.NoError(t, err)
	out := map[string][]byte{}
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		out[filepath.Base(f)] = b
	}
	return out
}

func requirePrefixOf(t *testing.T, before, after map[string][]byte) {
	t.Helper()
	for name, b := range before {
		require.GreaterOrEqual(t, len(after[name]), len(b), "partition %s shrank", name)
		require.Equal(t, b, after[name][:len(b)], "partition %s: earlier bytes changed (a rewrite)", name)
	}
}

// N1 + N2: settle, projection and retract never rewrite a byte, and merge by the rules.
func TestToolCallEffects_AppendOnlyAndMergeRules(t *testing.T) {
	s, sid := newEffectsSession(t)
	now := time.Now().UTC()
	pending := appendCall(t, s, sid, "call-1", "pending", now)
	before := partitionBytes(t, s, sid)

	// Settle guarded by if_status.
	_, err := s.SettleToolCall(sid, pending, "pending", ToolCall{ID: "call-1", Tool: "web_search", Status: "success", Result: map[string]any{"text": "real"}})
	require.NoError(t, err)
	assert.Equal(t, "success", toolCallOf(t, s, sid, "call-1").Status)

	// A second settle that still expects pending is a no-op on read: it cannot clobber the real result.
	_, err = s.SettleToolCall(sid, pending, "pending", ToolCall{ID: "call-1", Tool: "web_search", Status: "denied"})
	require.NoError(t, err)
	assert.Equal(t, "success", toolCallOf(t, s, sid, "call-1").Status, "if_status mismatch must skip the settle")

	// Two projections: last writer wins.
	p1, err := s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: pending, ToolCallID: "call-1", ContentState: "capped", Text: "short"}})
	require.NoError(t, err)
	p2, err := s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: pending, ToolCallID: "call-1", ContentState: "emptied", Text: "gone"}})
	require.NoError(t, err)
	got := toolCallOf(t, s, sid, "call-1")
	assert.Equal(t, "emptied", got.ContentState)
	assert.Equal(t, "gone", got.Result["text"])

	// Retract the newer projection: the older one shows again. Retract both: the exact original.
	_, err = s.RetractToolCallEffects(sid, []ArchiveAddress{p2})
	require.NoError(t, err)
	got = toolCallOf(t, s, sid, "call-1")
	assert.Equal(t, "capped", got.ContentState)
	assert.Equal(t, "short", got.Result["text"])
	_, err = s.RetractToolCallEffects(sid, []ArchiveAddress{p1})
	require.NoError(t, err)
	got = toolCallOf(t, s, sid, "call-1")
	assert.Empty(t, got.ContentState, "retracting every projection restores the pre-projection state")
	assert.Equal(t, "real", got.Result["text"])

	// Retract of a record that is not a projection effect is refused.
	_, err = s.RetractToolCallEffects(sid, []ArchiveAddress{pending})
	require.Error(t, err)

	requirePrefixOf(t, before, partitionBytes(t, s, sid)) // N1: only appends happened
}

// A bad target in a batch appends nothing: the batch is atomic.
func TestProjectToolCalls_BadTargetAppendsNothing(t *testing.T) {
	s, sid := newEffectsSession(t)
	addr := appendCall(t, s, sid, "call-1", "success", time.Now().UTC())
	before := partitionBytes(t, s, sid)
	bad := addr
	bad.ByteOffset += 3 // lands inside the record, not on one
	_, err := s.ProjectToolCalls(sid, []ToolCallProjectionEdit{
		{Target: addr, ToolCallID: "call-1", ContentState: "capped", Text: "x"},
		{Target: bad, ToolCallID: "call-1", ContentState: "capped", Text: "y"},
	})
	require.Error(t, err)
	assert.Equal(t, before, partitionBytes(t, s, sid), "a refused batch writes no byte")
	_, err = s.SettleToolCall(sid, addr, "", ToolCall{ID: "other-call", Status: "success"})
	require.Error(t, err, "a settle naming a call the record does not carry is refused")
}

// N4 / A1: an address issued before a UTC day rollover still projects after it.
func TestProjectToolCalls_SurvivesDayRollover(t *testing.T) {
	s, sid := newEffectsSession(t)
	day1 := time.Date(2026, 3, 27, 23, 59, 0, 0, time.UTC)
	now := day1
	setArchiveDay(s, &now)
	addr := appendCall(t, s, sid, "call-1", "success", day1)
	now = day1.Add(2 * time.Minute)
	appendCall(t, s, sid, "call-2", "success", now) // rolls the partition
	files := partitionBytes(t, s, sid)
	require.Contains(t, files, "2026-03-27.jsonl", "the first day's file is frozen under its day name")

	_, err := s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: addr, ToolCallID: "call-1", ContentState: "capped", Text: "short"}})
	require.NoError(t, err, "the chat record sits in a rolled partition; its address must still resolve")
	got := toolCallOf(t, s, sid, "call-1")
	assert.Equal(t, "capped", got.ContentState)
	assert.Equal(t, "short", got.Result["text"])
	_, err = s.SettleToolCall(sid, addr, "success", ToolCall{ID: "call-1", Status: "error", Error: "late"})
	require.NoError(t, err)
	assert.Equal(t, "error", toolCallOf(t, s, sid, "call-1").Status)
}

// N5 / A2: settles and appends from many goroutines lose no record.
func TestToolCallEffects_ConcurrentWritersLoseNothing(t *testing.T) {
	s, sid := newEffectsSession(t)
	const n = 24
	addrs := make([]ArchiveAddress, n)
	for i := 0; i < n; i++ {
		addrs[i] = appendCall(t, s, sid, fmt.Sprintf("call-%d", i), "pending", time.Now().UTC())
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_, err := s.SettleToolCall(sid, addrs[i], "pending", ToolCall{ID: ToolCallID(fmt.Sprintf("call-%d", i)), Status: "success"})
			assert.NoError(t, err)
		}(i)
		go func(i int) {
			defer wg.Done()
			assert.NoError(t, s.AppendTranscript(sid, TranscriptEntry{ID: fmt.Sprintf("msg-%d", i), Role: "user", Content: "hi", Timestamp: time.Now().UTC()}))
		}(i)
	}
	wg.Wait()
	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	msgs, calls := 0, 0
	for _, e := range entries {
		if e.Role == "user" {
			msgs++
		}
		for _, tc := range e.ToolCalls {
			calls++
			assert.Equal(t, "success", tc.Status, "every concurrent settle is visible")
		}
	}
	assert.Equal(t, n, msgs, "no concurrently appended message was lost")
	assert.Equal(t, n, calls)
}

// N7 / A3: a torn final line does not swallow the next record, and the returned address resolves.
func TestAppendTranscriptRecord_TornFinalLineGetsFreshLine(t *testing.T) {
	s, sid := newEffectsSession(t)
	first := appendCall(t, s, sid, "call-1", "success", time.Now().UTC())
	path := filepath.Join(s.baseDir, sid, "transcript.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"id":"torn","type":"mess`) // a crash mid-line: no trailing newline
	require.NoError(t, err)
	require.NoError(t, f.Close())

	second := appendCall(t, s, sid, "call-2", "success", time.Now().UTC())
	assert.Greater(t, second.ByteOffset, first.ByteOffset)
	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	ids := []string{}
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	assert.Equal(t, []string{"call-1", "call-2"}, ids, "the new record is intact and the torn residue is skipped")
	_, err = s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: second, ToolCallID: "call-2", ContentState: "capped", Text: "x"}})
	require.NoError(t, err, "the address returned after a torn line points at the new record's first byte")
}

// A4: a rollover whose frozen name already exists is refused, never re-pointed.
func TestTranscriptRollover_CollisionIsRefused(t *testing.T) {
	s, sid := newEffectsSession(t)
	day1 := time.Date(2026, 3, 27, 10, 0, 0, 0, time.UTC)
	now := day1
	setArchiveDay(s, &now)
	appendCall(t, s, sid, "call-1", "success", day1)
	stray := filepath.Join(s.baseDir, sid, "2026-03-27.jsonl")
	require.NoError(t, os.WriteFile(stray, []byte("{}\n"), 0o600))
	now = day1.Add(24 * time.Hour)
	_, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{
		ID: "call-2", Type: EntryTypeToolCall, Timestamp: now,
		ToolCalls: []ToolCall{{ID: "call-2", Status: "success"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	b, rerr := os.ReadFile(stray)
	require.NoError(t, rerr)
	assert.Equal(t, "{}\n", string(b), "the out-of-band file is untouched")
}

// N3 / D1-D4: chat, model and effect records of one session share ONE file set at
// the transcript's own path; the chat reader sees only chat; the model window is
// unaffected by interleaved chat and effect records; a model address still
// resolves after a settle.
func TestSharedArchive_OneFileSetChatModelAndEffects(t *testing.T) {
	s, sid := newEffectsSession(t)
	ctx := context.Background()
	now := time.Now().UTC()

	require.NoError(t, s.AppendTranscript(sid, TranscriptEntry{ID: "u1", Role: "user", Content: "hi", Timestamp: now}))
	_, _, err := s.AppendModelMessage(ctx, sid, ModelAppend{
		Message: providers.Message{Role: "user", Content: "hi"}, ViewMembership: ViewMembershipModel, Source: EntrySource{Kind: "user"},
	})
	require.NoError(t, err)
	callRec := appendCall(t, s, sid, "call-1", "pending", now)
	asstMsg := assistantCall("call-1")
	asstMsg.Content = "searching"
	asst, _, err := s.AppendModelMessage(ctx, sid, ModelAppend{
		Message: asstMsg, ViewMembership: ViewMembershipModel, Source: EntrySource{Kind: "agent"},
	})
	require.NoError(t, err)
	_, err = s.SettleToolCall(sid, callRec, "pending", ToolCall{ID: "call-1", Tool: "web_search", Status: "success"})
	require.NoError(t, err)
	_, _, err = s.AppendModelMessage(ctx, sid, ModelAppend{
		Message: providers.Message{Role: "tool", ToolCallID: "call-1", Content: "ok"}, ViewMembership: ViewMembershipModel,
		Source: EntrySource{Kind: "tool"}, ToolResultFor: &asst.Addr,
	})
	require.NoError(t, err)

	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	ids := []string{}
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	assert.Equal(t, []string{"u1", "call-1"}, ids, "the chat view holds chat records only")

	view, err := s.WindowView(ctx, sid)
	require.NoError(t, err)
	require.Len(t, view.Live, 3, "the model window holds exactly the three model payloads")
	assert.Equal(t, []string{"user", "assistant", "tool"}, []string{view.Live[0].Message.Role, view.Live[1].Message.Role, view.Live[2].Message.Role})

	dir := filepath.Join(s.baseDir, sid)
	assert.NoDirExists(t, filepath.Join(dir, "u2archive"), "no second file set")
	raw, err := os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, 6, bytes.Count(raw, []byte{'\n'}), "chat, model and effect records are lines of the one file")

	store, err := s.archiveStore(sid)
	require.NoError(t, err)
	rec, err := store.ReadAt(asst.Addr)
	require.NoError(t, err, "a model address still resolves after a settle")
	assert.Equal(t, asst.Addr.EntryID, rec.ID)
	assert.Empty(t, rec.Content, "a model-only record does not copy its payload into the chat fields")
}

// A chat append cannot claim the model view.
func TestAppendTranscript_RefusesNonChatMembership(t *testing.T) {
	s, sid := newEffectsSession(t)
	err := s.AppendTranscript(sid, TranscriptEntry{ID: "x", Role: "user", Content: "hi", ViewMembership: ViewMembershipModel, Timestamp: time.Now().UTC()})
	require.Error(t, err)
	err = s.AppendTranscript(sid, TranscriptEntry{ID: "y", Role: "user", Content: "hi", ViewMembership: ViewMembershipChat, Timestamp: time.Now().UTC()})
	require.NoError(t, err, "an explicit chat membership is the same as the default")
}

// N5 / A2 with model writers: chat appends, model appends and settles interleave
// under one writer lock with no lost record.
func TestSharedArchive_ConcurrentChatModelAndSettlesLoseNothing(t *testing.T) {
	s, sid := newEffectsSession(t)
	ctx := context.Background()
	const n = 12
	addrs := make([]ArchiveAddress, n)
	for i := 0; i < n; i++ {
		addrs[i] = appendCall(t, s, sid, fmt.Sprintf("call-%d", i), "pending", time.Now().UTC())
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			_, err := s.SettleToolCall(sid, addrs[i], "pending", ToolCall{ID: ToolCallID(fmt.Sprintf("call-%d", i)), Status: "success"})
			assert.NoError(t, err)
		}(i)
		go func(i int) {
			defer wg.Done()
			assert.NoError(t, s.AppendTranscript(sid, TranscriptEntry{ID: fmt.Sprintf("msg-%d", i), Role: "user", Content: "hi", Timestamp: time.Now().UTC()}))
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _, err := s.AppendModelMessage(ctx, sid, ModelAppend{
				Message: providers.Message{Role: "user", Content: fmt.Sprintf("m%d", i)}, ViewMembership: ViewMembershipModel, Source: EntrySource{Kind: "user"},
			})
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()
	view, err := s.WindowView(ctx, sid)
	require.NoError(t, err)
	assert.Len(t, view.Live, n, "every concurrent model append took its own slot")
	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	assert.Len(t, entries, 2*n, "every chat append and tool_call record survived")
}

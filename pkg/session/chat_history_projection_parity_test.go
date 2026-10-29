package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestChatHistoryRebuild_MatchesLiveProjectionForEveryEffect is the replacement
// for the deleted cmd/chat-history-index-repair projection parity test (train 4a
// removed that command). It rebuilds the derived index from scratch and checks
// the rebuilt page against the live canonical projection.
//
// Spec rows, not today's printer:
//   - D-P1 / T04 / B18: pending → guarded success → mismatching settle does not
//     apply; capped projection, then emptied, then retract of the emptied one
//     leaves capped/"short"; discarded input; matching cancel marks the answer
//     truncated/"cancelled"; duplicate ids keep both occurrences and the last
//     one is canonical. Canonical bytes stay unchanged.
//   - D2: each occurrence position is hp1_ plus the length-delimited SHA-256 of
//     session id, partition key, decimal offset and entry id (historyExpectedPosition
//     copies that recipe; it does not call the product encoder).
//   - D3 / D4a: a short latest page that holds every eligible group ends there
//     (has_earlier/has_later false, stop_reason end).
//
// The live reader is ReadTranscriptWithDispositions: ReadTranscript merges tool
// effects and cancel truncation; the FR-024 sidecar adds the discarded-input
// label. ReadChatHistoryPage must show that same projection, entry by entry.
func TestChatHistoryRebuild_MatchesLiveProjectionForEveryEffect(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := NewUnifiedStore(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	closed := false
	t.Cleanup(func() {
		if !closed {
			require.NoError(t, s.Close())
		}
	})
	day := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	setArchiveDay(s, &day)
	meta, err := s.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID

	owner, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{
		ID: "owner", Type: EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: day,
		ToolCalls: []ToolCall{{ID: "call", Tool: "read_file", Status: "pending", Result: map[string]any{"text": "original"}}},
	})
	require.NoError(t, err)
	// Guarded settle, then a mismatching settle that must not clobber it (D-P1).
	_, err = s.SettleToolCall(sid, owner, "pending", ToolCall{ID: "call", Tool: "read_file", Status: "success", Result: map[string]any{"text": "actual"}})
	require.NoError(t, err)
	_, err = s.SettleToolCall(sid, owner, "pending", ToolCall{ID: "call", Tool: "read_file", Status: "denied"})
	require.NoError(t, err)
	_, err = s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: owner, ToolCallID: "call", ContentState: "capped", Text: "short"}})
	require.NoError(t, err)
	emptied, err := s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: owner, ToolCallID: "call", ContentState: "emptied", Text: "gone"}})
	require.NoError(t, err)
	_, err = s.RetractToolCallEffects(sid, []ArchiveAddress{emptied})
	require.NoError(t, err)

	public := make([]ArchiveAddress, 0, 6)
	public = append(public, owner)
	for _, e := range []TranscriptEntry{
		{ID: "discarded", Role: "user", Content: "not delivered", ClientMessageID: "client-u"},
		{ID: "answer", Role: "assistant", Content: "partial", TurnID: "T2"},
		{ID: "cancel", Type: EntryTypeTurnCancelled, TurnID: "T2", CancelMethod: "graceful"},
		{ID: "duplicate", Role: "user", Content: "older"},
		{ID: "duplicate", Role: "user", Content: "canonical newest"},
	} {
		e.AgentID = "mia"
		e.Timestamp = day
		addr, appendErr := s.AppendTranscriptAddressed(sid, e)
		require.NoError(t, appendErr)
		public = append(public, addr)
	}
	newestDup := public[len(public)-1]
	require.NoError(t, s.RecordInputDiscarded(sid, "discarded", "client-u"))

	live, err := s.ReadTranscriptWithDispositions(sid)
	require.NoError(t, err)
	requireSixEffectEntries(t, live)

	require.NoError(t, s.Close())
	closed = true
	dir := filepath.Join(root, "sessions", sid)
	before := canonicalFileBytes(t, dir)
	require.NoError(t, VerifyChatHistoryIndex(ctx, dir), "live writer must have published a healthy index before the derived tree is removed")
	require.NoError(t, os.RemoveAll(filepath.Join(dir, ChatHistoryIndexDirectory)))
	build, err := PrepareChatHistoryIndex(ctx, dir, ChatHistoryIndexBuildOptions{})
	require.NoError(t, err, "fresh build after the derived index directory is removed")
	require.NotNil(t, build)
	require.Equal(t, "create", build.Summary().Action, "a missing checkpoint is a fresh build, not an unchanged index")
	require.EqualValues(t, len(public), build.Summary().SourceGroupCount, "effect records are not source groups; the six public appends are")
	require.Equal(t, before, canonicalFileBytes(t, dir), "prepare must not rewrite canonical bytes")
	require.NoError(t, build.Publish(ctx))
	require.NoError(t, build.Close())
	require.Equal(t, before, canonicalFileBytes(t, dir), "publish writes only the derived index")
	require.NoError(t, VerifyChatHistoryIndex(ctx, dir))

	reopened, err := NewUnifiedStore(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	again, err := reopened.ReadTranscriptWithDispositions(sid)
	require.NoError(t, err)
	require.Equal(t, live, again, "rebuilding the index must not change the canonical projection")

	page := historyRead(t, reopened, sid, historyOptions(100))
	require.False(t, page.HasEarlier, "D3: the whole short session has no earlier group")
	require.False(t, page.HasLater, "D3: the latest page has no later group")
	require.Equal(t, "end", page.PageInfo.StopReason, "D4a: eligible content ended; bytes must not cut this page")
	require.Len(t, page.Entries, len(live))
	for i := range live {
		require.Equal(t, live[i], page.Entries[i].Entry, "rebuilt page entry %d (%s) must match the live canonical projection", i, live[i].ID)
		require.Equal(t, historyExpectedPosition(sid, public[i]), page.Entries[i].Position, "D2 position for entry %d (%s)", i, live[i].ID)
	}
	requireSixEffectEntries(t, historyEntries(page))
	require.Equal(t, []ChatHistoryDuplicate{{
		Kind: "message", ID: "duplicate", OccurrenceCount: 2,
		CanonicalPosition: historyExpectedPosition(sid, newestDup),
	}}, page.PageInfo.Context.DuplicateIDs, "D-P1/D2: last duplicate occurrence is canonical")
}

// requireSixEffectEntries checks the frozen D-P1 projection on the public
// entries, in acceptance order. The expected values are the spec row and the
// deleted parity fixture, not a sample of the reader.
func requireSixEffectEntries(t *testing.T, entries []TranscriptEntry) {
	t.Helper()
	require.Equal(t, []string{"owner", "discarded", "answer", "cancel", "duplicate", "duplicate"}, historyEntryIDs(entries))

	require.Len(t, entries[0].ToolCalls, 1, "settled tool call")
	call := entries[0].ToolCalls[0]
	require.Equal(t, "call", string(call.ID))
	require.Equal(t, "read_file", call.Tool)
	require.Equal(t, "success", call.Status, "settled tool call: guarded success sticks")
	require.NotEqual(t, "denied", call.Status, "mismatching settle must not replace the guarded success")
	require.NotEqual(t, "pending", call.Status, "settled tool call must not stay pending")
	require.Equal(t, "capped", call.ContentState, "capped projection")
	require.Equal(t, map[string]any{"text": "short"}, call.Result, "retract drops the emptied projection and leaves the capped text")
	require.NotEqual(t, "gone", call.Result["text"], "retracted emptied text must not remain")
	require.NotEqual(t, "actual", call.Result["text"], "capped projection replaces the settled result text")
	require.NotEqual(t, "original", call.Result["text"], "settle replaces the pending result before the cap")

	discarded := entries[1].InputDisposition
	require.NotNil(t, discarded, "discarded input")
	// Literals, not production constants. Message.yaml input_disposition enums
	// fix state "discarded" and reason "stopped_before_delivery" (D-P1).
	require.Equal(t, InputDisposition{
		MessageID: "discarded", ClientMessageID: "client-u",
		State: "discarded", Reason: "stopped_before_delivery",
	}, *discarded)
	require.Nil(t, entries[0].InputDisposition, "a settled tool call is not a discarded input")
	require.Nil(t, entries[4].InputDisposition, "the older duplicate is not discarded")
	require.Nil(t, entries[5].InputDisposition, "the canonical duplicate is not discarded")

	require.True(t, entries[2].Truncated, "cancel truncation")
	require.Equal(t, "cancelled", entries[2].TruncationReason)
	require.Equal(t, "partial", entries[2].Content, "cancel marks the answer; it does not rewrite its text")
	require.False(t, entries[0].Truncated, "cancel truncation does not mark the earlier tool call")
	require.Equal(t, EntryTypeTurnCancelled, entries[3].Type)
	require.Equal(t, "T2", entries[3].TurnID)

	require.Equal(t, "older", entries[4].Content, "duplicate ids: the older occurrence stays addressable")
	require.Equal(t, "canonical newest", entries[5].Content, "duplicate ids: the last occurrence keeps its own text")
}

func historyEntries(page ChatHistoryPage) []TranscriptEntry {
	out := make([]TranscriptEntry, len(page.Entries))
	for i, e := range page.Entries {
		out[i] = e.Entry
	}
	return out
}

func historyEntryIDs(entries []TranscriptEntry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}

// canonicalFileBytes is the D-P1 "bytes unchanged" oracle: every file outside
// the derived index, compared by content. Directory mtimes are not part of it.
func canonicalFileBytes(t *testing.T, sessionDir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(sessionDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ChatHistoryIndexDirectory {
				return filepath.SkipDir
			}
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(sessionDir, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = string(b)
		return nil
	})
	require.NoError(t, err)
	require.Contains(t, out, "meta.json", "canonical snapshot must be able to see a real file")
	require.NotEmpty(t, out)
	return out
}

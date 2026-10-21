package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func repairExpectedPosition(sid string, a session.ArchiveAddress) session.HistoryPosition {
	h := sha256.New()
	for _, s := range []string{sid, a.PartitionKey, strconv.FormatInt(a.ByteOffset, 10), a.EntryID} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len([]byte(s))))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(s))
	}
	return session.HistoryPosition("hp1_" + base64.RawURLEncoding.EncodeToString(h.Sum(nil)))
}

// T25/D-GO1/D-P1: shared offline reconstruction is checked by real bounded
// projection plus hand-derived corrections, disposition, cancellation and
// duplicate ownership; no full reader or product encoder supplies the oracle.
func TestHistoryRepairCommand_SharedProjectionEffectAndIdentityParity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := session.NewUnifiedStore(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	meta, err := s.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	owner, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "owner", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: at, ToolCalls: []session.ToolCall{{ID: "call", Tool: "read_file", Status: "pending", Result: map[string]any{"text": "original"}}}})
	require.NoError(t, err)
	_, err = s.SettleToolCall(sid, owner, "pending", session.ToolCall{ID: "call", Tool: "read_file", Status: "success", Result: map[string]any{"text": "actual"}})
	require.NoError(t, err)
	_, err = s.SettleToolCall(sid, owner, "pending", session.ToolCall{ID: "call", Tool: "read_file", Status: "denied"})
	require.NoError(t, err)
	_, err = s.ProjectToolCalls(sid, []session.ToolCallProjectionEdit{{Target: owner, ToolCallID: "call", ContentState: "capped", Text: "short"}})
	require.NoError(t, err)
	p2, err := s.ProjectToolCalls(sid, []session.ToolCallProjectionEdit{{Target: owner, ToolCallID: "call", ContentState: "emptied", Text: "gone"}})
	require.NoError(t, err)
	_, err = s.RetractToolCallEffects(sid, []session.ArchiveAddress{p2})
	require.NoError(t, err)
	var duplicate session.ArchiveAddress
	for _, e := range []session.TranscriptEntry{{ID: "discarded", Role: "user", Content: "not delivered", ClientMessageID: "client-u"}, {ID: "answer", Role: "assistant", Content: "partial", TurnID: "T2"}, {ID: "cancel", Type: session.EntryTypeTurnCancelled, TurnID: "T2", CancelMethod: "graceful"}, {ID: "duplicate", Role: "user", Content: "older"}, {ID: "duplicate", Role: "user", Content: "canonical newest"}} {
		e.AgentID = "mia"
		e.Timestamp = at
		a, appendErr := s.AppendTranscriptAddressed(sid, e)
		require.NoError(t, appendErr)
		if e.ID == "duplicate" {
			duplicate = a
		}
	}
	require.NoError(t, s.RecordInputDiscarded(sid, "discarded", "client-u"))
	require.NoError(t, s.Close())
	before := repairCanonicalFacts(t, root)
	dir := filepath.Join(root, "sessions", sid)
	require.NoError(t, session.VerifyChatHistoryIndex(ctx, dir), "healthy real writer before deleting only derived metadata")
	require.NoError(t, os.RemoveAll(filepath.Join(dir, session.ChatHistoryIndexDirectory)))
	build, err := session.PrepareChatHistoryIndex(ctx, dir, session.ChatHistoryIndexBuildOptions{})
	require.NoError(t, err, "actual shared reconstruction engine")
	require.NotNil(t, build)
	require.NoError(t, build.Publish(ctx))
	require.NoError(t, build.Close())
	require.NoError(t, session.VerifyChatHistoryIndex(ctx, dir))
	reopened, err := session.NewUnifiedStore(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	weigh := func(_ context.Context, p session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
		n := int64(512 + 128*len(p.Entries))
		return session.ChatHistoryWeight{RESTBytes: n, WSBytes: n, ProjectedBytes: n}, nil
	}
	page, err := reopened.ReadChatHistoryPage(ctx, sid, session.ChatHistoryPageOptions{Limit: 100, Weigh: weigh})
	require.NoError(t, err)
	byID := map[string]session.TranscriptEntry{}
	for _, e := range page.Entries {
		byID[e.Entry.ID] = e.Entry
	}
	require.Contains(t, byID, "owner")
	require.Len(t, byID["owner"].ToolCalls, 1)
	call := byID["owner"].ToolCalls[0]
	require.Equal(t, "success", call.Status)
	require.Equal(t, "capped", call.ContentState)
	require.Equal(t, map[string]any{"text": "short"}, call.Result)
	discard := byID["discarded"].InputDisposition
	require.NotNil(t, discard)
	require.Equal(t, "discarded", discard.State)
	require.Equal(t, "client-u", discard.ClientMessageID)
	require.True(t, byID["answer"].Truncated)
	require.Equal(t, "cancelled", byID["answer"].TruncationReason)
	require.Equal(t, "canonical newest", byID["duplicate"].Content)
	found := false
	for _, d := range page.PageInfo.Context.DuplicateIDs {
		if d.ID == "duplicate" {
			found = true
			require.Equal(t, "message", d.Kind)
			require.Equal(t, 2, d.OccurrenceCount)
			require.Equal(t, repairExpectedPosition(sid, duplicate), d.CanonicalPosition)
		}
	}
	require.True(t, found, "bounded duplicate diagnostics from actual offline reducer")
	require.Equal(t, before, repairCanonicalFacts(t, root), "effects/reconstruction never rewrite accepted bytes or identities")
	binary := repairBinary(t)
	out := repairRun(t, binary, 0, "dry-run", "--data-root", root)
	require.Contains(t, out, "unchanged")
}

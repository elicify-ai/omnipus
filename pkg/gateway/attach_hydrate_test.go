// Package gateway — ADR-066 D5.5 (US-15): opening a session must never
// rewrite the per-agent archive.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestAttach_TwiceArchiveByteIdentical — FR-045 / B-52 / DS-10 #5 (test 55).
//
// Given an agent archive with lines (21 user / 53 assistant / 36 tool, Skip
// advanced by a trim), when the WS attach_session path runs twice for that
// session, then the archive file is byte-identical and the window cursor is
// unchanged. The hydration step this used to guard is deleted (DEL-12); the
// archive now lives at the transcript path under the owning session id, and
// opening a session rewrites nothing (FR-006).
func TestAttach_TwiceArchiveByteIdentical(t *testing.T) {
	const agentID = "mia"
	handler, _, al := newTestWSHandlerWithAgent(t, agentID)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID: "u1", Role: "user", Content: "hello", AgentID: agentID, TurnID: "T1", Timestamp: now,
	}))
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID: "a1", Role: "assistant", Content: "hi", AgentID: agentID, TurnID: "T1", Timestamp: now.Add(time.Second),
	}))

	ag, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok)
	key := fmt.Sprintf("agent:%s:session:%s", agentID, meta.ID)

	// A real archive written by the turn path: 21 user / 53 assistant / 36
	// tool lines (the operator's 08-21 snapshot), then a trim so Skip > 0.
	user, assistant, tool := 0, 0, 0
	for i := 0; i < 21; i++ {
		ag.Sessions.AddMessage(key, "user", fmt.Sprintf("user %d", i))
		user++
		ag.Sessions.AddMessage(key, "assistant", fmt.Sprintf("assistant %d", i))
		assistant++
		// 16 turns carry two tool-call steps each (32 assistant lines); the
		// first 4 turns' second step issues two parallel calls (36 results).
		if i < 16 {
			for j := 0; j < 2; j++ {
				ids := []string{fmt.Sprintf("call-%d-%d", i, j)}
				if j == 1 && i < 4 {
					ids = append(ids, fmt.Sprintf("call-%d-%d-b", i, j))
				}
				ag.Sessions.AddFullMessage(key, providersToolCallMessage(ids...))
				assistant++
				for _, id := range ids {
					ag.Sessions.AddFullMessage(key, providersToolResultMessage(id))
					tool++
				}
			}
		}
	}
	require.Equal(t, 21, user)
	require.Equal(t, 53, assistant)
	require.Equal(t, 36, tool)
	gwTruncateWindowTo(t, ag.Sessions, key, 40)
	require.NoError(t, ag.Sessions.Save(key))

	// The one archive lives at the transcript path of the owning session (the
	// routing key embeds it: agent:<id>:session:<sid>).
	sessionDir := filepath.Join(ag.Home, "sessions", meta.ID)
	archivePath := filepath.Join(sessionDir, "transcript.jsonl")
	metaPath := filepath.Join(sessionDir, "backend_meta.json")
	bytesBefore, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	require.Equal(t, 110, strings.Count(string(bytesBefore), "\n"))
	skipBefore := readSkip(t, metaPath)
	require.Equal(t, 70, skipBefore)

	attach := func(chatID string) {
		wc := &wsConn{
			sendCh: make(chan []byte, 2048),
			doneCh: make(chan struct{}),
		}
		handler.handleAttachSession(context.Background(), chatID, meta.ID, nil, wc)
		close(wc.sendCh)
		for raw := range wc.sendCh {
			var f replayFrameDecoder
			if json.Unmarshal(raw, &f) == nil {
				require.NotEqual(t, "error", f.Type, "attach must not error: %s", raw)
			}
		}
	}
	attach("chat-attach-1")
	attach("chat-attach-2")

	bytesAfter, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	assert.Equal(t, string(bytesBefore), string(bytesAfter),
		"attaching twice must leave the archive byte-identical (FR-045)")
	assert.Equal(t, skipBefore, readSkip(t, metaPath), "attach must not move the window cursor")
	assert.Len(t, ag.Sessions.GetHistory(key), 40, "window unchanged")
}

// gwTruncateWindowTo keeps only the last keepLast live messages by moving the
// window start with one compare-and-set commit — the replacement for the
// deleted TruncateHistory (DEL-12): no archive byte changes.
func gwTruncateWindowTo(t *testing.T, store session.SessionStore, key string, keepLast int) {
	t.Helper()
	cw, ok := store.(session.ContextWindowStore)
	require.True(t, ok, "the session store supports the checkpoint seam")
	ctx := context.Background()
	view, err := cw.WindowView(ctx, key)
	require.NoError(t, err)
	after := view.State.Clone()
	if keepLast <= 0 {
		after.Skip = after.Count
	} else if effective := after.Count - after.Skip; keepLast < effective {
		after.Skip = after.Count - keepLast
	}
	for k := range after.Projection.Entries {
		if k.ArchiveLine < after.Skip {
			delete(after.Projection.Entries, k)
			delete(after.Projection.SourceRunes, k)
		}
	}
	for k := range after.Projection.TranscriptAddr {
		if k.ArchiveLine < after.Skip {
			delete(after.Projection.TranscriptAddr, k)
		}
	}
	require.NoError(t, cw.CommitWindow(ctx, key, view.State, after))
}

func readSkip(t *testing.T, metaPath string) int {
	t.Helper()
	b, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	var m struct {
		Skip int `json:"skip"`
	}
	require.NoError(t, json.Unmarshal(b, &m))
	return m.Skip
}

func providersToolCallMessage(ids ...string) providers.Message {
	msg := providers.Message{Role: "assistant"}
	for _, id := range ids {
		msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
			ID: id, Type: "function",
			Function: &providers.FunctionCall{Name: "bash", Arguments: `{"cmd":"true"}`},
			Name:     "bash",
		})
	}
	return msg
}

func providersToolResultMessage(id string) providers.Message {
	return providers.Message{Role: "tool", ToolCallID: id, Content: `{"output":"ok"}`}
}

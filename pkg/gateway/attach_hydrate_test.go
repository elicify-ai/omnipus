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
// session, then the archive file is byte-identical and meta.skip is unchanged.
// Before D5.5 every attach rebuilt the archive from the transcript via
// SetHistory — dropping every tool line and resetting skip to 0.
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
	ag.Sessions.TruncateHistory(key, 40)
	require.NoError(t, ag.Sessions.Save(key))

	archivePath := filepath.Join(ag.Home, "sessions", ".context",
		strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(key)+".jsonl")
	metaPath := strings.TrimSuffix(archivePath, ".jsonl") + ".meta.json"
	bytesBefore, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	require.Equal(t, 110, strings.Count(string(bytesBefore), "\n"))
	skipBefore := readSkip(t, metaPath)
	require.Equal(t, 70, skipBefore)

	attach := func(chatID string) {
		wc := &wsConn{
			sendCh:         make(chan []byte, 2048),
			doneCh:         make(chan struct{}),
			replayDivertCh: make(chan []byte, replayLiveBufferCap),
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
	assert.Equal(t, skipBefore, readSkip(t, metaPath), "attach must not move meta.skip")
	assert.False(t, ag.Sessions.Projection(key).Hydrated,
		"a skipped hydration must not flag the archive as hydrated")
	assert.Len(t, ag.Sessions.GetHistory(key), 40, "window unchanged")
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

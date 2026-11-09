package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Raw JSON is intentional test input, not a parallel wire carrier: today's
// generated CancelFrame cannot represent shown. Send the actual proposed bytes
// without editing the production contract or silently losing the new field.
// Oracle: /workspace/logs/1534/arch-decision.md §§1,3,5.
func stop1534CancelFromTokens(t *testing.T, sid string, frames [][]byte) []byte {
	t.Helper()
	var messageID, turnID, text string
	for _, raw := range frames {
		if historyD16Type(t, raw) != "token" {
			continue
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		require.NotNil(t, frame.MessageId)
		require.NotNil(t, frame.TurnId)
		if messageID != *frame.MessageId {
			messageID, text = *frame.MessageId, ""
		}
		turnID = *frame.TurnId
		if frame.Replace != nil && *frame.Replace {
			text = frame.Content
		} else {
			text += frame.Content
		}
	}
	require.NotEmpty(t, messageID, "checkpoint identity must come from received tokens")
	require.NotEmpty(t, turnID, "checkpoint turn must be known BEFORE Stop/done")
	raw, err := json.Marshal(map[string]any{
		"type": "cancel", "session_id": sid,
		"shown": map[string]any{"message_id": messageID, "turn_id": turnID, "code_points": utf8.RuneCountInString(text)},
	})
	require.NoError(t, err)
	t.Logf("pre_stop_wire_cancel=%s received_snapshot=%q", raw, text)
	return raw
}

// The legacy Stop reader deliberately tolerates error notices while waiting for
// done. This checkpoint-specific reader admits ONLY the canonical stopped-turn
// notice, so a schema-rejected shown fails at its real error frame, not a timeout.
func stop1534ReadCheckpointDone(t *testing.T, peer *websocket.Conn) [][]byte {
	t.Helper()
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(30*time.Second)))
	var frames [][]byte
	for {
		_, raw, err := peer.ReadMessage()
		require.NoError(t, err, "checkpoint Stop must reach a real error/done boundary")
		frames = append(frames, raw)
		switch historyD16Type(t, raw) {
		case "error":
			var frame generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			require.NotNil(t, frame.Payload, "§3/§5: valid shown must not be refused: %s", frame.Message)
			require.Equal(t, string(agent.CodeTurnCanceled), frame.Payload.LlmError.Code,
				"§3/§5: only the canonical cancellation notice is permitted")
			require.Equal(t, agent.UserMessageForCode(agent.CodeTurnCanceled), frame.Payload.LlmError.Message)
		case "done":
			return frames
		}
	}
}

func Test1534BrowserStop_CheckpointIngress(t *testing.T) {
	for _, tc := range []struct {
		name, prefix     string
		validate, legacy bool
	}{
		// 13 code points: ï=1, each Japanese character=1, 😀=1, e+◌́=2.
		{"unicode_emoji_and_combining_mark", "naïve 名前 😀 é", false, false},
		{"shown_with_inbound_validation", stop1534Prefix, true, false},
		{"compatibility_without_shown_uses_receipt_cut", stop1534Prefix, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, cfg := historyD16Config(t)
			cfg.Gateway.ValidateInbound = tc.validate
			provider := &stop1534TransitProvider{stop: true, prefix: tc.prefix, next: make(chan struct{}), finish: make(chan struct{})}
			h := newHistoryD16Harness(t, home, cfg, provider, true)
			sid := h.session()
			peer, gate := stop1534Connect(t, h)
			h.attach(peer, sid)
			h.send(peer, sid, "1534-shown-ingress", "continue an answer")
			before := stop1534ReadToken(t, h, peer)
			shown := stopBodyParityParseLive(t, before)
			want := strings.Join(shown.tokenContents, "")
			require.Equal(t, tc.prefix, want)
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(before[len(before)-1], &token))
			require.NotNil(t, token.TurnId)
			cancelJSON := stop1534CancelFromTokens(t, sid, before)
			if tc.legacy {
				var err error
				cancelJSON, err = json.Marshal(generated.CancelFrame{Type: "cancel", SessionId: sid})
				require.NoError(t, err)
			}
			gate.armed.Store(true)
			sent := make(chan error, 1)
			go func() { sent <- peer.WriteMessage(websocket.TextMessage, cancelJSON) }()
			select {
			case <-gate.held:
			case <-time.After(30 * time.Second):
				t.Fatal("SETUP: cancel did not reach held network write")
			}
			close(provider.next)
			transit := stopBodyParityParseLive(t, stop1534ReadToken(t, h, peer))
			require.Equal(t, []string{stop1534InFlight}, transit.tokenContents)
			require.Equal(t, shown.liveMessageID, transit.liveMessageID)
			gate.open()
			require.NoError(t, <-sent)
			terminal := stopBodyParityParseLive(t, stop1534ReadCheckpointDone(t, peer))
			require.Equal(t, *token.TurnId, terminal.turnID)
			historyD16AwaitCancelRecord(t, h, sid, terminal.turnID)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			require.True(t, h.loop.WaitForActiveRequestsContext(ctx))
			assert.Equal(t, int32(1), provider.calls.Load(), "Stop ends the whole turn")
			require.NoError(t, peer.Close())
			if tc.legacy {
				// §2 compatibility, not the founder's screen-boundary promise.
				// This fixture guarantees the second delta precedes server receipt.
				want += strings.Join(transit.tokenContents, "")
			}
			stop1534AssertReadback(t, h, sid, terminal.turnID, shown.liveMessageID, want, true)
		})
	}
}

func stop1534SetLimit(t *testing.T, streamer *wsStreamer, keep int) {
	t.Helper()
	setter, ok := any(streamer).(interface{ SetStopLimit(keep int) })
	if !ok {
		t.Fatal("BLOCKED: wsStreamer.SetStopLimit not implemented — required by #1534 architect decision §4 W1/W2")
	}
	setter.SetStopLimit(keep)
}

func Test1534Shown_PersistTranscriptCutsCodePoints(t *testing.T) {
	for _, tc := range []struct {
		name, accumulated, continuation, want string
		keep                                  int
	}{
		{"accumulated", "though we name unseen", "", "though we name", 14},
		{"continuation_whole_message", "second unseen", "first 😀 second unseen", "first 😀", 7},
		{"unicode_not_bytes_or_utf16", "naïve 名前 😀 éTAIL", "", "naïve 名前 😀 é", 13},
		{"zero_keeps_nothing", "unseen", "", "", 0},
		{"one_code_point", "😀é", "", "😀", 1},
		{"exact_length", "😀é", "", "😀é", 3},
		{"ahead_never_adds_text", "😀é", "", "😀é", 4},
		{"invalid_utf8_byte_counts_one", "a\xff\xfeb€😀TAIL", "", "a\xff\xfeb€😀", 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, store, sid := newStopParityStreamer(t, "1534-shown-writer", "turn-shown", "message-shown")
			require.NoError(t, s.Update(context.Background(), tc.accumulated))
			s.CutoffAtStop()
			stop1534SetLimit(t, s, tc.keep)
			if tc.continuation != "" {
				s.SetContinuationContent(tc.continuation)
			}
			require.NoError(t, s.Finalize(context.Background(), "UNSEEN-FINAL"))
			entries := stopParityAssistantEntries(t, store, sid)
			if tc.want == "" {
				assert.Empty(t, entries, "zero checkpoint must not fall back to final content")
				return
			}
			require.Len(t, entries, 1, "one original message, never a second outcome")
			// JSON replaces invalid UTF-8 bytes with one U+FFFD per byte.
			var wireText string
			raw, err := json.Marshal(tc.want)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &wireText))
			assert.Equal(t, wireText, entries[0].Content, "persistTranscript must cut the whole message by code points")
			assert.Equal(t, "message-shown", entries[0].ID)
			assert.Equal(t, "turn-shown", entries[0].TurnID)
		})
	}
}

func Test1534Shown_PersistTranscriptNoDuplicateNarration(t *testing.T) {
	s, store, sid := newStopParityStreamer(t, "1534-narration", "turn-narration", "message-narration")
	prior := session.TranscriptEntry{ID: "message-narration", TurnID: "turn-narration", Role: "assistant", Content: "already saved lead-in"}
	require.NoError(t, store.AppendTranscriptStrict(sid, prior))
	s.SuppressTranscriptWrite()
	require.NoError(t, s.Update(context.Background(), prior.Content))
	s.CutoffAtStop()
	stop1534SetLimit(t, s, 0)
	require.NoError(t, s.Finalize(context.Background(), "new terminal tail"))
	entries := stopParityAssistantEntries(t, store, sid)
	require.Len(t, entries, 1, "Stop must not derive pendingFinalContent or create a second entry (§2)")
	assert.Equal(t, prior.ID, entries[0].ID)
	// Deliberately do not assert prior body length: founder Q1 is open.
}

func Test1534Shown_PersistTranscriptWithZeroViewers(t *testing.T) {
	s, store, sid := newStopParityStreamer(t, "1534-no-viewers", "turn-no-viewers", "message-no-viewers")
	// No socket was ever connected to this real handler: GetStreamer/Update/
	// Finalize still run, and persistence must not depend on a viewing tab.
	require.NoError(t, s.Update(context.Background(), "VISIBLE unseen"))
	s.CutoffAtStop()
	stop1534SetLimit(t, s, 7)
	require.NoError(t, s.Finalize(context.Background(), "VISIBLE unseen final"))
	entries := stopParityAssistantEntries(t, store, sid)
	require.Len(t, entries, 1)
	assert.Equal(t, "VISIBLE", entries[0].Content, "zero viewers must not bypass the client checkpoint")
}

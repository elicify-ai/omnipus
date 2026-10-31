// chat_history_1534_stop_body_parity_test.go: #1534 RED — end-to-end parity
// row. A real engine, real gateway, real hub and real transcript store; only
// the provider boundary is a stub. The stub streams "part-1", waits until the
// production Stop has reached its context, then streams "part-2" and ends
// cancelled — so "part-2" is delivered strictly AFTER the server claimed the
// Stop (cancel.go's RequestCancel claims the turn before
// interruptCancelTargets fires providerCancel), exactly the window the
// founder decision says must never be saved. The client Stops after "part-1"
// with a real cancel frame; the reload happens after the turn_canceled record
// is durably saved (bounded wait — the #1524 lesson).
//
// Founder decision 2026-10-31 (option A): after any reload the stopped answer
// body equals the body the browser showed at Stop, with the same "interrupted"
// label as today.
package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stopBodyParityProvider is the external-model transport edge only: it streams
// "part-1", blocks until the Stop's provider cancel reaches its context, then
// delivers "part-2" (as accumulated content — the engine's callback forwards
// the delta) and ends with the cancellation error. Every other unit in the
// flow is real.
type stopBodyParityProvider struct{}

func (*stopBodyParityProvider) GetDefaultModel() string { return "d16-frozen-model" }

func (p *stopBodyParityProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	return nil, context.Canceled // this fixture always streams
}

func (p *stopBodyParityProvider) ChatStream(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	onChunk("part-1")
	// <-ctx.Done() fires when interruptCancelTargets calls providerCancel —
	// strictly AFTER claimActiveTurn claimed the Stop. Anything delivered from
	// here on is delivered after the server claimed the Stop.
	<-ctx.Done()
	onChunk("part-1part-2")
	return nil, context.Canceled
}

var _ providers.StreamingProvider = (*stopBodyParityProvider)(nil)

// stopBodyParityReadToDone reads the peer until the stopped turn's done frame
// and returns every raw frame in between. Unlike the shared D16 readUntil —
// whose blanket "no error frames" guard is for flows that never read across a
// Stop — a mid-stream Stop legitimately delivers the production
// turn_canceled error notice ("This turn was stopped before it finished.")
// before done; that notice is part of the unchanged UX this row must keep.
func stopBodyParityReadToDone(t *testing.T, h *historyD16Harness, peer *websocket.Conn) [][]byte {
	t.Helper()
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(30*time.Second)))
	frames := make([][]byte, 0)
	for {
		_, raw, err := peer.ReadMessage()
		require.NoError(t, err, "the real socket must reach the stopped turn's done frame")
		frames = append(frames, raw)
		if historyD16Type(t, raw) == string(generated.WsFrameTypeDone) {
			return frames
		}
	}
}

// stopBodyParityLive is the parsed live half of the row: token deliveries,
// the live bubble's message id, the turn id, and whether the stopped turn's
// user-visible error notice arrived.
type stopBodyParityLive struct {
	tokenContents  []string
	liveMessageID  string
	turnID         string
	canceledNotice bool
}

func stopBodyParityParseLive(t *testing.T, raws [][]byte) stopBodyParityLive {
	t.Helper()
	var live stopBodyParityLive
	for _, raw := range raws {
		switch historyD16Type(t, raw) {
		case string(generated.WsFrameTypeToken):
			var frame generated.TokenFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			live.tokenContents = append(live.tokenContents, frame.Content)
			if frame.MessageId != nil {
				live.liveMessageID = *frame.MessageId
			}
		case string(generated.WsFrameTypeDone):
			var frame generated.DoneFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame.TurnId != nil {
				live.turnID = *frame.TurnId
			}
		case "error":
			// Characterization (label-parity half): the stopped turn's
			// user-visible notice, unchanged by the fix.
			var frame struct {
				Payload struct {
					LLMError struct {
						Code string `json:"code"`
					} `json:"llm_error"`
				} `json:"payload"`
			}
			if json.Unmarshal(raw, &frame) == nil && frame.Payload.LLMError.Code == "turn_canceled" {
				live.canceledNotice = true
			}
		}
	}
	return live
}

// stopBodyParityReplayAssistant joins the assistant bodies a reload replays.
func stopBodyParityReplayAssistant(t *testing.T, raws [][]byte) (string, string) {
	t.Helper()
	body := ""
	id := ""
	for _, raw := range raws {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeReplayMessage) {
			continue
		}
		var frame generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Role != "assistant" {
			continue
		}
		body += frame.Content
		if frame.Id != nil {
			id = *frame.Id
		}
	}
	return body, id
}

// TestStopBodyParity_ReloadShowsBodyShownAtStop is the #1534 acceptance row:
// the user Stops after "part-1"; "part-2" is delivered afterwards (the
// browser freezes and discards it); a reload must show "part-1" — the body
// shown at Stop — still labelled interrupted (the turn_canceled record saved
// and correlated by turn id), never "part-1part-2".
func TestStopBodyParity_ReloadShowsBodyShownAtStop(t *testing.T) {
	home, cfg := historyD16Config(t)
	provider := &stopBodyParityProvider{}
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "1534-parity-instruction", "count to two, slowly")

	// The browser shows "part-1" — the last live delivery before the Stop.
	preStopRaws := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeToken) {
			return false
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.Content == "part-1"
	})

	// The client Stops: the production web-SPA Stop frame, through the real
	// dispatcher → RequestCancel → claimActiveTurn → interruptCancelTargets.
	require.NoError(t, peer.WriteJSON(generated.CancelFrame{Type: "cancel", SessionId: sid}))

	// Read to the turn's done, collecting every live delivery in between:
	// "part-2" must still arrive (frames unchanged; the browser discards it),
	// and the turn_canceled error notice must reach the client (unchanged UX).
	// The pre-Stop frames (part-1 included) join the same collection.
	raws := append(preStopRaws, stopBodyParityReadToDone(t, h, peer)...)
	live := stopBodyParityParseLive(t, raws)
	require.NotEmpty(t, live.turnID, "the done frame carries the stopped turn's id")
	assert.Contains(t, live.tokenContents, "part-1", "the pre-Stop body was delivered live")
	assert.Contains(t, live.tokenContents, "part-2",
		"frames are unchanged by the fix: the post-Stop delta is still published live; the browser discards it")
	assert.True(t, live.canceledNotice,
		"the stopped turn's turn_canceled error notice must still reach the client (unchanged UX)")

	// #1524 lesson: the turn_canceled record is saved after the done frame —
	// wait, bounded, before any reload-style readback.
	historyD16AwaitCancelRecord(t, h, sid, live.turnID)

	// Reload: a fresh connection attaches and replays the saved transcript.
	peer2 := h.connect()
	replayRaws := h.attach(peer2, sid)
	body, replayID := stopBodyParityReplayAssistant(t, replayRaws)
	assert.Equal(t, "part-1", body,
		"after a reload the stopped answer body must equal the body the browser showed at Stop (#1534); got %q", body)
	assert.Equal(t, live.liveMessageID, replayID,
		"the replayed entry must keep the live bubble's message id so the reload replaces the same message")

	// Label-parity preconditions on the saved transcript: the assistant entry
	// and the turn_canceled record share the turn id the SPA correlates to
	// render the existing "interrupted" label.
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	assistantWithTurn := 0
	cancelRecords := 0
	var savedBody string
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.TurnID == live.turnID {
			assistantWithTurn++
			savedBody = entry.Content
		}
		if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == live.turnID {
			cancelRecords++
		}
	}
	assert.Equal(t, 1, assistantWithTurn, "exactly one assistant entry belongs to the stopped turn")
	assert.Equal(t, 1, cancelRecords, "the stopped turn saves exactly one turn_canceled record — the interrupted label's source")
	assert.Equal(t, "part-1", savedBody,
		"the saved stopped answer must equal the body the browser showed at Stop (#1534); got %q", savedBody)
}

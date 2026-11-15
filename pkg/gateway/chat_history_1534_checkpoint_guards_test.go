package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1534 architect decision §2 / CHECK S1f: a checkpoint for a different
// turn is unusable, even when it has the valid empty-placeholder shape. With
// no message ID and keep=0, erasing the turn ID would wrongly erase this answer.
func Test1534BrowserStop_OtherTurnCheckpointUsesReceiptCut(t *testing.T) {
	home, cfg := historyD16Config(t)
	cfg.Gateway.ValidateInbound = true
	provider := &stop1534TransitProvider{stop: true, next: make(chan struct{}), finish: make(chan struct{})}
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	sid := h.session()
	peer, _ := stop1534Connect(t, h)
	h.attach(peer, sid)
	h.send(peer, sid, "1534-other-turn-checkpoint", "continue an answer")
	before := stop1534ReadToken(t, h, peer)
	shown := stopBodyParityParseLive(t, before)
	require.Equal(t, []string{stop1534Prefix}, shown.tokenContents)
	var token generated.TokenFrame
	require.NoError(t, json.Unmarshal(before[len(before)-1], &token))
	require.NotNil(t, token.TurnId)
	require.NotEmpty(t, *token.TurnId)
	const oldTurn = "1534-not-the-live-turn"
	require.NotEqual(t, oldTurn, *token.TurnId, "the sent checkpoint must name a different turn")

	close(provider.next)
	transit := stopBodyParityParseLive(t, stop1534ReadToken(t, h, peer))
	require.Equal(t, []string{stop1534InFlight}, transit.tokenContents)
	require.Equal(t, shown.liveMessageID, transit.liveMessageID)
	// Both received chunks precede sending Stop, so they belong to the
	// receipt cut. This expectation is from the wire snapshot, not saved data.
	want := strings.Join(shown.tokenContents, "") + strings.Join(transit.tokenContents, "")
	require.NoError(t, peer.WriteJSON(map[string]any{
		"type": "cancel", "session_id": sid,
		"shown": map[string]any{"turn_id": oldTurn, "code_points": 0},
	}))
	terminal := stopBodyParityParseLive(t, stop1534ReadCheckpointDone(t, peer))
	require.Equal(t, *token.TurnId, terminal.turnID)
	historyD16AwaitCancelRecord(t, h, sid, terminal.turnID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.True(t, h.loop.WaitForActiveRequestsContext(ctx), "join the real stopped provider before readback")
	assert.Equal(t, int32(1), provider.calls.Load(), "Stop must not start another model round")
	require.NoError(t, peer.Close())
	stop1534AssertReadback(t, h, sid, terminal.turnID, shown.liveMessageID, want, true)
}

// #1534 architect decision §2 / CHECK L2,L3: an ahead warning contains only
// identity/count metadata, never the answer. The positive control tests the
// capture before an absent warning could otherwise look like privacy success.
func Test1534Shown_GatewayAheadWarningOmitsAnswerText(t *testing.T) {
	const answer = "PRIVATE_STOP_1534" // 17 ASCII code points; keep=18 is one ahead.
	const keep = 18
	s, store, sid := newStopParityStreamer(t, "1534-ahead-warning", "turn-ahead-warning", "message-ahead-warning")
	logs := captureSlogWarn(t)
	slog.Warn("1534-gateway-warning-positive-control")
	require.Contains(t, logs.String(), "1534-gateway-warning-positive-control", "the log instrument must see a real WARN")

	require.NoError(t, s.Update(context.Background(), answer))
	s.CutoffAtStop()
	stop1534SetLimit(t, s, keep)
	require.NoError(t, s.Finalize(context.Background(), "UNSEEN_FINAL_TAIL"))
	entries := stopParityAssistantEntries(t, store, sid)
	require.Len(t, entries, 1)
	assert.Equal(t, answer, entries[0].Content, "an ahead checkpoint must retain only the actual server text")

	var warnings []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "captured slog output must be structured JSON")
		if row["msg"] != "stop_checkpoint_ahead" {
			continue
		}
		assert.NotContains(t, line, answer, "the ahead warning must never include answer text")
		assert.NotContains(t, line, "UNSEEN_FINAL_TAIL", "the ahead warning must never include final answer text")
		delete(row, "time") // timestamp is incidental; all diagnostic fields are specified.
		warnings = append(warnings, row)
	}
	require.NotEmpty(t, warnings, "an ahead checkpoint must emit stop_checkpoint_ahead")
	for _, warning := range warnings {
		assert.Equal(t, map[string]any{
			"level": "WARN", "msg": "stop_checkpoint_ahead", "session_id": sid,
			"turn_id": "turn-ahead-warning", "message_id": "message-ahead-warning", "code_points": float64(keep),
		}, warning, "the warning must contain only the specified IDs and count, never an answer/text field")
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// The external model pauses its first response while a real web input joins.
// Later calls record inputs and finish, instead of answering the room twice.
type wu15LateWebProvider struct{ *wu15ScriptProvider }

func (p *wu15LateWebProvider) Chat(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any) (*providers.LLMResponse, error) {
	response, err := p.wu15ScriptProvider.Chat(ctx, msgs, defs, model, opts)
	if err != nil {
		return nil, err
	}
	if len(p.inputs()) > 1 {
		return &providers.LLMResponse{Content: "Finished including the late web input.", FinishReason: "stop"}, nil
	}
	response.Usage = &providers.UsageInfo{PromptTokens: 3000, TotalTokens: 3000}
	return response, nil
}

func TestWU15Rooms_WebInputJoiningLiveTurnChangesAllocationAtTurnEnd(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "room-input", "@Mia ROOM_INPUT_FIRST")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "room-input"), "mia")
	p := &wu15LateWebProvider{&wu15ScriptProvider{
		requestID: request.RequestId, answer: "room answer", entered: make(chan struct{}), release: make(chan struct{}),
	}}
	h.pricedProvider("mia", p)
	conn := h.connect()
	sid := h.main("mia")
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: sid}))
	wu15ReadFrame(t, conn, "catch_up_complete")
	h.startLoop()
	select {
	case <-p.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: the real room-received turn never entered the blocked provider")
	}
	firstInputs := p.inputs()
	require.Len(t, firstInputs, 1)
	for _, msg := range firstInputs[0] {
		require.NotContains(t, msg.Content, "LATE_WEB_INPUT", "instrument: web input was not present at turn start")
	}
	h.waitState(room.Id, request.RequestId, "working")
	agentID, clientID := "mia", "late-web"
	require.NoError(t, conn.WriteJSON(gen.MessageFrame{
		Type: "message", SessionId: &sid, AgentId: &agentID, ClientMessageId: &clientID,
		Content: "LATE_WEB_INPUT", Metadata: map[string]any{"workspace_id": wu15Workspace},
	}))
	for {
		var status gen.MessageStatusFrame
		require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "message_status"), &status))
		if status.ClientMessageId == clientID && status.State == "working" {
			break
		}
	}
	close(p.release)
	done := wu15ReadTurnDone(t, conn)
	calls := p.inputs()
	require.Len(t, calls, 2, "instrument: one live turn consumed the joined web input in its next provider call")
	seenLate := false
	for _, msg := range calls[1] {
		if msg.Role != "system" && strings.Contains(msg.Content, "LATE_WEB_INPUT") {
			seenLate = true
		}
	}
	require.True(t, seenLate, "§6.14: a merely queued/unconsumed web message is not an allocation input")
	require.NotNil(t, done.Stats.Cost)
	require.Greater(t, *done.Stats.Cost, float64(0))
	inputCost := int64(math.Round(*done.Stats.Cost * 1_000_000))
	require.Equal(t, int64(0), inputCost%2, "instrument: this fixture has an exact whole-micro-dollar half; Q-I fractional rounding is tested separately")
	h.waitMainSettled("mia")
	require.Equal(t, inputCost/2, h.snapshot(room.Id).AllocatedCostMicroUsd,
		"§6.14 / OR-MIN-006: weights are one room input out of two inputs consumed by turn end, not the full cost frozen at turn start")
}

var _ providers.LLMProvider = (*wu15LateWebProvider)(nil)

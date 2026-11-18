package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Architect §5: shown names step 1 while the server has already minted step 2.
// Only the tool/provider edges are controlled. No assertion on step 1's length:
// whether to cut already-saved text is founder Q1, deliberately unresolved here.
func Test1534Shown_ServerMovedToNextStepDiscardsLaterMessage(t *testing.T) {
	home, cfg := historyD16Config(t)
	// Custom fixture tools are not in the compressed shipped manifest. Use the
	// existing plain-definitions path so this real tool is actually offered.
	cfg.Tools.Manifest.Compressed = false
	tool := newStopWholeTurnGateTool("stop1534_checkpoint_step")
	provider := &stopWholeTurnE2EProvider{steps: []stopWholeTurnStep{
		{stream: "FIRST-STEP", content: "FIRST-STEP", toolCalls: []providers.ToolCall{stopWholeTurnToolCall("checkpoint-tool", tool.Name())}},
		{stream: "UNSEEN-SECOND-STEP", waitCancel: true, err: context.Canceled},
	}}
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	t.Cleanup(tool.letGo) // release the tool before the harness joins on failure
	stopWholeTurnRegisterTools(t, h, tool)
	sid := h.session()
	peer, gate := stop1534Connect(t, h)
	h.attach(peer, sid)
	h.send(peer, sid, "1534-checkpoint-next-step", "do two steps")
	before := stop1534ReadToken(t, h, peer)
	first := stopBodyParityParseLive(t, before)
	require.Equal(t, []string{"FIRST-STEP"}, first.tokenContents)
	cancelJSON := stop1534CancelFromTokens(t, sid, before)
	gate.armed.Store(true)
	sent := make(chan error, 1)
	go func() { sent <- peer.WriteMessage(websocket.TextMessage, cancelJSON) }()
	select {
	case <-gate.held:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: cancel write was not held")
	}
	select {
	case <-tool.started:
	case <-time.After(30 * time.Second):
		t.Fatal("SETUP: first step did not reach its real tool boundary")
	}
	tool.letGo()
	later := stopBodyParityParseLive(t, stop1534ReadToken(t, h, peer))
	require.Equal(t, []string{"UNSEEN-SECOND-STEP"}, later.tokenContents)
	require.NotEmpty(t, later.liveMessageID)
	require.NotEqual(t, first.liveMessageID, later.liveMessageID, "fixture must exercise a later minted message")
	gate.open()
	require.NoError(t, <-sent)
	terminal := stopBodyParityParseLive(t, stopBodyParityReadToDone(t, h, peer))
	historyD16AwaitCancelRecord(t, h, sid, terminal.turnID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.True(t, h.loop.WaitForActiveRequestsContext(ctx))
	assert.Equal(t, 2, provider.CallCount(), "no third provider round after Stop")
	assert.Equal(t, int32(1), tool.execs.Load(), "the pre-receipt tool executed once")
	require.NoError(t, peer.Close())
	page := h.page(sid, "")
	for _, entry := range page.Entries {
		if entry.Role != nil && *entry.Role == "assistant" && derefString(entry.Content) != "" {
			assert.NotEqual(t, later.liveMessageID, entry.Id, "REST must omit all text minted after shown.message_id")
			assert.NotContains(t, derefString(entry.Content), "UNSEEN-SECOND-STEP")
		}
	}
	replay := stopWholeTurnReplayAssistants(t, h.attach(h.connect(), sid))
	for _, entry := range replay {
		if entry.Content != "" {
			assert.NotEqual(t, later.liveMessageID, derefString(entry.Id), "fresh replay must omit the later message")
			assert.NotContains(t, entry.Content, "UNSEEN-SECOND-STEP")
		}
	}
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	cancelCount := 0
	for _, entry := range entries {
		if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == terminal.turnID {
			cancelCount++
		}
		if entry.Role == "assistant" && entry.Content != "" {
			assert.NotEqual(t, later.liveMessageID, entry.ID, "canonical transcript must not keep the later step")
			assert.NotContains(t, entry.Content, "UNSEEN-SECOND-STEP")
		}
	}
	assert.Equal(t, 1, cancelCount)
}

package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	stop1534SavedVisible = "Visible tool lead-in"
	stop1534SavedWhole   = "Visible tool lead-in plus narration saved before Stop arrived."
)

// Only the external provider edge is controlled. onChunk receives cumulative
// snapshots, as on the real streaming interface. The client freezes after the
// first snapshot; the second really streams and is saved when the tool starts.
type stop1534SavedNarrationProvider struct {
	stopWholeTurnE2EProvider
	tail     chan struct{}
	tailOnce sync.Once
}

func (p *stop1534SavedNarrationProvider) releaseTail() { p.tailOnce.Do(func() { close(p.tail) }) }

func (p *stop1534SavedNarrationProvider) ChatStream(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	step, err := p.next()
	if err != nil {
		return nil, err
	}
	onChunk(stop1534SavedVisible)
	select {
	case <-p.tail:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	onChunk(stop1534SavedWhole)
	return &providers.LLMResponse{Content: stop1534SavedWhole, ToolCalls: step.toolCalls, FinishReason: "tool_calls"}, nil
}

var _ providers.StreamingProvider = (*stop1534SavedNarrationProvider)(nil)

// Founder Q1 A, ta3-brief.md ADDENDUM: already-saved tool-step narration stays
// whole. This is an accepted-exception control, not a new RED promise. The
// original unsaved-body trimming and later-message exclusion tests are intact.
func Test1534Shown_Q1A_AlreadySavedToolNarrationStaysWhole(t *testing.T) {
	home, cfg := historyD16Config(t)
	cfg.Tools.Manifest.Compressed = false
	tool := newStopWholeTurnGateTool("stop1534_saved_narration")
	provider := &stop1534SavedNarrationProvider{tail: make(chan struct{})}
	provider.steps = []stopWholeTurnStep{{toolCalls: []providers.ToolCall{stopWholeTurnToolCall("saved-narration-call", tool.Name())}}}
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	t.Cleanup(tool.letGo)
	t.Cleanup(provider.releaseTail)
	stopWholeTurnRegisterTools(t, h, tool)
	sid := h.session()
	peer, gate := stop1534Connect(t, h)
	h.attach(peer, sid)
	h.send(peer, sid, "1534-q1a-saved-narration", "start the tool after narrating")
	before := stop1534ReadToken(t, h, peer)
	first := stopBodyParityParseLive(t, before)
	require.Equal(t, []string{stop1534SavedVisible}, first.tokenContents)
	cancelJSON := stop1534CancelFromTokens(t, sid, before)
	gate.armed.Store(true)
	sent := make(chan error, 1)
	go func() { sent <- peer.WriteMessage(websocket.TextMessage, cancelJSON) }()
	historyD16Await(t, gate.held, "actual Stop write is held after the visible prefix")
	provider.releaseTail()
	historyD16Await(t, tool.started, "real tool started after the entire narration was saved")
	started := h.event(func(event agent.Event) bool {
		payload, ok := event.Payload.(agent.ToolExecStartPayload)
		return event.Kind == agent.EventKindToolExecStart && ok && payload.SessionID == sid && string(payload.ToolCallID) == "saved-narration-call"
	})
	require.NotEmpty(t, started.Meta.SessionKey)
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	var savedBeforeStop []string
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content != "" {
			savedBeforeStop = append(savedBeforeStop, entry.Content)
		}
	}
	require.Equal(t, []string{stop1534SavedWhole}, savedBeforeStop, "instrument: full narration was durably saved before the held Stop can reach the server")
	gate.open()
	require.NoError(t, <-sent)
	h.readUntil(peer, func(raw []byte) bool { return historyD16Type(t, raw) == "cancel_stage" })
	tool.letGo() // only after the real server Stop claim, not a scheduling guess
	terminal := stopBodyParityParseLive(t, stopBodyParityReadToDone(t, h, peer))
	require.Equal(t, started.Meta.TurnID, terminal.turnID)
	historyD16AwaitCancelRecord(t, h, sid, terminal.turnID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.True(t, h.loop.WaitForActiveRequestsContext(ctx))
	require.NoError(t, peer.Close())

	var restBodies []string
	for _, entry := range h.page(sid, "").Entries {
		if entry.Role != nil && *entry.Role == "assistant" && derefString(entry.Content) != "" {
			restBodies = append(restBodies, derefString(entry.Content))
			assert.Equal(t, first.liveMessageID, entry.Id)
		}
	}
	assert.Equal(t, []string{stop1534SavedWhole}, restBodies, "Q1 A: reload keeps already-saved narration whole, not the shorter shown prefix")
	var replayBodies []string
	for _, entry := range stopWholeTurnReplayAssistants(t, h.attach(h.connect(), sid)) {
		if entry.Content != "" {
			replayBodies = append(replayBodies, entry.Content)
			assert.Equal(t, first.liveMessageID, derefString(entry.Id))
		}
	}
	assert.Equal(t, []string{stop1534SavedWhole}, replayBodies, "Q1 A: fresh replay preserves the same saved text")
	entries, err = h.store.ReadTranscript(sid)
	require.NoError(t, err)
	var canonicalBodies []string
	markers := 0
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content != "" {
			canonicalBodies = append(canonicalBodies, entry.Content)
			assert.Equal(t, first.liveMessageID, entry.ID)
		}
		if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == terminal.turnID {
			markers++
		}
	}
	assert.Equal(t, []string{stop1534SavedWhole}, canonicalBodies, "Q1 A: Stop never rewrites or duplicates the already-saved narration")
	var modelBodies []string
	for _, message := range h.store.GetHistory(started.Meta.SessionKey) {
		if message.Role == "assistant" && message.Content != "" {
			modelBodies = append(modelBodies, message.Content)
		}
	}
	assert.Equal(t, []string{stop1534SavedWhole}, modelBodies, "Q1 A: model memory retains the actual saved tool lead-in")
	assert.Equal(t, 1, markers)
	assert.Equal(t, 1, provider.CallCount(), "whole-turn Stop forbids another provider step")
	assert.Equal(t, int32(1), tool.execs.Load())
}

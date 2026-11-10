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
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
// turn_canceled error notice (agent.UserMessageForCode(agent.CodeTurnCanceled))
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
			var frame generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame.Payload != nil && frame.Payload.LlmError.Code == string(agent.CodeTurnCanceled) {
				assert.Equal(t, agent.UserMessageForCode(agent.CodeTurnCanceled), frame.Payload.LlmError.Message,
					"the stopped-turn notice must retain the canonical catalogue copy")
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

// ---------------------------------------------------------------------------
// #1534 RED round 2 — Q1 = A whole-turn rows on the same D16 harness
// (CHECK findings F1, F3, F2): Stop ends the WHOLE turn. After the claimed
// Stop there is no further provider round, no further tool call and no
// continuation text; the saved answer ends where the screen stopped.
// ---------------------------------------------------------------------------

// stopWholeTurnStep is one scripted streaming response for
// stopWholeTurnE2EProvider.
type stopWholeTurnStep struct {
	stream string // accumulated chunk delivered immediately via onChunk
	// waitCancel blocks until the Stop's providerCancel reaches the request
	// context (cancel.go claims the turn BEFORE interruptCancelTargets fires
	// providerCancel, so everything after this wait is post-claim).
	waitCancel       bool
	postCancelStream string // accumulated chunk delivered after the cancel wait
	content          string
	finishReason     string
	toolCalls        []providers.ToolCall
	err              error
}

// stopWholeTurnE2EProvider is the external-model transport edge for the
// whole-turn rows. It always streams; Chat is never on the live path.
type stopWholeTurnE2EProvider struct {
	mu    sync.Mutex
	steps []stopWholeTurnStep
	calls int
}

func (p *stopWholeTurnE2EProvider) next() (stopWholeTurnStep, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	var step stopWholeTurnStep
	have := idx < len(p.steps)
	if have {
		step = p.steps[idx]
	}
	p.mu.Unlock()
	if !have {
		return stopWholeTurnStep{}, fmt.Errorf(
			"stopWholeTurnE2EProvider: unscripted provider call %d (only %d scripted) — "+
				"Q1 = A forbids a provider round after the claimed Stop", idx+1, len(p.steps))
	}
	return step, nil
}

func (*stopWholeTurnE2EProvider) GetDefaultModel() string { return "d16-frozen-model" }

func (p *stopWholeTurnE2EProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *stopWholeTurnE2EProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	return nil, context.Canceled // this fixture always streams
}

func (p *stopWholeTurnE2EProvider) ChatStream(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	step, err := p.next()
	if err != nil {
		return nil, err
	}
	if step.stream != "" {
		onChunk(step.stream)
	}
	if step.waitCancel {
		<-ctx.Done()
	}
	if step.postCancelStream != "" {
		onChunk(step.postCancelStream)
	}
	if step.err != nil {
		return nil, step.err
	}
	return &providers.LLMResponse{Content: step.content, FinishReason: step.finishReason, ToolCalls: step.toolCalls}, nil
}

var _ providers.StreamingProvider = (*stopWholeTurnE2EProvider)(nil)

// stopWholeTurnGateTool blocks inside Execute until released — the running
// tool whose execution window the Stop is claimed in.
type stopWholeTurnGateTool struct {
	name       string
	started    chan struct{}
	release    chan struct{}
	startOnce  sync.Once
	releaseOne sync.Once
	execs      atomic.Int32
}

func newStopWholeTurnGateTool(name string) *stopWholeTurnGateTool {
	return &stopWholeTurnGateTool{name: name, started: make(chan struct{}), release: make(chan struct{})}
}

func (t *stopWholeTurnGateTool) Name() string { return t.name }
func (t *stopWholeTurnGateTool) Description() string {
	return "blocks until released (#1534 round 2 fixture)"
}
func (t *stopWholeTurnGateTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (t *stopWholeTurnGateTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (t *stopWholeTurnGateTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *stopWholeTurnGateTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	t.execs.Add(1)
	t.startOnce.Do(func() { close(t.started) })
	<-t.release
	return tools.SilentResult("executed " + t.name)
}

func (t *stopWholeTurnGateTool) letGo() { t.releaseOne.Do(func() { close(t.release) }) }

// stopWholeTurnCountTool counts executions and returns immediately — the
// QUEUED tool that Q1 = A says must never start once the Stop is claimed.
type stopWholeTurnCountTool struct {
	name  string
	execs atomic.Int32
}

func (t *stopWholeTurnCountTool) Name() string { return t.name }
func (t *stopWholeTurnCountTool) Description() string {
	return "counts executions (#1534 round 2 fixture)"
}
func (t *stopWholeTurnCountTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (t *stopWholeTurnCountTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (t *stopWholeTurnCountTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *stopWholeTurnCountTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	t.execs.Add(1)
	return tools.SilentResult("executed " + t.name)
}

func stopWholeTurnToolCall(id, name string) providers.ToolCall {
	return providers.ToolCall{
		ID:   id,
		Type: "function",
		Name: name,
		Function: &providers.FunctionCall{
			Name:      name,
			Arguments: "{}",
		},
		Arguments: map[string]any{},
	}
}

// stopWholeTurnRegisterTools grants and registers the fixtures on the real
// loop's "mia" agent (no-default-policy model: tools fail closed without an
// explicit agent-level grant).
func stopWholeTurnRegisterTools(t *testing.T, h *historyD16Harness, registered ...tools.Tool) {
	t.Helper()
	for _, tool := range registered {
		h.loop.RegisterTool(tool)
	}
	inst, ok := h.loop.GetRegistry().GetAgent("mia")
	require.True(t, ok, "SETUP: the harness agent must be registered")
	policies := map[string]config.ToolPolicy{}
	for _, tool := range registered {
		policies[tool.Name()] = config.ToolPolicyAllow
	}
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: policies})
}

// stopWholeTurnCancelRecordPresent reports (never fatals) whether the
// stopped turn's turn_canceled record is durably saved yet — bounded, for
// the #1524 ordering lesson.
func stopWholeTurnCancelRecordPresent(t *testing.T, h *historyD16Harness, sid, turn string) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		entries, err := h.store.ReadTranscript(sid)
		require.NoError(t, err)
		for _, entry := range entries {
			if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == turn {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stopWholeTurnReplayAssistants returns the assistant replay frames a fresh
// connection receives on attach.
func stopWholeTurnReplayAssistants(t *testing.T, raws [][]byte) []generated.ReplayMessageFrame {
	t.Helper()
	var assistant []generated.ReplayMessageFrame
	for _, raw := range raws {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeReplayMessage) {
			continue
		}
		var frame generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Role == "assistant" {
			assistant = append(assistant, frame)
		}
	}
	return assistant
}

// TestStopWholeTurnE2E_ToolBoundaryStopEndsTurnAtShownBody is the Q1 = A
// whole-turn row (CHECK F1): the user Stops while a tool executes; a second
// tool is still queued. The turn must END at the boundary — no wrap-up
// provider round, no queued tool run, no wrap-up text delivered or saved —
// and the reload shows exactly the body shown at Stop.
//
// RED today: the graceful stage skips the queued tool but then runs ONE MORE
// provider round whose wrap-up answer streams through a fresh, uncut
// streamer (delivered live as a new bubble) and is saved.
func TestStopWholeTurnE2E_ToolBoundaryStopEndsTurnAtShownBody(t *testing.T) {
	home, cfg := historyD16Config(t)
	// The default config's compressed tool manifest sends only manifest-tier
	// tools; these fixtures are custom test tools, so the plain defs path
	// must be used for them to be offered at all.
	cfg.Tools.Manifest.Compressed = false
	provider := &stopWholeTurnE2EProvider{}
	gate := newStopWholeTurnGateTool("stop_gate_e2e")
	count := &stopWholeTurnCountTool{name: "stop_count_e2e"}

	provider.steps = []stopWholeTurnStep{
		{
			stream:  "BEFORE",
			content: "BEFORE",
			toolCalls: []providers.ToolCall{
				stopWholeTurnToolCall("call_1", gate.Name()),
				stopWholeTurnToolCall("call_2", count.Name()),
			},
		},
		// The wrap-up round Q1 = A forbids — scripted so the RED failures are
		// the behavioural assertions below, not the script guard.
		{content: "WRAP-UP-AFTER-STOP"},
	}

	h := newHistoryD16Harness(t, home, cfg, provider, true)
	t.Cleanup(gate.letGo) // release before the harness joins owned turn work on failure
	stopWholeTurnRegisterTools(t, h, gate, count)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "1534-whole-turn-instruction", "use the tools")

	preStopRaws := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeToken) {
			return false
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.Content == "BEFORE"
	})

	select {
	case <-gate.started:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for the blocking tool to start")
	}

	shown := stopBodyParityParseLive(t, preStopRaws)
	require.Equal(t, "BEFORE", strings.Join(shown.tokenContents, ""), "the body accepted before Stop")
	require.NotEmpty(t, shown.liveMessageID, "capture the shown bubble's id before any post-Stop frame")

	// A successful socket write only enqueues the request; it does not prove
	// the server claimed Stop. The genuine graceful-interrupt event is emitted
	// after claimActiveTurn and before the blocked tool is released here.
	require.NoError(t, peer.WriteJSON(generated.CancelFrame{Type: "cancel", SessionId: sid}))
	claimed := h.event(func(event agent.Event) bool {
		payload, ok := event.Payload.(agent.InterruptReceivedPayload)
		return event.Kind == agent.EventKindInterruptReceived && ok &&
			payload.Kind == agent.InterruptKindGraceful && strings.HasSuffix(event.Meta.SessionKey, sid)
	})
	require.NotEmpty(t, claimed.Meta.TurnID, "the actual Stop event identifies the claimed turn")
	gate.letGo()

	raws := append(preStopRaws, stopBodyParityReadToDone(t, h, peer)...)
	live := stopBodyParityParseLive(t, raws)
	require.NotEmpty(t, live.turnID, "the done frame carries the stopped turn's id")
	assert.Equal(t, claimed.Meta.TurnID, live.turnID, "done must identify the turn that Stop actually claimed")
	assert.Contains(t, strings.Join(live.tokenContents, ""), "BEFORE",
		"the pre-Stop body was delivered live")
	assert.NotContains(t, strings.Join(live.tokenContents, ""), "WRAP-UP-AFTER-STOP",
		"Q1 = A: no continuation text after the claimed Stop may be produced (got live tokens %q)",
		strings.Join(live.tokenContents, ""))
	assert.Equal(t, int32(1), gate.execs.Load(),
		"the already-running tool executes exactly once (fixture sanity)")
	assert.Equal(t, int32(0), count.execs.Load(),
		"Q1 = A: no further tool call may start after the claimed Stop — the queued tool must never run")

	// #1524 lesson: wait for the durable cancellation record before reload;
	// a completed wrap-up is not a reason to waive the interrupted-label source.
	assert.True(t, stopWholeTurnCancelRecordPresent(t, h, sid, live.turnID),
		"the claimed Stop must retain its durable interrupted-label source")

	// Reload: a fresh connection attaches and replays the saved transcript.
	peer2 := h.connect()
	replay := stopWholeTurnReplayAssistants(t, h.attach(peer2, sid))
	joined := ""
	for _, frame := range replay {
		joined += frame.Content
	}
	assert.Equal(t, "BEFORE", joined,
		"Q1 = A: after a reload the stopped answer ends where the screen stopped — no wrap-up text (got %q)", joined)
	require.NotEmpty(t, replay)
	assert.Equal(t, shown.liveMessageID, derefString(replay[0].Id),
		"the replayed assistant entry must keep the bubble id captured before Stop")

	var savedWithTurn []string
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	cancelRecords := 0
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.TurnID == live.turnID {
			savedWithTurn = append(savedWithTurn, entry.Content)
		}
		if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == live.turnID {
			cancelRecords++
		}
	}
	assert.Equal(t, []string{"BEFORE"}, savedWithTurn,
		"Q1 = A: exactly the shown body is saved for the stopped turn (got %q)", savedWithTurn)
	assert.Equal(t, 1, cancelRecords, "exactly one turn_canceled record supplies the stopped turn's interrupted label")
	assert.Equal(t, 1, provider.CallCount(),
		"Q1 = A: the claimed Stop must end the turn — no wrap-up provider round (got %d calls)", provider.CallCount())
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TestStopWholeTurnE2E_Control_NoStopRunsQueuedWorkAndNextRound is the
// no-Stop control for the row above (CHECK F1): with no Stop, both tools run
// and the next provider round happens — the ordinary loop this round must
// not break.
func TestStopWholeTurnE2E_Control_NoStopRunsQueuedWorkAndNextRound(t *testing.T) {
	home, cfg := historyD16Config(t)
	// Same manifest reasoning as the stopped row above.
	cfg.Tools.Manifest.Compressed = false
	provider := &stopWholeTurnE2EProvider{}
	gate := newStopWholeTurnGateTool("stop_gate_ctl")
	count := &stopWholeTurnCountTool{name: "stop_count_ctl"}

	provider.steps = []stopWholeTurnStep{
		{
			stream:  "BEFORE",
			content: "BEFORE",
			toolCalls: []providers.ToolCall{
				stopWholeTurnToolCall("call_1", gate.Name()),
				stopWholeTurnToolCall("call_2", count.Name()),
			},
		},
		{content: "WRAP-UP-AFTER-STOP"},
	}

	h := newHistoryD16Harness(t, home, cfg, provider, true)
	t.Cleanup(gate.letGo) // release before the harness joins owned turn work on failure
	stopWholeTurnRegisterTools(t, h, gate, count)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "1534-control-instruction", "use the tools")

	h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeToken) {
			return false
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.Content == "BEFORE"
	})

	select {
	case <-gate.started:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for the blocking tool to start")
	}
	gate.letGo()

	_, _ = h.turnDone(peer) // normal completion, no Stop anywhere

	assert.Equal(t, 2, provider.CallCount(), "control: without a Stop the next provider round runs")
	assert.Equal(t, int32(1), gate.execs.Load(), "control: the gate tool ran")
	assert.Equal(t, int32(1), count.execs.Load(), "control: the queued tool ran")

	peer2 := h.connect()
	replay := stopWholeTurnReplayAssistants(t, h.attach(peer2, sid))
	joined := ""
	for _, frame := range replay {
		joined += frame.Content
	}
	assert.Equal(t, "BEFOREWRAP-UP-AFTER-STOP", joined,
		"control: without a Stop the wrap-up text is produced and saved")
}

// TestStopContinuationE2E_CanceledRoundKeepsShownPrefix is the Q1 = A
// continuation-parity row (CHECK F3): round 1 is truncated with content
// "A" (the D6 accumulator holds it); round 2 streams "B" — the screen shows
// "AB" — then the Stop is claimed and the round ends cancelled. The reload
// must show exactly "AB"; today it shows "A" because
// preserveTruncatedAccumulator overrides the correctly cut buffer with the
// accumulated text alone.
func TestStopContinuationE2E_CanceledRoundKeepsShownPrefix(t *testing.T) {
	home, cfg := historyD16Config(t)
	provider := &stopWholeTurnE2EProvider{}
	provider.steps = []stopWholeTurnStep{
		{stream: "A", content: "A", finishReason: "truncated"},
		{stream: "B", waitCancel: true, err: context.Canceled},
	}

	h := newHistoryD16Harness(t, home, cfg, provider, true)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "1534-continuation-instruction", "tell me a long story")

	round1Raws := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeToken) {
			return false
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.Content == "A"
	})
	round2Raws := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeToken) {
			return false
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.Content == "B"
	})
	first := stopBodyParityParseLive(t, round1Raws)
	shown := stopBodyParityParseLive(t, round2Raws)
	require.Equal(t, "A", strings.Join(first.tokenContents, ""), "the original truncated round was shown")
	require.Equal(t, "B", strings.Join(shown.tokenContents, ""), "this continuation's prefix was shown before Stop")
	require.NotEmpty(t, first.liveMessageID, "the original round has a live message id")
	require.Equal(t, first.liveMessageID, shown.liveMessageID, "D6 continues the same live answer id")

	// The Stop arrives after "B" was shown; the cancelled round delivers
	// nothing further (frames unchanged).
	require.NoError(t, peer.WriteJSON(generated.CancelFrame{Type: "cancel", SessionId: sid}))

	raws := append(round2Raws, stopBodyParityReadToDone(t, h, peer)...)
	live := stopBodyParityParseLive(t, raws)
	require.NotEmpty(t, live.turnID, "the done frame carries the stopped turn's id")

	// The turn was stopped: its turn_canceled record must be durably saved
	// before any reload-style readback (#1524 lesson).
	historyD16AwaitCancelRecord(t, h, sid, live.turnID)

	peer2 := h.connect()
	replay := stopWholeTurnReplayAssistants(t, h.attach(peer2, sid))
	joined := ""
	for _, frame := range replay {
		joined += frame.Content
	}
	assert.Equal(t, "AB", joined,
		"the shown prefix of the cancelled continuation must survive the reload (got %q)", joined)
	require.NotEmpty(t, replay)
	assert.Equal(t, first.liveMessageID, derefString(replay[0].Id),
		"the replayed entry must keep the same id across original round, continuation and reload")
	assert.Equal(t, 2, provider.CallCount(), "the claimed Stop must not allow a third provider call")
	assistant := stopParityAssistantEntries(t, h.store, sid)
	require.Len(t, assistant, 1, "the continued answer is persisted once under its original message id")
	assert.Equal(t, "AB", assistant[0].Content, "the actual saved body includes the current round's shown prefix")
	assert.Equal(t, first.liveMessageID, assistant[0].ID, "saved and live answer identity agree")
	assert.Equal(t, live.turnID, assistant[0].TurnID, "the saved answer correlates with the stopped turn")
}

// TestStopContinuationE2E_Control_NoStopSavesJoinedAnswer is the no-Stop
// control for the continuation row: an uninterrupted truncated-then-continued
// answer saves the joined "AB" — the harness really drives D6.
func TestStopContinuationE2E_Control_NoStopSavesJoinedAnswer(t *testing.T) {
	home, cfg := historyD16Config(t)
	provider := &stopWholeTurnE2EProvider{}
	provider.steps = []stopWholeTurnStep{
		{stream: "A", content: "A", finishReason: "truncated"},
		{stream: "B", content: "B", finishReason: "stop"},
	}

	h := newHistoryD16Harness(t, home, cfg, provider, true)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "1534-continuation-control", "tell me a long story")

	_, _ = h.turnDone(peer)

	assert.Equal(t, 2, provider.CallCount(), "control: the truncated round and its continuation happened")

	peer2 := h.connect()
	replay := stopWholeTurnReplayAssistants(t, h.attach(peer2, sid))
	joined := ""
	for _, frame := range replay {
		joined += frame.Content
	}
	assert.Equal(t, "AB", joined, "control: an uninterrupted continuation saves the joined answer")
}

// TestStopSuccessRaceE2E_LateSuccessTailNeverSaved is the success-racing-Stop
// row (CHECK F2): round 2 streams "B", the Stop is claimed, the provider then
// delivers "BTAIL" (live delta "TAIL") and RETURNS SUCCESS with content
// "BTAIL". The success path must not append the post-claim content into the
// continuation accumulator — the reload shows exactly "AB", never "ABTAIL".
func TestStopSuccessRaceE2E_LateSuccessTailNeverSaved(t *testing.T) {
	home, cfg := historyD16Config(t)
	provider := &stopWholeTurnE2EProvider{}
	provider.steps = []stopWholeTurnStep{
		{stream: "A", content: "A", finishReason: "truncated"},
		{stream: "B", waitCancel: true, postCancelStream: "BTAIL", content: "BTAIL", finishReason: "stop"},
	}

	h := newHistoryD16Harness(t, home, cfg, provider, true)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "1534-success-race-instruction", "tell me a long story")

	round1Raws := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeToken) {
			return false
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.Content == "A"
	})
	round2Raws := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeToken) {
			return false
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.Content == "B"
	})
	first := stopBodyParityParseLive(t, round1Raws)
	shown := stopBodyParityParseLive(t, round2Raws)
	require.Equal(t, "A", strings.Join(first.tokenContents, ""), "the original truncated round was shown")
	require.Equal(t, "B", strings.Join(shown.tokenContents, ""), "this continuation's prefix was shown before Stop")
	require.NotEmpty(t, first.liveMessageID, "the original round has a live message id")
	require.Equal(t, first.liveMessageID, shown.liveMessageID, "D6 continues the same live answer id")

	// The Stop is claimed; the provider then delivers the tail and returns
	// success (frames unchanged — the browser discards the tail).
	require.NoError(t, peer.WriteJSON(generated.CancelFrame{Type: "cancel", SessionId: sid}))

	raws := append(round2Raws, stopBodyParityReadToDone(t, h, peer)...)
	live := stopBodyParityParseLive(t, raws)
	require.NotEmpty(t, live.turnID, "the done frame carries the turn's id")
	assert.Contains(t, strings.Join(live.tokenContents, ""), "TAIL",
		"frames are unchanged: the post-claim tail is still published live; the browser discards it")
	assert.Equal(t, 2, provider.CallCount(),
		"the truncated round and its continuation happened; no third call may follow")

	// The turn completed; the persisted answer must be stable before the
	// reload-style readback — bounded wait for the assistant entry.
	deadline := time.Now().Add(10 * time.Second)
	var saved string
	for {
		entries, err := h.store.ReadTranscript(sid)
		require.NoError(t, err)
		for _, entry := range entries {
			if entry.Role == "assistant" && entry.TurnID == live.turnID {
				saved = entry.Content
			}
		}
		if saved != "" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, "AB", saved,
		"Q1 = A: the success that returned after the claimed Stop must not append its content — got %q", saved)

	peer2 := h.connect()
	replay := stopWholeTurnReplayAssistants(t, h.attach(peer2, sid))
	require.NotEmpty(t, replay, "the shown answer must survive reload")
	joined := ""
	nonemptyReplayBodies := 0
	// Per-call accounting may keep empty records under their own ids. Only
	// the non-empty answer must retain its original message id; every record
	// still keeps turn identity and must exclude post-Stop text.
	for _, frame := range replay {
		assert.Equal(t, live.turnID, derefString(frame.TurnId), "every answer frame belongs to the stopped turn")
		assert.NotContains(t, frame.Content, "TAIL", "no replayed frame may contain the post-claim tail")
		if frame.Content != "" {
			assert.Equal(t, first.liveMessageID, derefString(frame.Id), "the non-empty answer frame keeps the pre-Stop message id")
			nonemptyReplayBodies++
		}
		joined += frame.Content
	}
	assert.Equal(t, 1, nonemptyReplayBodies, "the continuation reloads exactly one non-empty answer body")
	assert.Equal(t, "AB", joined,
		"Q1 = A: the reload shows exactly the body shown at Stop — no post-claim tail (got %q)", joined)
	assert.NotContains(t, joined, "TAIL", "no saved or replayed assistant body may contain the post-claim tail")
	assistant := stopParityAssistantEntries(t, h.store, sid)
	require.NotEmpty(t, assistant, "the shown answer must be saved")
	savedJoined := ""
	nonemptySavedBodies := 0
	for _, entry := range assistant {
		assert.Equal(t, live.turnID, entry.TurnID, "every saved answer entry belongs to the stopped turn")
		assert.NotContains(t, entry.Content, "TAIL", "no saved entry may contain the post-claim tail")
		if entry.Content != "" {
			assert.Equal(t, first.liveMessageID, entry.ID, "the non-empty saved answer entry keeps the pre-Stop message id")
			nonemptySavedBodies++
		}
		savedJoined += entry.Content
	}
	assert.Equal(t, 1, nonemptySavedBodies, "the continuation saves exactly one non-empty answer body")
	assert.Equal(t, "AB", savedJoined, "saved answer bodies equal the pre-Stop screen exactly")
}

// TestStopSecondWriterE2E_LateToolCallsNeverSavedOrReplayed closes CHECK F2:
// a successful response carrying ToolCalls arrives after the real Stop claim.
// Q1=A requires both browser reload paths to retain only the shown answer,
// never recordToolCalls' post-claim narration or a card for the late tool.
// This is baseline-green coverage on the round-2 fix; CHECK owns mutation.
func TestStopSecondWriterE2E_LateToolCallsNeverSavedOrReplayed(t *testing.T) {
	const (
		shownBody  = "SHOWN"
		lateTail   = "TAIL-AFTER-CLAIM"
		nextAnswer = "NEXT-ANSWER-AFTER-STOP"
	)
	home, cfg := historyD16Config(t)
	cfg.Tools.Manifest.Compressed = false // offer the registered fixture, not a compressed builtin-only manifest
	tool := &stopWholeTurnCountTool{name: "stop_second_writer_e2e"}
	provider := &stopWholeTurnE2EProvider{steps: []stopWholeTurnStep{
		{
			stream: shownBody, waitCancel: true,
			postCancelStream: shownBody + lateTail,
			content:          shownBody + lateTail,
			finishReason:     "tool_calls",
			toolCalls:        []providers.ToolCall{stopWholeTurnToolCall("1534-late-call", tool.Name())},
		},
		// If the turn incorrectly continues, make the forbidden answer visible
		// instead of relying on an unscripted-provider error to end the test.
		{stream: nextAnswer, content: nextAnswer, finishReason: "stop"},
	}}
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	stopWholeTurnRegisterTools(t, h, tool)
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	h.send(peer, sid, "1534-second-writer-instruction", "narrate, then use the tool")

	preStopRaws := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(t, raw) != string(generated.WsFrameTypeToken) {
			return false
		}
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame.Content == shownBody
	})
	shown := stopBodyParityParseLive(t, preStopRaws)
	require.Equal(t, shownBody, strings.Join(shown.tokenContents, ""), "the exact body actually delivered before Stop")
	require.NotEmpty(t, shown.liveMessageID, "capture the shown answer ID before any racing response")

	// The provider's waitCancel barrier releases its successful ToolCalls
	// response only after claimCancel has cut the streamer and cancelled the
	// request context. A socket write alone would not establish that ordering.
	require.NoError(t, peer.WriteJSON(generated.CancelFrame{Type: "cancel", SessionId: sid}))
	claimed := h.event(func(event agent.Event) bool {
		payload, ok := event.Payload.(agent.InterruptReceivedPayload)
		return event.Kind == agent.EventKindInterruptReceived && ok &&
			payload.Kind == agent.InterruptKindGraceful && strings.HasSuffix(event.Meta.SessionKey, sid)
	})
	require.NotEmpty(t, claimed.Meta.TurnID, "the genuine Stop event identifies the claimed turn")
	liveRaws := append(preStopRaws, stopBodyParityReadToDone(t, h, peer)...)
	live := stopBodyParityParseLive(t, liveRaws)
	require.Equal(t, claimed.Meta.TurnID, live.turnID, "done belongs to the turn whose Stop was claimed")
	require.Contains(t, strings.Join(live.tokenContents, ""), lateTail,
		"fixture sanity: the in-flight provider delivered its post-claim tail; the browser discards it")
	assert.NotContains(t, strings.Join(live.tokenContents, ""), nextAnswer, "Stop must not start a further answer")
	for _, raw := range liveRaws {
		kind := historyD16Type(t, raw)
		assert.NotEqual(t, string(generated.WsFrameTypeToolCallStart), kind, "no late tool card may start live")
		assert.NotEqual(t, string(generated.WsFrameTypeToolCallResult), kind, "no late tool result may appear live")
		if kind == string(generated.WsFrameTypeToken) {
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(raw, &token))
			assert.Equal(t, shown.liveMessageID, derefString(token.MessageId), "no new answer bubble after Stop")
		}
	}

	// done precedes the durable cancellation record (#1524). The request
	// drain alone does not join that session-worker append; await it explicitly.
	historyD16AwaitCancelRecord(t, h, sid, live.turnID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.True(t, h.loop.WaitForActiveRequestsContext(ctx), "owned provider work drained before reload")
	assert.Equal(t, 1, provider.CallCount(), "Stop permits no further provider round or answer")
	assert.Equal(t, int32(0), tool.execs.Load(), "a tool requested only after the claim must never execute")

	// Browser GET-history path: check every entry, not just the first matching
	// answer, so a second writer cannot hide a tail/card under another ID.
	page := h.page(sid, "")
	require.NotEmpty(t, page.Entries, "the reload must return the saved conversation")
	assert.False(t, page.HasEarlier, "this tiny conversation must fit wholly in the history page")
	assert.False(t, page.HasLater, "no later source group may hide a post-Stop write")
	var restBodies []string
	for _, entry := range page.Entries {
		body := derefString(entry.Content)
		assert.NotContains(t, body, lateTail, "no REST entry may carry post-claim narration")
		assert.NotContains(t, body, nextAnswer, "no REST entry may carry a further answer")
		if entry.Type != nil {
			assert.NotEqual(t, "tool_call", string(*entry.Type), "no saved tool-card source in REST history")
		}
		if entry.ToolCalls != nil {
			assert.Empty(t, *entry.ToolCalls, "no tool requested after the claim may have a saved REST card")
		}
		if entry.Role != nil && *entry.Role == "assistant" {
			assert.Equal(t, live.turnID, derefString(entry.TurnId), "all REST answer records retain turn identity")
			if body != "" {
				assert.Equal(t, shown.liveMessageID, entry.Id, "only nonempty answer bodies require the original ID")
				restBodies = append(restBodies, body)
			}
		}
	}
	assert.Equal(t, []string{shownBody}, restBodies, "REST reload has exactly one nonempty answer, exactly as shown at Stop")

	// Fresh-socket replay path: inspect all frame types, not only assistant
	// prose. Tool cards replay as start/result frames outside replay_message.
	peer2 := h.connect()
	replayRaws := h.attach(peer2, sid)
	var replayBodies []string
	for _, raw := range replayRaws {
		kind := historyD16Type(t, raw)
		assert.NotEqual(t, string(generated.WsFrameTypeToolCallStart), kind, "no late tool card in reload replay")
		assert.NotEqual(t, string(generated.WsFrameTypeToolCallResult), kind, "no late tool result in reload replay")
		assert.NotContains(t, string(raw), lateTail, "no replay or current-projection frame may hide post-claim narration")
		assert.NotContains(t, string(raw), nextAnswer, "no reload frame may hide a further answer")
		if kind == string(generated.WsFrameTypeReplayMessage) {
			var frame generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame.Role == "assistant" {
				assert.Equal(t, live.turnID, derefString(frame.TurnId), "all replay answer records retain turn identity")
				if frame.Content != "" {
					assert.Equal(t, shown.liveMessageID, derefString(frame.Id), "the nonempty replay answer keeps its original ID")
					replayBodies = append(replayBodies, frame.Content)
				}
			}
		}
	}
	assert.Equal(t, []string{shownBody}, replayBodies, "replay has exactly one nonempty answer, exactly as shown at Stop")

	// Canonical readback rules out a hidden write that both projections omit.
	// Empty per-call accounting records may have separate IDs; they are not
	// answer bubbles and are still checked for turn identity and forbidden text.
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	var savedBodies []string
	for _, entry := range entries {
		assert.NotContains(t, entry.Content, lateTail, "the second writer must not save post-claim narration")
		assert.NotContains(t, entry.Content, nextAnswer, "the archive must not gain a further answer")
		assert.NotEqual(t, session.EntryTypeToolCall, entry.Type, "the archive must not gain a late tool-card source")
		assert.Empty(t, entry.ToolCalls, "no late tool call or result may be saved")
		if entry.Role == "assistant" {
			assert.Equal(t, live.turnID, entry.TurnID, "all saved answer records retain turn identity")
			if entry.Content != "" {
				assert.Equal(t, shown.liveMessageID, entry.ID, "the nonempty saved answer keeps its original ID")
				savedBodies = append(savedBodies, entry.Content)
			}
		}
	}
	assert.Equal(t, []string{shownBody}, savedBodies, "the archive has exactly one nonempty answer, exactly as shown at Stop")
	t.Logf("second-writer reload: shown=%q REST=%q replay=%q saved=%q provider_calls=%d tool_executions=%d",
		shownBody, restBodies, replayBodies, savedBodies, provider.CallCount(), tool.execs.Load())
}

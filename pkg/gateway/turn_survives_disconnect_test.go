// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// turn_survives_disconnect_test.go — ADR-082 integration coverage (T-10,
// T-11, T-12): a webchat turn is UI-independent (P1) and streaming is a
// property of the session, not the socket (P2). These tests drive a REAL
// turn end to end (real WS server, real agent loop, a controllable fake
// streaming provider — no live model) and assert on the actual wire frames
// and persisted transcript, reproducing the exact E1/E2 evidence from
// ADR-082 §2.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// providerRound describes one ChatStream/Chat round for
// controllableStreamProvider: the token deltas to emit, and an optional
// tool call to include in the round's final response (forcing the agent
// loop to execute a tool and start another round).
type providerRound struct {
	tokens   []string
	toolCall *providers.ToolCall
}

// controllableStreamProvider is a providers.LLMProvider + StreamingProvider
// fake with deterministic, test-controllable pacing — no live model, no
// wall-clock races. Each call to ChatStream consumes the next round in
// order.
type controllableStreamProvider struct {
	mu     sync.Mutex
	rounds []providerRound
	idx    int

	chatCalls   atomic.Int32
	streamCalls atomic.Int32

	// afterToken, if non-nil, is invoked synchronously after each token is
	// delivered via onChunk (before any pause check below) — lets a test
	// observe/react to a specific point in the stream (e.g. "5 tokens have
	// gone out") without relying on wall-clock timing.
	afterToken func(roundIdx, tokenIdx int, cumulative string)

	// Pause support (T-11's exact reproduction of ADR-082 §2 evidence E2):
	// after emitting token index pauseAfterToken (0-based) of round
	// pauseRound, ChatStream closes pausedCh and then blocks on resumeCh —
	// giving the test a precise window to inspect/rebind connections mid-
	// stream before the remaining tokens flow. pauseAfterToken == -1
	// disables pausing entirely.
	pauseRound      int
	pauseAfterToken int
	pausedCh        chan struct{}
	resumeCh        chan struct{}
}

func newControllableStreamProvider(rounds ...providerRound) *controllableStreamProvider {
	return &controllableStreamProvider{
		rounds:          rounds,
		pauseAfterToken: -1,
	}
}

// pauseAfter arms a one-shot pause after token index tokenIdx (0-based) of
// round roundIdx. Returns the channel that closes once the pause point is
// reached (test should wait on it) and the channel the test must close to
// let the stream continue.
func (p *controllableStreamProvider) pauseAfter(roundIdx, tokenIdx int) (paused <-chan struct{}, resume chan<- struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pauseRound = roundIdx
	p.pauseAfterToken = tokenIdx
	p.pausedCh = make(chan struct{})
	p.resumeCh = make(chan struct{})
	return p.pausedCh, p.resumeCh
}

func (p *controllableStreamProvider) GetDefaultModel() string { return "controllable-test-model" }

func (p *controllableStreamProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.chatCalls.Add(1)
	p.mu.Lock()
	round := p.rounds[p.idx]
	p.idx++
	p.mu.Unlock()
	content := strings.Join(round.tokens, "")
	resp := &providers.LLMResponse{Content: content}
	if round.toolCall != nil {
		resp.ToolCalls = []providers.ToolCall{*round.toolCall}
	}
	return resp, nil
}

func (p *controllableStreamProvider) ChatStream(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
	onChunk func(accumulated string),
	_ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	p.streamCalls.Add(1)
	p.mu.Lock()
	roundIdx := p.idx
	round := p.rounds[roundIdx]
	p.idx++
	pauseRound := p.pauseRound
	pauseAfterToken := p.pauseAfterToken
	pausedCh := p.pausedCh
	resumeCh := p.resumeCh
	afterToken := p.afterToken
	p.mu.Unlock()

	var acc strings.Builder
	for i, tok := range round.tokens {
		acc.WriteString(tok)
		onChunk(acc.String())
		if afterToken != nil {
			afterToken(roundIdx, i, acc.String())
		}
		if pauseAfterToken >= 0 && roundIdx == pauseRound && i == pauseAfterToken {
			close(pausedCh)
			<-resumeCh
		}
	}
	resp := &providers.LLMResponse{Content: acc.String()}
	if round.toolCall != nil {
		resp.ToolCalls = []providers.ToolCall{*round.toolCall}
	}
	return resp, nil
}

var (
	_ providers.LLMProvider       = (*controllableStreamProvider)(nil)
	_ providers.StreamingProvider = (*controllableStreamProvider)(nil)
)

// newControllableStreamTestServer builds a real WS server + agent loop
// wired to provider, mirroring newStreamingTestWSHandler's shape but with a
// caller-supplied, test-controllable provider.
func newControllableStreamTestServer(t *testing.T, provider providers.LLMProvider) (*httptest.Server, *WSHandler, *bus.MessageBus) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "controllable-test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run exited: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(30 * time.Second):
			t.Logf("agent loop Run did not exit within 30s")
		}
	})
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	return srv, handler, msgBus
}

// TestTurn_ContinuesWhenOnlyConnectionCloses is T-10/S-01: A drops after 5
// tokens; the turn must keep running to completion and persist the FULL
// 200-token response, with no cancel recorded.
func TestTurn_ContinuesWhenOnlyConnectionCloses(t *testing.T) {
	const totalTokens = 200
	tokens := make([]string, totalTokens)
	for i := range tokens {
		tokens[i] = "x"
	}
	provider := newControllableStreamProvider(providerRound{tokens: tokens})

	fifthTokenSeen := make(chan struct{})
	var closeOnce sync.Once
	provider.afterToken = func(_ int, tokenIdx int, _ string) {
		if tokenIdx == 4 { // 5th token (0-indexed)
			closeOnce.Do(func() { close(fifthTokenSeen) })
		}
	}

	srv, handler, _ := newControllableStreamTestServer(t, provider)

	conn := dialTestWS(t, srv)
	sendWSAuthFrameDevMode(t, conn)

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "stream 200 tokens please"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", 5*time.Second)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	// Wait until at least the 5th token has been streamed, then close the
	// ONLY connection — before the turn has finished (200 tokens total).
	select {
	case <-fifthTokenSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: never observed the 5th streamed token")
	}
	require.NoError(t, conn.Close())

	// Poll the persisted transcript until the full 200-token assistant
	// entry appears — no live connection is needed for this to happen.
	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)

	deadline := time.Now().Add(10 * time.Second)
	var finalContent string
	for time.Now().Before(deadline) {
		entries, readErr := store.ReadTranscript(sessionID)
		require.NoError(t, readErr)
		for _, e := range entries {
			if e.Role == "assistant" {
				finalContent = e.Content
			}
		}
		if len(finalContent) == totalTokens {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.Equal(t, totalTokens, len(finalContent),
		"the turn must persist the FULL response even though its only viewer disconnected mid-stream")
	assert.Equal(t, strings.Repeat("x", totalTokens), finalContent)

	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.NotEqual(t, session.StatusInterrupted, meta.Status,
		"a disconnect must never be recorded as a cancel/interrupt")
}

// TestReconnectMidTurn_CatchUpThenLive is T-11/S-03: the exact E2
// reproduction. A receives N tokens then drops; B attaches on a NEW
// connection while the turn is still generating; B must receive replay,
// then exactly ONE catch-up token whose content is exactly the
// concatenation of everything generated so far, then every remaining
// token, then exactly one done — and the final text must equal the
// persisted transcript entry.
func TestReconnectMidTurn_CatchUpThenLive(t *testing.T) {
	const pauseAfterIdx = 9 // pause after the 10th token (0-based index 9)
	tokens := []string{
		"one-", "two-", "three-", "four-", "five-",
		"six-", "seven-", "eight-", "nine-", "ten-",
		"eleven-", "twelve-", "thirteen-", "fourteen-", "fifteen.",
	}
	provider := newControllableStreamProvider(providerRound{tokens: tokens})
	paused, resume := provider.pauseAfter(0, pauseAfterIdx)

	srv, handler, _ := newControllableStreamTestServer(t, provider)

	connA := dialTestWS(t, srv)
	sendWSAuthFrameDevMode(t, connA)

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "stream then reconnect"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, connA.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, connA, "session_started", 5*time.Second)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	// Wait for the provider to reach its pause point. Because the provider
	// races ahead of the client (writes are async, not gated on the client
	// actually reading), tokens 0..pauseAfterIdx may ALL already be
	// in-flight/socket-buffered by the time this fires — do not assume A
	// has read only one frame per provider step.
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: provider never reached its pause point")
	}

	// Drain everything A has received so far, using an IDLE timeout (not a
	// fixed frame count) as the stop condition: once the provider is
	// blocked, no further frames can arrive, so a short silence reliably
	// means "everything currently in flight has been drained".
	var aReceived strings.Builder
	for {
		connA.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, raw, rerr := connA.ReadMessage()
		if rerr != nil {
			break
		}
		var f replayFrameDecoder
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if f.Type == "token" {
			aReceived.WriteString(f.Content)
		}
	}
	snapshotAtA := aReceived.String()
	require.NotEmpty(t, snapshotAtA, "A must have received at least one token before the pause")

	// A drops.
	require.NoError(t, connA.Close())

	// B attaches on a NEW connection while the turn is still paused
	// mid-generation.
	connB := dialTestWS(t, srv)
	t.Cleanup(func() { _ = connB.Close() })
	sendWSAuthFrameDevMode(t, connB)

	attachFrame := wsClientFrameTestHelper{Type: "attach_session", SessionID: sessionID}
	attachData, err := json.Marshal(attachFrame)
	require.NoError(t, err)
	require.NoError(t, connB.WriteMessage(websocket.TextMessage, attachData))

	// Wait for evidence that B's bind (and catch-up snapshot, per D3) has
	// already happened before resuming the provider: handleAttachSession's
	// bind+snapshot is the FIRST thing it does, strictly before ANY
	// attach-related frame (replay history, catch-up, or live) is emitted
	// to B — so receiving an attach-related frame proves the bind already
	// happened. The VERY first frame on a fresh connection is always the
	// connection-open session_state one-shot (emitted before the client's
	// attach_session message can even be read by the server) — skip past
	// that, it proves nothing about the attach. Without this wait,
	// resuming immediately after the client-side Write races the server's
	// own processing: if a live token's Update() ran (and captured its
	// target list) before B's bind, that token becomes part of B's
	// catch-up snapshot instead of a separate live frame — no
	// duplicate/gap either way, but it would make the "catch-up == EXACTLY
	// what A had" assertion below flaky rather than deterministic.
	var firstRaw []byte
	for {
		connB.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, raw, rerr := connB.ReadMessage()
		require.NoError(t, rerr, "B must receive at least one attach-related frame before resuming")
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Type == "session_state" {
			continue // the connection-open one-shot — not attach-related
		}
		firstRaw = raw
		break
	}

	close(resume)

	// Collect B's frames — replaying firstRaw (already read above) through
	// the same decode/classify path, then continuing to read — until the
	// turn's own done frame arrives (see ws_catchup_test.go's identical
	// distinguishing-marker note: streamReplay's own done, present because
	// the user's message was already persisted, is NOT the turn's done).
	var bTokens []string
	doneCount := 0
	decodeAndClassify := func(raw []byte) {
		var f struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Stats   *struct {
				Tokens *float64 `json:"tokens"`
			} `json:"stats"`
		}
		if json.Unmarshal(raw, &f) != nil {
			return
		}
		switch f.Type {
		case "token":
			bTokens = append(bTokens, f.Content)
		case "done":
			if f.Stats != nil && f.Stats.Tokens != nil {
				doneCount++
			}
		}
	}
	decodeAndClassify(firstRaw)
	bDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(bDeadline) && doneCount == 0 {
		connB.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, raw, rerr := connB.ReadMessage()
		if rerr != nil {
			break
		}
		decodeAndClassify(raw)
	}
	require.NotEmpty(t, bTokens, "B must have received at least the catch-up token")
	require.Equal(t, 1, doneCount, "B must receive exactly one TURN done frame")

	// The FIRST token frame B receives is the catch-up snapshot.
	require.Equal(t, snapshotAtA, bTokens[0],
		"the catch-up token's content must equal EXACTLY what A had already received")

	// Reassembling catch-up + every subsequent live token must equal the
	// full text.
	var bFull strings.Builder
	for _, tok := range bTokens {
		bFull.WriteString(tok)
	}
	fullText := strings.Join(tokens, "")
	assert.Equal(t, fullText, bFull.String(),
		"catch-up + live tokens must reassemble to the full text with no duplicate, no gap")

	// The persisted transcript entry must match exactly.
	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)
	deadline2 := time.Now().Add(5 * time.Second)
	var persisted string
	for time.Now().Before(deadline2) {
		entries, readErr := store.ReadTranscript(sessionID)
		require.NoError(t, readErr)
		for _, e := range entries {
			if e.Role == "assistant" {
				persisted = e.Content
			}
		}
		if persisted == fullText {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.Equal(t, fullText, persisted, "the final persisted transcript must equal the full streamed text")
}

// TestTurn_NextRoundStreamsAfterDisconnect is T-12/S-05: a turn whose first
// round streamed and whose only connection then closed must still use
// ChatStream (never fall back to non-streaming Chat) for its NEXT round,
// even with zero bound connections — proving GetStreamer's D2 fix (a
// streamer always exists for a webchat session id) closes the E4 fallback
// path for every round of a turn, not just the first.
func TestTurn_NextRoundStreamsAfterDisconnect(t *testing.T) {
	round1 := providerRound{
		tokens: []string{"Checking", " tasks", "..."},
		toolCall: &providers.ToolCall{
			ID:        "call-1",
			Name:      "list_tasks",
			Arguments: map[string]any{},
		},
	}
	round2 := providerRound{tokens: []string{"All", " done."}}
	provider := newControllableStreamProvider(round1, round2)

	round1Done := make(chan struct{})
	var closeOnce sync.Once
	provider.afterToken = func(roundIdx, tokenIdx int, _ string) {
		if roundIdx == 0 && tokenIdx == len(round1.tokens)-1 {
			closeOnce.Do(func() { close(round1Done) })
		}
	}

	srv, handler, _ := newControllableStreamTestServer(t, provider)

	conn := dialTestWS(t, srv)
	sendWSAuthFrameDevMode(t, conn)

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "check my tasks"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", 5*time.Second)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	select {
	case <-round1Done:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: round 1 never completed streaming")
	}
	// Close the ONLY connection right after round 1 — before the tool call
	// even finishes executing, guaranteeing round 2 begins with ZERO bound
	// connections.
	require.NoError(t, conn.Close())

	// Wait for round 2 (ChatStream call #2) to complete and the turn to
	// persist its final assistant entry.
	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)
	deadline := time.Now().Add(10 * time.Second)
	var sawFinal bool
	for time.Now().Before(deadline) {
		entries, readErr := store.ReadTranscript(sessionID)
		require.NoError(t, readErr)
		for _, e := range entries {
			if e.Role == "assistant" && strings.Contains(e.Content, "All done.") {
				sawFinal = true
			}
		}
		if sawFinal {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.True(t, sawFinal, "the turn must complete round 2 and persist its final response")

	assert.Equal(t, int32(0), provider.chatCalls.Load(),
		"S-05: every round of the turn must use ChatStream — a fallback to non-streaming Chat "+
			"means GetStreamer failed to produce a streamer for a round with zero bound connections")
	assert.Equal(t, int32(2), provider.streamCalls.Load(),
		"both round 1 (tool call) and round 2 (final answer) must have used ChatStream")
}

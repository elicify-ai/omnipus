package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const (
	sinceCursorFirstPrompt = "first streaming question"
	sinceCursorNextPrompt  = "follow-up at the finished-turn boundary"
	sinceCursorFirstAnswer = "First streamed answer."
	sinceCursorNextAnswer  = "Continued streamed answer."
)

// Only the provider edge is scripted. A plain Chat call is a test failure,
// rather than a second way to obtain a successful-looking response.
type sinceCursorStreamingProvider struct {
	mu         sync.Mutex
	streamed   int
	plain      int
	lastInputs []string
}

func (*sinceCursorStreamingProvider) GetDefaultModel() string { return "sincecursor-stream-model" }

func (p *sinceCursorStreamingProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.plain++
	return nil, fmt.Errorf("sincecursor probe requires actual streaming, not plain Chat")
}

func (p *sinceCursorStreamingProvider) ChatStream(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := p.streamed
	p.streamed++
	lastInput := ""
	for _, message := range messages {
		if message.Role == "user" && !message.Volatile {
			lastInput = message.Content
		}
	}
	p.lastInputs = append(p.lastInputs, lastInput)
	p.mu.Unlock()
	answers := []string{sinceCursorFirstAnswer, sinceCursorNextAnswer}
	if index >= len(answers) {
		return nil, fmt.Errorf("unexpected extra streaming call %d", index+1)
	}
	answer := answers[index]
	chunk(answer[:len(answer)/2])
	chunk(answer)
	return &providers.LLMResponse{Content: answer, FinishReason: "stop"}, nil
}

type sinceCursorStreamFixture struct {
	loop         *agent.AgentLoop
	handler      *WSHandler
	provider     *sinceCursorStreamingProvider
	bus          *bus.MessageBus
	conn         *wsConn
	sid          string
	firstEnded   chan struct{}
	release      chan struct{}
	releaseOnce  sync.Once
	subscription agent.EventSubscription
}

func newSinceCursorStreamFixture(t *testing.T) *sinceCursorStreamFixture {
	t.Helper()
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "sincecursor-stream-model"}, MaxTokens: 4096, MaxToolIterations: 4},
		List:     []config.AgentConfig{{ID: "mia"}},
	}}
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := &sinceCursorStreamingProvider{}
	loop := mustAgentLoop(t, cfg, msgBus, provider)
	handler := newWSHandler(msgBus, loop, "")
	handler.webchatCh = newWebchatChannel(handler)
	msgBus.SetStreamDelegate(handler)
	meta, err := loop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	conn := makeTestConn()
	conn.sendCh = make(chan []byte, 256)
	t.Cleanup(conn.close)
	bindTestConnToSession(handler, "sincecursor-stream-chat", meta.ID, conn)
	f := &sinceCursorStreamFixture{loop: loop, handler: handler, provider: provider, bus: msgBus, conn: conn, sid: meta.ID,
		firstEnded: make(chan struct{}), release: make(chan struct{}), subscription: loop.SubscribeEvents(256)}
	t.Cleanup(func() { loop.UnsubscribeEvents(f.subscription.ID) })
	ctx, cancel := context.WithCancel(context.Background())
	var first sync.Once
	// Preserve the real gateway tap. This test-only barrier holds the first
	// turn after its final steering poll and streamed done, before the worker
	// can drain a late instruction. No sleep or scheduling guess is involved.
	loop.SetEventSyncTap(func(event agent.Event) {
		handler.hubSyncTap(event)
		if event.Kind == agent.EventKindTurnEnd {
			payload, ok := event.Payload.(agent.TurnEndPayload)
			if ok && payload.SessionID == f.sid {
				first.Do(func() {
					close(f.firstEnded)
					select {
					case <-f.release:
					case <-ctx.Done():
					}
				})
			}
		}
	})
	runDone := make(chan error, 1)
	go func() { runDone <- loop.Run(ctx) }()
	t.Cleanup(func() {
		f.releaseOnce.Do(func() { close(f.release) })
		cancel()
		require.NoError(t, <-runDone)
	})
	return f
}

func sinceCursorWaitEvent(t *testing.T, f *sinceCursorStreamFixture, kind agent.EventKind) agent.Event {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-f.subscription.C:
			if event.Kind == kind {
				return event
			}
		case <-timer.C:
			t.Fatalf("did not observe required continuation event %v", kind)
		}
	}
}

func sinceCursorChatRows(entries []session.TranscriptEntry) [][]string {
	var rows [][]string
	for _, entry := range entries {
		if entry.Role == "user" || entry.Role == "assistant" {
			rows = append(rows, []string{entry.Role, entry.Content})
		}
	}
	return rows
}

func sinceCursorLiveMessageIDs(t *testing.T, frames [][]byte, sid string) []string {
	t.Helper()
	var ids []string
	text := make(map[string]string)
	for _, raw := range framesOfType(t, frames, "token") {
		var token generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &token))
		require.Equal(t, sid, token.SessionId)
		require.NotNil(t, token.MessageId)
		id := *token.MessageId
		require.NotEmpty(t, id)
		if _, seen := text[id]; !seen {
			ids = append(ids, id)
		}
		text[id] += token.Content
	}
	require.Len(t, ids, 2, "each real streamed turn has exactly one assistant message")
	require.Equal(t, []string{sinceCursorFirstAnswer, sinceCursorNextAnswer}, []string{text[ids[0]], text[ids[1]]},
		"live output contains each complete reply exactly once, with no fallback duplicate")
	doneFrames := framesOfType(t, frames, "done")
	doneIDs := make([]string, 0, len(doneFrames))
	for _, raw := range doneFrames {
		var done generated.DoneFrame
		require.NoError(t, json.Unmarshal(raw, &done))
		require.Equal(t, sid, done.SessionId)
		require.NotNil(t, done.Stats)
		require.NotNil(t, done.MessageId)
		if done.Stats.TurnFailed != nil {
			require.False(t, *done.Stats.TurnFailed)
		}
		doneIDs = append(doneIDs, *done.MessageId)
	}
	require.Equal(t, ids, doneIDs, "exactly one terminal frame closes each streamed reply")
	return ids
}

// Drive the actual busy-worker drain, real gateway streamer, durable store and
// snapshot replay together. The first turn is held AFTER its done, and the
// next instruction must be accepted into steering before that barrier opens.
func TestSinceCursor_StreamedContinuationSavedAndReplayedExactlyOnce(t *testing.T) {
	f := newSinceCursorStreamFixture(t)
	ctx := context.Background()
	f.handler.handleChatMessage(ctx, "sincecursor-stream-chat", f.sid, sinceCursorFirstPrompt, "mia", nil, "", "", f.conn)
	select {
	case <-f.firstEnded:
	case <-time.After(10 * time.Second):
		t.Fatal("initial streamed turn never reached its post-done barrier")
	}
	f.handler.handleChatMessage(ctx, "sincecursor-stream-chat", f.sid, sinceCursorNextPrompt, "mia", nil, "", "", f.conn)
	queued := sinceCursorWaitEvent(t, f, agent.EventKindInterruptReceived)
	require.Equal(t, "agent:mia:session:"+f.sid, queued.Meta.SessionKey)
	f.releaseOnce.Do(func() { close(f.release) })
	injected := sinceCursorWaitEvent(t, f, agent.EventKindSteeringInjected)
	require.Equal(t, queued.Meta.SessionKey, injected.Meta.SessionKey)

	select {
	case final := <-f.bus.OutboundChan():
		require.Equal(t, f.sid, final.SessionID)
		require.Equal(t, sinceCursorNextAnswer, final.Content)
		// Exercise the real channel's streamed suppression, not just Finalize.
		require.NoError(t, f.handler.webchatCh.Send(ctx, final))
	case <-time.After(10 * time.Second):
		t.Fatal("continued worker never published its final response")
	}
	f.provider.mu.Lock()
	streamed, plain := f.provider.streamed, f.provider.plain
	inputs := append([]string(nil), f.provider.lastInputs...)
	f.provider.mu.Unlock()
	require.Equal(t, 2, streamed, "both replies must use the real streaming pipeline")
	require.Zero(t, plain, "no non-streaming fallback may mask a missing continuation streamer")
	require.Equal(t, []string{sinceCursorFirstPrompt, sinceCursorNextPrompt}, inputs)
	liveIDs := sinceCursorLiveMessageIDs(t, drainAllFrames(f.conn.sendCh), f.sid)

	store := f.loop.GetSessionStore()
	entries, err := store.ReadTranscript(f.sid)
	require.NoError(t, err)
	want := [][]string{{"user", sinceCursorFirstPrompt}, {"assistant", sinceCursorFirstAnswer},
		{"user", sinceCursorNextPrompt}, {"assistant", sinceCursorNextAnswer}}
	require.Equal(t, want, sinceCursorChatRows(entries), "streamer and fallback must not double-save either reply")
	var savedIDs []string
	for _, entry := range entries {
		if entry.Role == "assistant" {
			savedIDs = append(savedIDs, entry.ID)
		}
	}
	require.Equal(t, liveIDs, savedIDs, "live and saved messages retain the same exact identities")

	replay := makeTestConn()
	replay.sendCh = make(chan []byte, 256)
	t.Cleanup(replay.close)
	bindTestConnToSession(f.handler, "sincecursor-replay-chat", f.sid, replay)
	f.handler.handleAttachSession(ctx, "sincecursor-replay-chat", f.sid, nil, replay)
	replayed := drainAllFrames(replay.sendCh)
	require.Len(t, framesOfType(t, replayed, "session_snapshot"), 1)
	require.Len(t, framesOfType(t, replayed, "catch_up_complete"), 1)
	replayMessages := framesOfType(t, replayed, "replay_message")
	replayRows := make([][]string, 0, len(replayMessages))
	var replayIDs []string
	for _, raw := range replayMessages {
		var message generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(raw, &message))
		require.Equal(t, f.sid, message.SessionId)
		replayRows = append(replayRows, []string{message.Role, message.Content})
		if message.Role == "assistant" {
			require.NotNil(t, message.Id)
			replayIDs = append(replayIDs, *message.Id)
		}
	}
	require.Equal(t, want, replayRows, "history reload replays exactly the four saved chat rows, once each")
	require.Equal(t, liveIDs, replayIDs)
	afterReplay, err := store.ReadTranscript(f.sid)
	require.NoError(t, err)
	require.Equal(t, entries, afterReplay, "catch-up must not append a second copy of an answer")
}

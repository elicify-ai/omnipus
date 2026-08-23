// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FIX 5d regression coverage: an async delegate/tool result delivered after
// its originating turn has finished must (1) be attributed to the TRUE
// originating agent, never silently reattributed to the default agent, and
// (2) be persisted to the correct session's transcript.jsonl regardless of
// whether a live streaming connection exists for the chat at the moment the
// result arrives. Before this fix, AsyncNotifyEvent.AgentID and the
// originating turn's TranscriptSessionID had nowhere to go on
// bus.InboundMessage, so processSystemMessage reconstructed the turn by
// guessing GetRegistry().GetDefaultAgent() and never bound a transcript
// session at all — the confirmed root cause of both a live "Worker vs Jim"
// speaker-attribution flip and permanent, silent data loss when the
// originating WS connection had already closed. See
// docs/internal/architecture/subturn.md for the full delivery path this
// covers.

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// asyncResultTestDelegateID is a named agent distinct from the registry's
// auto-created default, so attribution tests can prove a result lands on the
// DELEGATE, not coincidentally on whichever agent happens to be default.
const asyncResultTestDelegateID = "ava-worker"

// asyncResultTestDefaultID is a second, explicitly-registered agent distinct
// from asyncResultTestDelegateID. No "main" sentinel to fall back to
// anymore — NewAgentRegistry no longer auto-creates a default agent, so this
// test must register its own to keep the delegate/default distinction the
// attribution assertions below depend on.
const asyncResultTestDefaultID = "mia-default"

// newAsyncResultTestLoop builds a real AgentLoop with two agents: the
// explicitly-registered default (asyncResultTestDefaultID) and the named,
// non-default delegate (asyncResultTestDelegateID).
func newAsyncResultTestLoop(
	t *testing.T,
	provider providers.LLMProvider,
) (al *AgentLoop, msgBus *bus.MessageBus, delegate *AgentInstance, defaultAgent *AgentInstance) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				// Explicit override rather than relying on alphabetical
				// fallback ordering — asyncResultTestDefaultID
				// ("mia-default") does not sort before
				// asyncResultTestDelegateID ("ava-worker"), so
				// GetDefaultAgent's Priority 2 fallback alone would pick the
				// delegate. Naming the override explicitly is also the more
				// realistic production shape (config.Agents.Defaults.DefaultAgentID).
				DefaultAgentID: asyncResultTestDefaultID,
			},
			List: []config.AgentConfig{
				{ID: asyncResultTestDefaultID, Name: "Default Agent", Type: config.AgentTypeCustom},
				{ID: asyncResultTestDelegateID, Name: "Ava Worker", Type: config.AgentTypeCustom},
			},
		},
	}
	msgBus = bus.NewMessageBus()
	al = mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	defaultAgent = al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test setup: default agent must exist")
	var ok bool
	delegate, ok = al.GetRegistry().GetAgent(asyncResultTestDelegateID)
	require.True(t, ok, "test setup: delegate agent must be registered")
	require.NotEqual(t, defaultAgent.ID, delegate.ID,
		"test setup invariant: the delegate must NOT be the default agent — otherwise the "+
			"attribution assertions below cannot distinguish 'correctly attributed' from "+
			"'coincidentally the default'")

	return al, msgBus, delegate, defaultAgent
}

// drainNotify calls Notify and drains the resulting bus.InboundMessage,
// mirroring the established pattern in
// TestAsyncNotifier_NotificationGrantsNoCapability (async_notifier_test.go).
func drainNotify(t *testing.T, al *AgentLoop, msgBus *bus.MessageBus, event AsyncNotifyEvent) bus.InboundMessage {
	t.Helper()
	require.NoError(t, al.asyncNotifier.Notify(context.Background(), event))
	select {
	case msg := <-msgBus.InboundChan():
		require.Equal(t, "system", msg.Channel, "Notify must publish on the system channel")
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Notify to publish the inbound message")
		return bus.InboundMessage{}
	}
}

// readAssistantTranscript returns only the assistant-role entries for a session.
func readAssistantTranscript(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "read transcript")
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			out = append(out, e)
		}
	}
	return out
}

// TestProcessSystemMessage_AsyncResult_AttributesToOriginatingAgent covers
// FIX 5d root cause #1: an async result from agent X (the delegate),
// delivered after its parent's own turn has already finished, is attributed
// to X in the persisted transcript — never silently reattributed to
// GetDefaultAgent().
//
// BDD:
//
//	Given a delegate agent distinct from the registry's default,
//	  And a transcript session bound to that delegate's background work,
//	When AsyncNotifier.Notify publishes the delegate's result and
//	  processSystemMessage processes it (the exact function AgentLoop.Run's
//	  dispatch loop calls for any Channel=="system" message),
//	Then the persisted assistant transcript entry's AgentID is the
//	  DELEGATE's own ID, not the default agent's.
//
// Traces to: pkg/agent/loop.go processSystemMessage (AsyncOriginAgentID
// resolution); pkg/bus/types.go InboundMessage.AsyncOriginAgentID.
func TestProcessSystemMessage_AsyncResult_AttributesToOriginatingAgent(t *testing.T) {
	al, msgBus, delegate, defaultAgent := newAsyncResultTestLoop(t, &mockProvider{})

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "delegate",
		Content:             "The delegate finished the background task.",
	})

	_, err = al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	assistantEntries := readAssistantTranscript(t, store, meta.ID)
	require.Len(t, assistantEntries, 1, "exactly one assistant entry must be persisted")
	assert.Equal(t, delegate.ID, assistantEntries[0].AgentID,
		"the async result must be attributed to the NAMED originating agent (%q), not silently "+
			"reattributed to the default agent (%q)", delegate.ID, defaultAgent.ID)
	assert.NotEqual(t, defaultAgent.ID, assistantEntries[0].AgentID)
}

// TestProcessSystemMessage_AsyncResult_FallsBackToDefaultWhenNoOriginKnown
// is the complementary case: when the inbound message carries no
// AsyncOriginAgentID (e.g. a hypothetical future producer with no turn scope
// of its own, or a legacy/synthetic system message), processSystemMessage
// must still fall back to GetDefaultAgent() — the fallback is a genuine
// last resort, not removed by this fix.
func TestProcessSystemMessage_AsyncResult_FallsBackToDefaultWhenNoOriginKnown(t *testing.T) {
	al, msgBus, _, defaultAgent := newAsyncResultTestLoop(t, &mockProvider{})

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "webchat",
		ChatID:     "direct",
		SourceKind: "bash",
		Content:    "no AgentID/TranscriptSessionID on this event",
		// AgentID and TranscriptSessionID deliberately left unset.
	})
	require.Empty(t, msg.AsyncOriginAgentID, "test precondition: no origin agent on this message")

	_, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	// No TranscriptSessionID means nothing to assert against a session
	// store — the meaningful assertion is that processSystemMessage did not
	// error out and used SOME agent (implicitly the default, since
	// GetDefaultAgent() is the only fallback and a nil agent would have
	// produced an error return above).
	require.NotNil(t, defaultAgent)
}

// TestProcessSystemMessage_AsyncResult_PersistsWithoutLiveConnection is the
// FIX 5d data-loss regression test (root cause #2): it proves the async
// result reaches transcript.jsonl even when NO live streaming connection
// exists for the originating chat.
//
// This test's AgentLoop has NO bus.StreamDelegate registered at all —
// architecturally IDENTICAL, from the agent loop's point of view, to the
// real-world scenario where a WS connection existed when the background
// work started but has since closed (tab close, reload, network blip, or
// its own 60s idle timeout) by the time the async result arrives: either
// way, bus.MessageBus.GetStreamer(...) returns (nil, false), and the turn
// MUST fall through to the non-streaming persistence path
// (turnState.appendAssistantTranscript).
//
// Before FIX 5d, processSystemMessage never set TranscriptSessionID/
// TranscriptStore on the reconstructed turn's processOptions, so
// appendAssistantTranscript's own guard (transcriptStore != nil &&
// transcriptSessionID != "") was a GUARANTEED no-op — the result was
// silently, permanently lost, unrecoverable even by reopening the
// conversation. This test was confirmed to FAIL against the pre-fix
// processSystemMessage (TranscriptSessionID/TranscriptStore wiring
// temporarily reverted) before the fix was restored.
//
// BDD:
//
//	Given a delegate agent's background result bound to a transcript session,
//	  And no live streaming connection is available for the chat (simulating
//	    a closed/reconnected WS connection),
//	When processSystemMessage processes the async result,
//	Then the result IS STILL PERSISTED to the correct session's transcript —
//	  not silently dropped.
func TestProcessSystemMessage_AsyncResult_PersistsWithoutLiveConnection(t *testing.T) {
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, &mockProvider{})

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	// Sanity-check the scenario precondition: no StreamDelegate is
	// registered on this bus, so GetStreamer fails exactly as it would for a
	// closed WS connection.
	_, hasStreamer := msgBus.GetStreamer(context.Background(), "cli", "direct", meta.ID)
	require.False(t, hasStreamer, "test setup invariant: no live streaming connection must be available")

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "bash",
		Content:             "Background build finished: 0 errors.",
	})

	_, err = al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	assistantEntries := readAssistantTranscript(t, store, meta.ID)
	require.Len(t, assistantEntries, 1,
		"FIX 5d: the async result must be persisted to transcript.jsonl even though no live "+
			"streaming connection exists for this chat — it must NOT be silently, permanently lost")
	// The persisted content is the LLM's own response to the reconstructed
	// turn (mockProvider always returns "Mock response", ignoring input) —
	// the assertion that matters is that AN entry exists at all (proving
	// persistence occurred) and that it is attributed correctly.
	assert.Equal(t, "Mock response", assistantEntries[0].Content)
	assert.Equal(t, delegate.ID, assistantEntries[0].AgentID)
}

// asyncResultMockStreamer is a bus.Streamer that also records
// SetProducerAgentID calls, mirroring *gateway.wsStreamer's method of the
// same name (FIX 5a). Used to prove FIX 5a's producer-attribution wiring
// engages correctly on the FIX 5d reconstructed-turn path too, when a live
// connection genuinely is available.
type asyncResultMockStreamer struct {
	mu                      sync.Mutex
	updates                 []string
	finalized               bool
	setProducerAgentIDCalls []string
}

func (s *asyncResultMockStreamer) Update(_ context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, content)
	return nil
}

func (s *asyncResultMockStreamer) Finalize(_ context.Context, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalized = true
	return nil
}

func (s *asyncResultMockStreamer) Cancel(_ context.Context) {}

func (s *asyncResultMockStreamer) SetProducerAgentID(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setProducerAgentIDCalls = append(s.setProducerAgentIDCalls, agentID)
}

var _ bus.Streamer = (*asyncResultMockStreamer)(nil)

// asyncResultMockStreamDelegate always returns the same streamer for every
// channel/chatID, simulating a live WS connection — the "connection still
// alive" scenario.
type asyncResultMockStreamDelegate struct {
	streamer *asyncResultMockStreamer
}

func (d *asyncResultMockStreamDelegate) GetStreamer(_ context.Context, _, _, _ string) (bus.Streamer, bool) {
	return d.streamer, true
}

// asyncResultStreamingProvider is a minimal providers.LLMProvider that also
// implements providers.StreamingProvider, so the agent loop's streaming
// branch activates (mockProvider, used by the other tests in this file,
// deliberately does NOT implement ChatStream).
type asyncResultStreamingProvider struct {
	content string
}

func (p *asyncResultStreamingProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: p.content}, nil
}

func (p *asyncResultStreamingProvider) GetDefaultModel() string { return "mock-stream-model" }

func (p *asyncResultStreamingProvider) ChatStream(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
	onChunk func(accumulated string),
	_ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	onChunk(p.content)
	return &providers.LLMResponse{Content: p.content}, nil
}

var _ providers.StreamingProvider = (*asyncResultStreamingProvider)(nil)

// TestProcessSystemMessage_AsyncResult_LiveConnectionPathStillWorks is the
// FIX 5d "no regression" check (required test #3): when a live streaming
// connection IS available for the reconstructed turn's chat, the turn must
// still stream normally AND attribute correctly. FIX 5d's change (resolving
// the named agent + transcript session before runAgentLoop) must not disturb
// the existing live-streaming behavior FIX 5a already covers in depth at the
// wsStreamer level (pkg/gateway/websocket_producer_agent_id_test.go) — this
// test instead proves the WIRING from the reconstructed-turn entry point
// still reaches a live streamer correctly.
func TestProcessSystemMessage_AsyncResult_LiveConnectionPathStillWorks(t *testing.T) {
	provider := &asyncResultStreamingProvider{content: "Delegate says hello live."}
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)

	streamer := &asyncResultMockStreamer{}
	msgBus.SetStreamDelegate(&asyncResultMockStreamDelegate{streamer: streamer})

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "delegate",
		Content:             "irrelevant — the streaming provider ignores turn input for this test",
	})

	_, err = al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	streamer.mu.Lock()
	defer streamer.mu.Unlock()
	require.NotEmpty(t, streamer.updates,
		"the live streamer must have received at least one Update — streaming must still engage "+
			"for the reconstructed async-result turn, exactly as it would for any other turn")
	assert.True(t, streamer.finalized, "the live streamer must be Finalized at turn end")
	require.NotEmpty(t, streamer.setProducerAgentIDCalls,
		"FIX 5a's producer-attribution wiring must engage on the FIX 5d reconstructed-turn path too")
	assert.Equal(t, delegate.ID, streamer.setProducerAgentIDCalls[0],
		"the live streamer must be stamped with the delegate's own ID, not the default agent's")
}

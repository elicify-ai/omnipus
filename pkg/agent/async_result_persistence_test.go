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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
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

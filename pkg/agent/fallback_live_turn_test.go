// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Only the external provider transport is scripted. The ordinary inbound path,
// admission, runTurn, fallback chain, transcript and event bus remain real.
type fallbackLiveProvider struct {
	mu         sync.Mutex
	primaryErr error
	calls      []string
}

func (p *fallbackLiveProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, model)
	p.mu.Unlock()
	if model == fbNotePrimary && p.primaryErr != nil {
		return nil, p.primaryErr
	}
	return &providers.LLMResponse{Content: "live fallback answer", FinishReason: "stop"}, nil
}

func (p *fallbackLiveProvider) GetDefaultModel() string { return fbNotePrimary }

// Oracles: failure dispatch requirements 1/3/4; provider-messages spec section
// 7.4, FB-1/FB-2/FB-3, D12. A single fallback success produces exactly one live
// event with the actual session/turn/models; a primary success produces none.
func TestProviderFallbackLiveTurn(t *testing.T) {
	cases := []struct {
		name       string
		primaryErr error
		code       string
		scheduled  bool
	}{
		{
			name: "ordinary_rate_limited",
			// Over the 120-second retry ceiling: immediate fallback, no sleep.
			primaryErr: &common.ProviderError{Status: 429, Body: "rate limit exceeded", RetryAfterSeconds: 3600},
			code:       "rate_limited",
		},
		{
			name:       "ordinary_model_retired",
			primaryErr: &common.ProviderError{Status: 404, Body: "model has been decommissioned"},
			code:       "model_retired",
		},
		{
			name:       "scheduled_rate_limited",
			primaryErr: &common.ProviderError{Status: 429, Body: "rate limit exceeded", RetryAfterSeconds: 3600},
			code:       "rate_limited",
			scheduled:  true,
		},
		{name: "ordinary_primary_answers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &fallbackLiveProvider{primaryErr: tc.primaryErr}
			al, _ := newGoalLoopTestLoop(t, provider, func(cfg *config.Config) {
				// Ordinary chat addresses a colleague, never a delegation-only worker.
				cfg.Agents.List[0].Type = config.AgentTypeCustom
			})
			installTestSessionLauncher(t, al)
			agent, ok := al.GetRegistry().GetAgent("native-agent")
			require.True(t, ok)
			agent.Candidates = []providers.FallbackCandidate{
				{Provider: "prov", Model: fbNotePrimary},
				{Provider: "prov", Model: fbNoteFinal},
			}
			agent.StoreProviderPool(map[string]providers.LLMProvider{"prov": provider})
			store := al.GetSessionStore()
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", agent.ID)
			require.NoError(t, err)
			key := fmt.Sprintf("agent:%s:session:%s", agent.ID, meta.ID)

			sub := al.SubscribeEvents(128) // headroom; no lossy-subscriber ambiguity
			t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
			var tapMu sync.Mutex
			var tapped []Event
			al.SetEventSyncTap(func(evt Event) {
				if evt.Kind == EventKindProviderFallback {
					tapMu.Lock()
					tapped = append(tapped, evt)
					tapMu.Unlock()
				}
			})
			t.Cleanup(func() { al.SetEventSyncTap(nil) })

			var answer string
			if tc.scheduled {
				answer, err = al.ProcessScheduled(context.Background(), agent.ID, meta.ID, "answer me", "webchat", "fallback-live")
			} else {
				// The gateway saves the user entry before submitting this inbound
				// message. Do the same, without constructing or patching a turnState.
				require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
					ID: "fallback-live-input", Role: "user", Content: "answer me", AgentID: agent.ID, Timestamp: time.Now().UTC(),
				}))
				answer, _, err = al.processMessage(context.Background(), bus.InboundMessage{
					Channel: "webchat", ChatID: "fallback-live", SessionID: meta.ID, SessionKey: key,
					Content: "answer me", GatewayUserID: "fallback-live-user", OperatorPrompt: true,
					Metadata: map[string]string{"agent_id": agent.ID},
				})
			}
			require.NoError(t, err, "the real ordinary turn must complete")
			require.Equal(t, "live fallback answer", answer)
			wantCalls := []string{fbNotePrimary}
			wantFallbacks := 0
			if tc.primaryErr != nil {
				wantCalls = append(wantCalls, fbNoteFinal)
				wantFallbacks = 1 // one successful fallback in this turn, FB-1
			}
			provider.mu.Lock()
			calls := append([]string(nil), provider.calls...)
			provider.mu.Unlock()
			require.Equal(t, wantCalls, calls, "the real fallback chain must drive the configured models in order")

			entries, err := store.ReadTranscript(meta.ID)
			require.NoError(t, err)
			var notes []session.TranscriptEntry
			answerIndex, noteIndex := -1, -1
			for i, entry := range entries {
				if entry.Role == "assistant" && entry.Content == answer {
					answerIndex = i
				}
				if entry.Type == session.EntryTypeProviderFallback {
					notes = append(notes, entry)
					noteIndex = i
				}
			}
			require.GreaterOrEqual(t, answerIndex, 0, "the answer must be saved, not just returned")
			require.Len(t, notes, wantFallbacks, "real transcript wiring must not suppress the fallback hook")
			if wantFallbacks == 1 {
				require.Greater(t, noteIndex, answerIndex, "FB-2: saved note follows the assistant answer")
				wantNote := "Answered by the Fallback model (final-x2) because primary-y was unavailable."
				if tc.code == "model_retired" {
					wantNote += " Pick a new model in the agent's settings."
				}
				require.Equal(t, wantNote, notes[0].Content)
			}

			events := drainEvents(sub.C)
			counts := make(map[EventKind]int)
			var live []Event
			var start Event
			for _, evt := range events {
				counts[evt.Kind]++
				if evt.Kind == EventKindTurnStart {
					start = evt
				}
				if evt.Kind == EventKindProviderFallback {
					live = append(live, evt)
				}
			}
			// Positive controls: the subscriber observes the real turn lifecycle;
			// its empty fallback result cannot be mistaken for a disconnected bus.
			for _, kind := range []EventKind{EventKindTurnStart, EventKindLLMRequest, EventKindLLMResponse, EventKindTurnEnd} {
				require.Equal(t, 1, counts[kind], "positive control: %s", kind)
			}
			require.Zero(t, al.EventDrops(EventKindProviderFallback), "subscriber overflow is not this defect")
			tapMu.Lock()
			tapEvents := append([]Event(nil), tapped...)
			tapMu.Unlock()
			require.Len(t, live, wantFallbacks, "exactly one live provider_fallback event must reach the ordinary turn's subscriber")
			require.Len(t, tapEvents, wantFallbacks, "the gateway's synchronous bus tap must receive the same event")
			if wantFallbacks == 1 {
				require.NotEmpty(t, start.Meta.TurnID)
				require.Equal(t, EventMeta{
					AgentID: agent.ID, TurnID: start.Meta.TurnID, SessionKey: key, Iteration: 1,
					Source: "runTurn", TracePath: "turn.llm.fallback",
				}, live[0].Meta)
				require.Equal(t, ProviderFallbackPayload{
					SessionID: meta.ID, TurnID: start.Meta.TurnID, AnsweredModel: fbNoteFinal,
					UnavailableModel: fbNotePrimary, UnavailableCode: tc.code,
				}, live[0].Payload)
				require.Equal(t, live[0], tapEvents[0])
			}
		})
	}
	t.Run("missing_publishers_are_observable", testFallbackMissingPublisherDiagnostics)
}

// Dispatch requirement 4: a missing loop or bus is a wiring failure, not a
// reason to panic or silently discard the event. Logs expose a monotonic count
// and routing identity, but never the (potentially sensitive) event payload.
func testFallbackMissingPublisherDiagnostics(t *testing.T) {
	readLog := captureLogFile(t, logger.INFO)
	meta := EventMeta{AgentID: "native-agent", TurnID: "diagnostic-turn", Source: "fallback-live-test"}
	const privatePayload = "private-event-payload-must-not-be-logged"
	var missingLoop *AgentLoop
	missingLoop.emitEvent(EventKindProviderFallback, meta, privatePayload)
	(&AgentLoop{}).emitEvent(EventKindProviderFallback, meta, privatePayload)

	// A valid publisher still emits normally and must not increment suppression.
	al := &AgentLoop{eventBus: NewEventBus()}
	sub := al.SubscribeEvents(1)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	suppressedBeforeHealthy := eventEmissionsSuppressed.Load()
	al.emitEvent(EventKindProviderFallback, meta, privatePayload)
	require.Len(t, drainEvents(sub.C), 1)
	require.Equal(t, suppressedBeforeHealthy, eventEmissionsSuppressed.Load(),
		"a healthy publication must leave the suppression counter unchanged")

	logged := readLog()
	require.NotContains(t, logged, privatePayload)
	var suppressed []map[string]any
	decoder := json.NewDecoder(strings.NewReader(logged))
	for decoder.More() {
		var record map[string]any
		require.NoError(t, decoder.Decode(&record))
		if record["message"] == "Agent event suppressed: publisher is not initialized" {
			suppressed = append(suppressed, record)
		}
	}
	require.Len(t, suppressed, 2, "nil loop and nil event bus must each produce one warning, never a silent drop")
	for i, reason := range []string{"nil_loop", "nil_event_bus"} {
		require.Equal(t, "warn", suppressed[i]["level"])
		require.Equal(t, "eventbus", suppressed[i]["component"])
		require.Equal(t, reason, suppressed[i]["reason"])
		require.Equal(t, "provider_fallback", suppressed[i]["event_kind"])
		require.Equal(t, meta.AgentID, suppressed[i]["agent_id"])
		require.Equal(t, meta.TurnID, suppressed[i]["turn_id"])
		require.Equal(t, meta.Source, suppressed[i]["source"])
	}
	first, ok := suppressed[0]["suppressed_total"].(float64)
	require.True(t, ok, "the warning must expose the suppression counter")
	require.GreaterOrEqual(t, first, float64(1))
	require.Equal(t, first+1, suppressed[1]["suppressed_total"], "each suppressed emission increments the counter exactly once")
}

package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

const (
	wu4TranscriptInitialProbeID      = "wu4-transcript-initial-probe"
	wu4TranscriptContinuationProbeID = "wu4-transcript-continuation-probe"
	wu4TranscriptSecretCallID        = "wu4-transcript-real-secret-card"
)

type wu4TranscriptHarness struct {
	f        *secretWakeAcceptanceFixture
	provider *wu4OriginProvider
	probe    *wu4OriginProbe
	cards    []secretrequest.Record
}

func wu4TranscriptSecretCall() providers.LLMResponse {
	return providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: wu4TranscriptSecretCallID, Type: "function", Name: "request_secret",
		Arguments: map[string]any{"purpose": "OpenAI continuation setup key", "endpoint": "https://api.openai.com/v1"},
	}}}
}

// Reuse the unchanged real-loop fixture and context observer. Only external
// model responses differ; request_secret remains the registered builtin.
func newWU4TranscriptHarness(t *testing.T, direct bool) *wu4TranscriptHarness {
	t.Helper()
	f, provider, probe := wu4OriginHarness(t, false)
	provider.responses = []providers.LLMResponse{
		wu4OriginCall(wu4TranscriptInitialProbeID),
		{Content: "original web turn finished", FinishReason: "stop"},
		wu4OriginCall(wu4TranscriptContinuationProbeID),
		wu4TranscriptSecretCall(),
		{Content: "model reached after the secret request failed", FinishReason: "stop"},
	}
	if direct {
		provider.responses = []providers.LLMResponse{
			wu4OriginCall(wu4TranscriptInitialProbeID), wu4TranscriptSecretCall(),
			{Content: "model reached after the secret request failed", FinishReason: "stop"},
		}
	}
	h := &wu4TranscriptHarness{f: f, provider: provider, probe: probe}
	f.al.SecretRequests().SetSink(func(rec secretrequest.Record) {
		if rec.Card.State == "pending" {
			h.cards = append(h.cards, rec)
		}
	})
	return h
}

func (h *wu4TranscriptHarness) initialMessage() bus.InboundMessage {
	return bus.InboundMessage{
		Channel: "webchat", ChatID: "transcript-context-chat", SessionID: h.f.root,
		GatewayUserID: "qa-secret-wake-owner", Content: "Start the original web conversation",
	}
}

// The sync event tap runs on the original processTurn call stack: the turn's
// body has retired, but its worker has not yet reached its post-turn drain.
// Deliver through the real worker enqueue, never a hand-built processOptions.
type wu4TranscriptClosingArrival struct {
	handler *sessionWorker
	scope   string
	input   bus.InboundMessage
	once    sync.Once
	calls   int
	handled bool
	retry   bool
	depth   int
	err     error
}

func (a *wu4TranscriptClosingArrival) observe(event Event) {
	if event.Kind != EventKindTurnEnd || event.Meta.SessionKey != a.scope {
		return
	}
	a.once.Do(func() {
		a.calls++
		if a.handler.parent.GetActiveTurnBySession(a.scope) != nil {
			a.err = fmt.Errorf("instrument: original turn is still active at the selected post-turn boundary")
			return
		}
		a.handled, a.retry = a.handler.enqueueOrRetry(a.input)
		a.depth = a.handler.parent.pendingSteeringCountForScope(a.scope)
	})
}

func (h *wu4TranscriptHarness) runWorkerContinuation(t *testing.T, kind string) bus.InboundMessage {
	t.Helper()
	initial := h.initialMessage()
	target, err := h.f.al.buildContinuationTarget(initial)
	require.NoError(t, err)
	input := wu4OriginInput(t, h.f, h.f.root, kind)
	worker := newSessionWorker(target.SessionKey, h.f.al, func() {})
	t.Cleanup(worker.cancel)
	arrival := &wu4TranscriptClosingArrival{handler: worker, scope: target.SessionKey, input: input}
	h.f.al.SetEventSyncTap(arrival.observe)
	t.Cleanup(func() { h.f.al.SetEventSyncTap(nil) })
	// The real ordinary worker itself owns and drives Continue. No parallel
	// test process, sleep, fabricated turn state or second executor is used.
	worker.processTurn(context.Background(), initial)
	require.NoError(t, arrival.err)
	require.Equal(t, 1, arrival.calls, "instrument: one arrival while the first worker still owns processTurn")
	require.True(t, arrival.handled, "instrument: the actual worker must accept the input")
	require.False(t, arrival.retry, "instrument: input did not fall back to a different worker")
	require.Equal(t, 1, arrival.depth, "instrument: input joined the post-turn steering queue, not the next-turn inbox")
	require.Zero(t, h.f.al.pendingSteeringCountForScope(target.SessionKey), "production Continue must really consume the input")
	return input
}

func (h *wu4TranscriptHarness) requireInputConsumedOnce(t *testing.T, input bus.InboundMessage) {
	t.Helper()
	requests := h.provider.snapshot()
	// The first turn makes its observer call and final reply; the THIRD real
	// provider request is the continuation's first, after destructive dequeue.
	require.GreaterOrEqual(t, len(requests), 3, "instrument: the real continuation reached the model")
	copies := 0
	for _, message := range requests[2] {
		if message.Role == "user" && message.Content == input.Content {
			copies++
		}
	}
	require.Equal(t, 1, copies, "the late input is consumed by this new turn exactly once")
}

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Oracles: addendum-f1-network-card.md; handoff-2b.txt sections 1.1/1.6 B.
// Expected text comes from the message catalogue via UserMessageForCode.
// Codegen pins its wording to contracts/components/schemas/LLMError.yaml's
// x-user-messages; catalogue tests enforce coverage. Mutant f1 must still fail:
// removing transport promotion changes the emitted code/text, not this oracle.
var f1NetworkUserMessage = UserMessageForCode(CodeNetwork)

func TestF1DroppedSend_BareEOFExhaustionEndsWithNetworkCard(t *testing.T) {
	got := observeF1DroppedSend(t, f1DroppedSendError(io.EOF))
	assertF1NetworkExhaustion(t, got)
}

// Regression control: unexpected EOF already has transport vocabulary. Keep
// this full exhausted-turn path green while repairing the bare-EOF path.
func TestF1DroppedSend_UnexpectedEOFExhaustionEndsWithNetworkCard(t *testing.T) {
	got := observeF1DroppedSend(t, f1DroppedSendError(io.ErrUnexpectedEOF))
	assertF1NetworkExhaustion(t, got)
}

func TestF1DroppedSend_ConnectionResetExhaustionKeepsNetworkCard(t *testing.T) {
	got := observeF1DroppedSend(t, errors.New("read tcp 10.0.0.1:443: read: connection reset by peer"))
	assertF1NetworkExhaustion(t, got)
}

func TestF1DroppedSend_EOFInsideTokenStaysNonNetwork(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"plain GEOFENCE token", errors.New("adapter rejected GEOFENCE-1")},
		{"HTTP 400 EOFError token", &common.ProviderError{Status: 400, Body: "EOFError in user script"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := observeF1DroppedSend(t, tc.err)
			assert.NotEqual(t, "network", got.card.Code, "letters inside a token are not a dropped connection")
			assert.NotEqual(t, f1NetworkUserMessage, got.card.Message)
			assert.NotEqual(t, "network", string(got.turn.Code))
			assert.NotEqual(t, f1NetworkUserMessage, got.turn.Message)
			assert.Equal(t, 1, got.calls, "a non-network token must not acquire outage retries")
			assert.Empty(t, got.waits)
			assert.Empty(t, got.retries)
		})
	}
}

func TestF1DroppedSend_AuthenticationFailureKeepsItsOwnMessage(t *testing.T) {
	got := observeF1DroppedSend(t, &common.ProviderError{
		Status: 401,
		Body:   `{"error":{"message":"invalid api key"}}`,
	})
	// The fixture injects an unnamed provider, so the contract's facts-absent
	// authentication copy applies, not its named-provider template.
	authMessage := UserMessageForCode(CodeProviderAuthFailed)
	assert.Equal(t, "provider_auth_failed", got.card.Code)
	assert.Equal(t, authMessage, got.card.Message, "a real credentials failure keeps its own actionable text")
	assert.Equal(t, "provider_auth_failed", string(got.turn.Code))
	assert.Equal(t, authMessage, got.turn.Message)
	assert.False(t, got.card.Retryable)
	assert.Equal(t, 1, got.calls, "repeating an invalid credential is not a network retry")
	assert.Empty(t, got.waits)
	assert.Empty(t, got.retries)
}

func f1DroppedSendError(cause error) error {
	return fmt.Errorf("failed to send request: %w", &url.Error{
		Op: "Post", URL: "http://127.0.0.1:15611/v1/chat/completions", Err: cause,
	})
}

// Internal observation, not a hand-written wire shape: card is the generated
// wire type returned by the real assembly point, from an actual emitted event.
type f1DroppedSendObservation struct {
	card    generated.LLMError
	turn    LLMError
	calls   int
	waits   []time.Duration
	retries []LLMRetryPayload
}

func observeF1DroppedSend(t *testing.T, providerErr error) f1DroppedSendObservation {
	t.Helper()
	provider := &issue1280CountingProvider{err: providerErr}
	al, waits := issue1280Loop(t, provider)
	sub := al.eventBus.Subscribe(256)
	t.Cleanup(func() { al.eventBus.Unsubscribe(sub.ID) })
	_, err := al.runAgentLoop(context.Background(), al.GetRegistry().primaryChatAgentForTest(), processOptions{
		SessionKey: "f1-dropped-send", Channel: "web", ChatID: "f1-dropped-send-chat",
		UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
	})
	require.Error(t, err, "instrument: the real provider failure must end the turn")
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	require.Positive(t, calls, "instrument: reach the provider, not an earlier configuration refusal")

	var terminal []ErrorPayload
	var retries []LLMRetryPayload
drain:
	for {
		select {
		case evt, open := <-sub.C:
			require.True(t, open, "instrument: the event bus remains open until cleanup")
			switch evt.Kind {
			case EventKindError:
				payload, ok := evt.Payload.(ErrorPayload)
				require.True(t, ok, "instrument: terminal event has its real error payload")
				terminal = append(terminal, payload)
			case EventKindProviderRetry:
				payload, ok := evt.Payload.(LLMRetryPayload)
				require.True(t, ok)
				retries = append(retries, payload)
			}
		default:
			break drain
		}
	}
	require.Len(t, terminal, 1, "instrument: inspect the one actual terminal card, not a synthetic payload")
	require.Equal(t, "llm", terminal[0].Stage)
	require.Equal(t, "f1-dropped-send-chat", terminal[0].ChatID)
	got := f1DroppedSendObservation{
		card: WireLLMError(terminal[0]), turn: TranslateTurnError(err),
		calls: calls, waits: append([]time.Duration(nil), (*waits)...), retries: retries,
	}
	t.Logf("real turn ended: attempts=%d waits=%v retry_frames=%d; emitted code=%q message=%q; returned code=%q",
		got.calls, got.waits, len(got.retries), got.card.Code, got.card.Message, got.turn.Code)
	return got
}

func assertF1NetworkExhaustion(t *testing.T, got f1DroppedSendObservation) {
	t.Helper()
	// Card assertions intentionally come before the nonfatal schedule checks:
	// this base lacks the classifier fix too, but must still expose F1's wrong
	// final code/text instead of stopping at its missing retries.
	assert.Equal(t, "network", got.card.Code, "F1: exhaustion names the dropped connection, never unknown")
	assert.Equal(t, f1NetworkUserMessage, got.card.Message, "F1: the person sees the full network message, not 'can’t tell why'")
	assert.Equal(t, "network", string(got.turn.Code))
	assert.Equal(t, f1NetworkUserMessage, got.turn.Message)
	assert.True(t, got.card.Retryable)
	assert.Equal(t, 6, got.calls, "exactly one six-attempt plan, not a second loop-level retry cycle")
	assert.Equal(t, []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}, got.waits)
	if assert.Len(t, got.retries, 5, "each decided retry is announced") {
		for i, retry := range got.retries {
			assert.Equal(t, i+2, retry.Attempt)
			assert.Equal(t, 6, retry.MaxAttempts)
			assert.Equal(t, "network", retry.Reason)
		}
	}
}

// loop_run_turn_retry_f1_test.go — gate finding F1: a ROOT turn with a
// SINGLE candidate must run the §7.4 fallback chain (D3), so a 429 retries
// the candidate in place (C-8) with one provider_retry event per decided
// retry, instead of failing the turn with the raw rate-limit error as the
// plain path did. The provider_retry event proves the C-11 observer fired;
// the provider call count proves the retry actually happened; the successful
// return proves retry-then-terminal.
package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallProviderOnce_SingleCandidateRetriesWithFrame_F1(t *testing.T) {
	provider := &failingThenSuccessProvider{
		failures: []error{errors.New("429 Too Many Requests")},
		successResp: &providers.LLMResponse{
			Content:   "recovered after in-place retry",
			ToolCalls: []providers.ToolCall{},
		},
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	subs := al.eventBus.Subscribe(16)
	var retried int
	drain := func() {
		for {
			select {
			case evt := <-subs.C:
				if evt.Kind == EventKindProviderRetry {
					retried++
					p, ok := evt.Payload.(LLMRetryPayload)
					if !ok {
						t.Fatalf("provider_retry payload is %T, want LLMRetryPayload", evt.Payload)
					}
					assert.Equal(t, 2, p.Attempt, "the retry event announces the call ABOUT to be made (C-8)")
					assert.Equal(t, 3, p.MaxAttempts)
					assert.Equal(t, "test-model", p.Model)
				}
			default:
				return
			}
		}
	}

	_, err := al.runAgentLoop(context.Background(), al.GetRegistry().GetDefaultAgent(), processOptions{
		SessionKey:      "f1-retry-session",
		Channel:         "web",
		ChatID:          "f1-chat",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err, "the turn must succeed after the in-place retry; got: %v", err)
	drain()
	assert.Equal(t, 2, provider.callIdx,
		"provider must be called exactly twice: the 429 then the in-place retry (C-8)")
	assert.Equal(t, 1, retried,
		"exactly one DECIDED in-place retry (C-11: one provider_retry event per decision)")
}

// C-8's other end: a candidate that rate-limits on EVERY attempt exhausts
// its 3 total calls and fails the turn — the error the turn reports must
// still carry the real rate-limit failure (errorToProviderError walks the
// FallbackExhaustedError), not a cooldown-skip generic message (F1 uses a
// throwaway tracker, so nothing was ever marked).
func TestCallProviderOnce_SingleCandidateAlways429_ExhaustsThreeCalls_F1(t *testing.T) {
	provider := &failingThenSuccessProvider{
		failures: []error{
			errors.New("429 Too Many Requests"),
			errors.New("429 Too Many Requests"),
			errors.New("429 Too Many Requests"),
			errors.New("429 Too Many Requests"),
		},
		successResp: &providers.LLMResponse{Content: "unreachable"},
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	_, err := al.runAgentLoop(context.Background(), al.GetRegistry().GetDefaultAgent(), processOptions{
		SessionKey:      "f1-exhaust-session",
		Channel:         "web",
		ChatID:          "f1-exhaust-chat",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.Error(t, err, "an always-429 candidate must fail the turn after exhausting its retries")
	assert.GreaterOrEqual(t, provider.callIdx, 3,
		"C-8: the single candidate must get its 3 total calls")
}

// f1ScriptedProvider is a two-mode provider: fail429 answers every call with
// a rate limit until switched off, then answers with content. The switch is
// what lets ONE AgentLoop drive a failing turn and then a live turn.
type f1ScriptedProvider struct {
	mu      sync.Mutex
	fail429 bool
	calls   int
}

func (p *f1ScriptedProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.fail429 {
		return nil, errors.New("429 Too Many Requests")
	}
	return &providers.LLMResponse{Content: "second turn answered", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *f1ScriptedProvider) GetDefaultModel() string { return "test-model" }

func (p *f1ScriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *f1ScriptedProvider) setFail429(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail429 = v
}

// CHECK finding (audit of the F1 fix): the throwaway cooldown tracker is the
// single-candidate chain's central safety property — today's plain path marks
// nothing, so a turn-1 429 exhaustion must never mark the SHARED chain's
// tracker; if it did, turn 2's only candidate would be cooldown-skipped and
// the turn would fail with a generic all-skipped exhaustion instead of
// reaching the provider. The two single-turn F1 tests above cannot see this
// (the difference only exists BETWEEN turns on one AgentLoop) — proven by
// mutation: removing WithCooldown(NewCooldownTracker()) leaves both green.
// This test pins the property on a second turn.
func TestCallProviderOnce_SingleCandidateExhaustion_NeverCooldownSkipsNextTurn_F1(t *testing.T) {
	provider := &f1ScriptedProvider{fail429: true}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	run := func(session string) error {
		_, err := al.runAgentLoop(context.Background(), al.GetRegistry().GetDefaultAgent(), processOptions{
			SessionKey:      session,
			Channel:         "web",
			ChatID:          "f1-cd-chat",
			UserMessage:     "hello",
			DefaultResponse: defaultResponse,
			SendResponse:    false,
		})
		return err
	}

	err := run("f1-cd-turn-1")
	require.Error(t, err, "turn 1 (always-429) must fail after exhausting its 3 calls")
	require.GreaterOrEqual(t, provider.callCount(), 3,
		"C-8: turn 1 must get its 3 total calls before failing")

	provider.setFail429(false)
	err = run("f1-cd-turn-2")
	require.NoError(t, err,
		"turn 2 must run: a turn-1 429 exhaustion must never mark the SHARED tracker "+
			"(F1's throwaway tracker) — a cooldown-skip here would fail the turn with a "+
			"generic all-skipped exhaustion instead of reaching the provider")
	require.GreaterOrEqual(t, provider.callCount(), 4,
		"turn 2 must actually reach the provider, not skip it in cooldown")
}

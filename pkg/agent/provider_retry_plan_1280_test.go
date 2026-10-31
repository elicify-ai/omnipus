package agent

// Issue #1280: a provider outage on a single-model agent is retried by the
// fallback chain's retry plan — 1 call plus 5 retries, one provider_retry
// event per decided retry from the first failure — and then the turn ends
// with the plain-language network cause. The agent loop must not retry the
// exhausted plan again (that multiplied the calls and the wait well past the
// founder's "~1.5 min, then the error card").

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

type issue1280CountingProvider struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (p *issue1280CountingProvider) GetDefaultModel() string { return "test-model" }

func (p *issue1280CountingProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return nil, p.err
}

func issue1280Loop(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *[]time.Duration) {
	t.Helper()
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
	var waits []time.Duration
	al.fallback = al.fallback.WithRetryWait(func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	})
	return al, &waits
}

func TestIssue1280_SingleModelOutageEndsAfterOneRetryPlan(t *testing.T) {
	provider := &issue1280CountingProvider{err: errors.New(`Post "https://openrouter.ai/api/v1/chat/completions": dial tcp: lookup openrouter.ai: no such host`)}
	al, waits := issue1280Loop(t, provider)
	subs := al.eventBus.Subscribe(64)
	_, err := al.runAgentLoop(context.Background(), al.GetRegistry().primaryChatAgentForTest(), processOptions{
		SessionKey: "issue-1280-outage", Channel: "web", ChatID: "issue-1280-chat",
		UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
	})
	require.Error(t, err, "an outage that outlasts the plan ends the turn")

	var retries []LLMRetryPayload
	for done := false; !done; {
		select {
		case evt := <-subs.C:
			if evt.Kind == EventKindProviderRetry {
				p, ok := evt.Payload.(LLMRetryPayload)
				require.True(t, ok)
				retries = append(retries, p)
			}
		default:
			done = true
		}
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	require.Equal(t, 6, calls, "#1280: exactly one retry plan — 1 call + 5 retries — never re-run by the agent loop")
	require.Equal(t, []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}, *waits,
		"#1280 Q1 waits, once")
	require.Len(t, retries, 5, "#1280 Q2: the retry state is announced from the first failed attempt")
	for i, p := range retries {
		require.Equal(t, i+2, p.Attempt)
		require.Equal(t, 6, p.MaxAttempts)
		require.Equal(t, "network", p.Reason, "cause class for the plain-language line")
	}
	require.Equal(t, CodeNetwork, TranslateTurnError(err).Code, "the final card names the network cause, not 'we can't tell why'")
}

func TestIssue1280_AccessProblemEndsTheTurnAtOnce(t *testing.T) {
	provider := &issue1280CountingProvider{err: errors.New("API request failed: status=401 body={\"error\":{\"message\":\"invalid api key\"}}")}
	al, waits := issue1280Loop(t, provider)
	_, err := al.runAgentLoop(context.Background(), al.GetRegistry().primaryChatAgentForTest(), processOptions{
		SessionKey: "issue-1280-auth", Channel: "web", ChatID: "issue-1280-auth-chat",
		UserMessage: "hello", DefaultResponse: defaultResponse, SendResponse: false,
	})
	require.Error(t, err)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	require.Equal(t, 1, provider.calls, "#1280: an access problem is never retried")
	require.Empty(t, *waits)
}

package providers

// Issue #1664 B9 (founder decision 2026-11-07 Q3 A): the empty stream's
// error classifies as FailoverTimeout — so a configured fallback model is
// tried — and RetryCauseOf returns "" so the #1280 retry plan does not take
// it. Oracle: /workspace/logs/fix-1664/verify-report.md §B9. The error is
// produced through the real exported transport against a local SSE server
// that answers only [DONE] (no finish_reason, no content, no tool calls) —
// never by constructing an expected error by hand. Until B8 lands, the
// transport returns a successful empty answer, so there is nothing to
// classify and this test fails on that first (the honest transitive RED;
// the failure message names it).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

func TestIssue1664_B9_ClassifyEmptyStreamAsFailoverTimeoutOutsideRetryPlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	provider, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout("k", server.URL, "", "", 0, nil)
	require.NoError(t, err, "SETUP: transport provider")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, streamErr := provider.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil, nil, nil)
	require.Error(t, streamErr,
		"#1664 B8 (transitive): the empty stream must fail as a typed error before it can be classified — today it parses as a successful empty answer, so there is nothing to classify")
	fe := ClassifyError(streamErr, "p", "m")
	require.NotNil(t, fe, "#1664 B9: the empty-stream error must classify (never fall through unclassified)")
	require.Equal(t, FailoverTimeout, fe.Reason,
		"#1664 B9 (founder Q3 A): the empty stream classifies as FailoverTimeout so a configured fallback model is tried")
	require.Equal(t, "", RetryCauseOf(streamErr),
		"#1664 B9: RetryCauseOf returns \"\" — the #1280 retry plan does not take the empty stream")
}

// issue1664ChainProvider is a scripted provider for the chain test below:
// every call returns the fixed response/error and counts itself.
type issue1664ChainProvider struct {
	mu    sync.Mutex
	calls int
	resp  *LLMResponse
	err   error
}

func (p *issue1664ChainProvider) Chat(_ context.Context, _ []Message, _ []ToolDefinition, _ string, _ map[string]any) (*LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.resp, p.err
}

func (p *issue1664ChainProvider) GetDefaultModel() string { return "m" }

func (p *issue1664ChainProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestIssue1664_B9_EmptyStreamFailsOverToTheConfiguredBackup — CHECK F5
// (round 3): founder Q3 A's promise driven to its outcome. The primary
// model's stream ends empty (the typed error, exactly what the real
// transport produces per B8); the chain classifies it as FailoverTimeout
// (outside the #1280 plan) and invokes the CONFIGURED BACKUP, whose reply
// is the turn's answer. Until this test, the pack stopped at
// classification and never proved the backup is actually called.
func TestIssue1664_B9_EmptyStreamFailsOverToTheConfiguredBackup(t *testing.T) {
	primary := &issue1664ChainProvider{err: common.NewEmptyStreamError(&common.UsageInfo{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7})}
	backup := &issue1664ChainProvider{resp: &LLMResponse{Content: "backup answer"}}
	chain := NewFallbackChain(NewCooldownTracker())
	candidates := []FallbackCandidate{{Provider: "p1", Model: "m1"}, {Provider: "p2", Model: "m2"}}
	pool := map[string]LLMProvider{"p1": primary, "p2": backup}

	res, err := chain.Execute(context.Background(), candidates,
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			return pool[provider].Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil, model, nil)
		})
	require.NoError(t, err, "#1664 B9 (founder Q3 A): a configured backup model answers after the primary's empty stream — the turn succeeds")
	require.NotNil(t, res)
	require.Equal(t, "backup answer", res.Response.Content,
		"#1664 B9: the turn's answer is the BACKUP's reply, not an empty success")
	require.Equal(t, "p2", res.Provider, "#1664 B9: the answering candidate is the configured backup")
	require.Equal(t, 1, primary.callCount(), "the primary was tried once")
	require.Equal(t, 1, backup.callCount(), "the backup was invoked exactly once")
	require.NotEmpty(t, res.Attempts,
		"#1664 B9: the primary's failed attempt is recorded on the way to the backup (FallbackAttempt records failures — the answering candidate is res.Provider/Model)")
	require.ErrorIs(t, res.Attempts[0].Error, common.ErrEmptyStream,
		"#1664 B9: the primary's recorded failure is the empty stream itself")
	require.Equal(t, FailoverTimeout, res.Attempts[0].Reason,
		"#1664 B9: the chain classified the empty stream as FailoverTimeout (the fallback class)")
	require.False(t, res.Attempts[0].RetryPlan,
		"#1664 B9: the empty stream never consumes the #1280 retry plan (RetryCauseOf is empty)")
}

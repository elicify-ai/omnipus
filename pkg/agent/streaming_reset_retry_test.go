// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// streaming_reset_retry_test.go — regression tests for transient mid-stream
// provider reset retry logic.
//
// Root cause: upstream HTTP/2 connections drop mid-SSE-stream before any
// tokens are emitted ("streaming read error: http2: response body closed").
// Without the fix the agent loop classified the error as nil/terminal and
// returned 0 tokens. With the fix the loop inline-retries up to maxRetries
// times (currently 2) and the turn SUCCEEDS when a later attempt works.
//
// Three test groups:
//   1. isTransientStreamError unit tests — tight positive/negative coverage.
//   2. FallbackExhaustedError retry — all-transient exhausted chain is retried.
//   3. runAgentLoop integration — end-to-end: loop retries and returns response.
//
// All tests run with CGO_ENABLED=0 and -tags goolm,stdjson; no real network.

package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ---------------------------------------------------------------------------
// Group 1: isTransientStreamError unit tests
// ---------------------------------------------------------------------------

// TestIsTransientStreamError_TrueForKnownPatterns verifies that every string
// the task description identifies as a transient streaming reset is detected.
//
// BDD: Given a known transient streaming/connection-reset error message,
// When isTransientStreamError is called,
// Then it returns true.
func TestIsTransientStreamError_TrueForKnownPatterns(t *testing.T) {
	positives := []struct {
		name string
		msg  string
	}{
		// The canonical case from the e2e log that triggered this fix.
		{"streaming read error wrapping http2 body closed", "streaming read error: http2: response body closed"},
		// Direct http2 body-closed sentinel.
		{"http2 response body closed direct", "http2: response body closed"},
		// HTTP/2 GOAWAY – non-graceful (INTERNAL_ERROR etc.).
		{
			"goaway non-graceful",
			"http2: server sent GOAWAY and closed the connection; LastStreamID=5, ErrCode=INTERNAL_ERROR, debug=\"\"",
		},
		// HTTP/2 GOAWAY – graceful shutdown (load-balancer recycling).
		{"goaway graceful", "http2: Transport received Server's graceful shutdown GOAWAY"},
		// http2StreamError.Error() format.
		{"stream error INTERNAL_ERROR", "stream error: stream ID 7; INTERNAL_ERROR"},
		// TCP-level resets.
		{"connection reset by peer", "read tcp 10.0.0.1:443: read: connection reset by peer"},
		{"unexpected eof", "unexpected EOF"},
		{"broken pipe", "write tcp: broken pipe"},
		// Miscellaneous connection drops.
		{"use of closed network connection", "use of closed network connection"},
		{"server closed idle connection", "http: server closed idle connection"},
		{"connection closed", "connection closed before response was received"},
		// Wrapped in provider error message.
		{
			"wrapped streaming reset",
			"failed to send request: Post \"https://openrouter.ai/api/v1/chat/completions\": streaming read error: http2: response body closed",
		},
		// Case-insensitive check.
		{"uppercase variant", "STREAMING READ ERROR: HTTP2: RESPONSE BODY CLOSED"},
	}

	for _, tc := range positives {
		t.Run(tc.name, func(t *testing.T) {
			err := errors.New(tc.msg)
			assert.True(t, isTransientStreamError(err),
				"isTransientStreamError should return true for transient reset: %q", tc.msg)
		})
	}
}

// TestIsTransientStreamError_FalseForNonTransient verifies that auth failures,
// 4xx application errors, and clean EOF are NOT mis-detected as streaming resets.
//
// BDD: Given a non-transient provider error,
// When isTransientStreamError is called,
// Then it returns false (so the loop does not retry auth/format errors).
func TestIsTransientStreamError_FalseForNonTransient(t *testing.T) {
	negatives := []struct {
		name string
		msg  string
	}{
		{"auth failure 401", "API request failed: Status: 401 invalid api key"},
		{"rate limit 429", "too many requests: rate limit exceeded"},
		{"context overflow", "context length exceeded: 32768 tokens"},
		{"clean EOF (normal stream end)", "EOF"},
		{"validation error", "invalid request format: tool_use_id is required"},
		{"billing error", "insufficient credits for this request"},
		{"404 not found", "404 No endpoints found for z-ai/glm-5-turbo"},
		{"empty string", ""},
		{"nil error", ""},
	}

	for _, tc := range negatives {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.msg != "" {
				err = errors.New(tc.msg)
			}
			assert.False(t, isTransientStreamError(err),
				"isTransientStreamError should return false for %q", tc.msg)
		})
	}
}

// ---------------------------------------------------------------------------
// Group 2: FallbackExhaustedError retry path
// ---------------------------------------------------------------------------

// failingThenSuccessProvider returns an error for the first N calls and then
// returns a successful response. It is used to simulate a provider that drops
// the connection transiently and then recovers.
type failingThenSuccessProvider struct {
	failures    []error
	successResp *providers.LLMResponse
	callIdx     int
}

func (p *failingThenSuccessProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if p.callIdx < len(p.failures) {
		err := p.failures[p.callIdx]
		p.callIdx++
		return nil, err
	}
	p.callIdx++
	return p.successResp, nil
}

func (p *failingThenSuccessProvider) GetDefaultModel() string { return "test-model" }

// TestRetryOnStreamingReset_SingleCandidateTurnSucceeds is the primary
// integration test for the fix. It drives runAgentLoop with a provider that
// returns "streaming read error: http2: response body closed" on the first
// call and a successful response on the second, asserting the turn SUCCEEDS.
//
// BDD: Given a provider that drops the HTTP/2 connection before the first token,
// When the agent loop handles the first call error,
// Then it retries inline (isTimeoutError path via ClassifyError → FailoverTimeout),
// And the second call succeeds, returning a non-empty response to the caller.
func TestRetryOnStreamingReset_SingleCandidateTurnSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceDir := tmpDir

	streamingResetErr := errors.New("streaming read error: http2: response body closed")
	provider := &failingThenSuccessProvider{
		failures: []error{streamingResetErr},
		successResp: &providers.LLMResponse{
			Content:   "retry succeeded",
			ToolCalls: []providers.ToolCall{},
		},
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	ctx := context.Background()
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent, "default agent must be registered")

	_, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "stream-retry-session",
		Channel:         "web",
		ChatID:          "test-chat",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err,
		"turn must succeed after retrying a transient streaming reset; got error: %v", err)
	assert.Equal(t, 2, provider.callIdx,
		"provider must be called exactly twice: once for the reset, once for the success")
}

// TestRetryOnStreamingReset_GoAwayTurnSucceeds verifies the GOAWAY INTERNAL_ERROR
// variant (which was previously unclassified by ClassifyError → nil → break).
//
// BDD: Given a provider that returns an HTTP/2 GOAWAY INTERNAL_ERROR,
// When the agent loop handles the error (ClassifyError previously returned nil),
// Then isTransientStreamError catches it and sets isTimeoutError = true,
// And the turn retries and succeeds.
func TestRetryOnStreamingReset_GoAwayTurnSucceeds(t *testing.T) {
	tmpDir := t.TempDir()

	goAwayErr := errors.New(
		`http2: server sent GOAWAY and closed the connection; LastStreamID=5, ErrCode=INTERNAL_ERROR, debug=""`,
	)
	provider := &failingThenSuccessProvider{
		failures: []error{goAwayErr},
		successResp: &providers.LLMResponse{
			Content:   "goaway retry succeeded",
			ToolCalls: []providers.ToolCall{},
		},
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	ctx := context.Background()
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	_, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "goaway-retry-session",
		Channel:         "web",
		ChatID:          "test-chat-goaway",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err,
		"turn must succeed after retrying a GOAWAY INTERNAL_ERROR reset; got error: %v", err)
	assert.Equal(t, 2, provider.callIdx,
		"provider must be called exactly twice: once for the GOAWAY, once for the success")
}

// TestRetryOnStreamingReset_ExhaustsMaxRetries verifies that when every retry
// also fails, the turn eventually surfaces an error rather than retrying forever.
//
// BDD: Given a provider that ALWAYS returns streaming reset errors,
// When the agent loop exhausts maxRetries,
// Then the turn returns an error (not an infinite loop).
func TestRetryOnStreamingReset_ExhaustsMaxRetries(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	streamErr := errors.New("streaming read error: http2: response body closed")
	// Enough failures to exhaust maxRetries (currently 2; provide one extra so
	// exhausted == all calls were errors regardless of exact maxRetries value).
	provider := &failingThenSuccessProvider{
		failures: []error{streamErr, streamErr, streamErr, streamErr},
		successResp: &providers.LLMResponse{
			Content:   "unreachable",
			ToolCalls: []providers.ToolCall{},
		},
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	ctx := context.Background()
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	_, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "exhaust-retry-session",
		Channel:         "web",
		ChatID:          "test-chat-exhaust",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.Error(t, err,
		"turn must fail after exhausting maxRetries on persistent streaming resets")
}

// TestRetryOnStreamingReset_AuthErrorNotRetried verifies that a 401 auth
// error is NOT retried by the transient-reset path.
//
// BDD: Given a provider that returns an auth-failure error,
// When the agent loop handles the error,
// Then isTransientStreamError returns false and no retry is attempted,
// And the turn fails immediately after the first call.
func TestRetryOnStreamingReset_AuthErrorNotRetried(t *testing.T) {
	tmpDir := t.TempDir()

	authErr := errors.New("provider auth failed: 401 invalid api key")
	provider := &failingThenSuccessProvider{
		failures: []error{authErr, authErr, authErr},
		successResp: &providers.LLMResponse{
			Content:   "should not be reached",
			ToolCalls: []providers.ToolCall{},
		},
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	ctx := context.Background()
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	_, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "auth-fail-session",
		Channel:         "web",
		ChatID:          "test-chat-auth",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.Error(t, err, "auth failure must cause the turn to fail")
	// Auth errors surface through the ClassifyError → FailoverAuth → IsRetriable
	// path which breaks immediately (not retried inline). The provider must have
	// been called once only.
	assert.Equal(t, 1, provider.callIdx,
		"auth errors must NOT be retried inline: provider must be called exactly once")
}

// TestRetryOnStreamingReset_ScenarioProviderVariant mirrors the above using
// the testutil.ScenarioProvider so the test pattern aligns with the rest of
// the agent test suite.
//
// BDD: Given a ScenarioProvider scripted with a transient error then a success,
// When runAgentLoop is called,
// Then the turn returns without error (retry succeeded).
func TestRetryOnStreamingReset_ScenarioProviderVariant(t *testing.T) {
	tmpDir := t.TempDir()

	streamErr := errors.New("streaming read error: http2: response body closed")
	provider := testutil.NewScenario().
		WithError(streamErr).
		WithText("scenario retry succeeded")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	ctx := context.Background()
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	_, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "scenario-retry-session",
		Channel:         "web",
		ChatID:          "test-chat-scenario",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err,
		"ScenarioProvider variant: turn must succeed after retrying the streaming reset")
}

// TestRetryOnStreamingReset_NothingToTrimStillRetries is a regression test for
// a fragility uncovered by bisecting a real failure: the timeout-recovery
// branch of runTurn's retry loop treated windowTrim's ok=false identically
// whether it meant "nothing eligible to evict" or "trim mechanism genuinely
// failed" — unconditionally `break`-ing the ENTIRE retry attempt in both
// cases. A turn whose assembled context sits over the compaction budget B
// (ADR-066 FR-028) but has little/no compressible history (e.g. the
// very first turn of a session — a single user message and no prior
// conversation) hits exactly the "nothing to evict" case: windowTrim's
// len(window) <= 1 early return. Since the ORIGINAL error that triggered this
// branch was a transient network/streaming reset (not a real context-overflow
// rejection from the provider), abandoning the retry there defeats the whole
// purpose of the timeout-retry path for any turn near the budget edge.
//
// This test forces "over budget" deterministically (rather than relying on
// incidental proximity to a default threshold, which is what made the
// original regression easy to trip and hard to pin down): ContextWindow=100
// and MaxTokens=4096 make the one budget B negative (B = W − max_tokens −
// ceil(0.05·W) − pinnedCoreOverhead), so
// isOverContextBudget is true on the very first (single-message) turn,
// windowTrim has nothing to evict and returns ok=false, and the fix must
// still let the retry proceed.
//
// BDD: Given a fresh turn (no compressible history) whose context sits over
// the compaction budget, and a provider that drops the connection on the
// first call then succeeds on the second,
// When the agent loop handles the transient error,
// Then windowTrim reports "nothing to trim" (ok=false, NothingToTrim=true),
// And the retry loop falls through to backoff+retry instead of abandoning,
// And the turn SUCCEEDS via the second call.
func TestRetryOnStreamingReset_NothingToTrimStillRetries(t *testing.T) {
	tmpDir := t.TempDir()

	streamingResetErr := errors.New("streaming read error: http2: response body closed")
	provider := &failingThenSuccessProvider{
		failures: []error{streamingResetErr},
		successResp: &providers.LLMResponse{
			Content:   "retry succeeded despite nothing to trim",
			ToolCalls: []providers.ToolCall{},
		},
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				// Deliberately tiny so isOverContextBudget is true on a
				// fresh, single-message turn (B = 100 − 4096 − … < 0),
				// independent of any default-config edge.
				ContextWindow:     100,
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	ctx := context.Background()
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent, "default agent must be registered")

	_, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "nothing-to-trim-retry-session",
		Channel:         "web",
		ChatID:          "test-chat-nothing-to-trim",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err,
		"turn must succeed after retrying a transient streaming reset even when "+
			"the over-budget context has nothing eligible to trim; got error: %v", err)
	assert.Equal(t, 2, provider.callIdx,
		"provider must be called exactly twice: once for the reset, once for the success")
}

// recallSpanInjectingProvider wraps failingThenSuccessProvider and, on its
// first call only, activates a recall span AFTER Site-1 message assembly has
// already happened — simulating a recall span that becomes active mid-turn
// (e.g. a recall_conversation tool call in an earlier iteration) rather than
// one already active when the turn began. This isolates windowTrim's
// recall-span-drop-alone branch (case 2, see TestWindowTrim_
// RecallSpanDropAloneReturnsOK in window_trim_test.go for the direct unit
// test) to the timeout-recovery call site (runTurn's compaction-attempt
// branch): the proactive pre-call compaction check runs earlier, against the
// original span-free assembled messages, before this provider is ever
// invoked.
type recallSpanInjectingProvider struct {
	*failingThenSuccessProvider
	al         *AgentLoop
	sessionKey string
	span       *RecallSpan
	injected   bool
}

func (p *recallSpanInjectingProvider) Chat(
	ctx context.Context,
	msgs []providers.Message,
	defs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	if !p.injected {
		p.injected = true
		p.al.setRecallSpan(p.sessionKey, p.span)
	}
	return p.failingThenSuccessProvider.Chat(ctx, msgs, defs, model, opts)
}

// TestRetryOnStreamingReset_RecallSpanDropAloneStillRetries is a regression
// test for windowTrim's case-2 return-value bug found in review: when an
// active recall span alone (not any window Turn) pushes the assembled
// context over the raw ContextWindow threshold, and dropping that span
// brings the window back under the compaction budget, windowTrim used to
// report this exactly like a genuine compaction failure (ok=false,
// NothingToTrim=false) — even though a real, useful eviction (FR-019: drop
// the recall span first) just happened. runTurn's timeout-recovery branch
// treated that identically to "TruncateHistory could not shrink the window"
// and broke out of the retry loop, abandoning a turn that could simply have
// been retried with the (now smaller) assembled messages.
//
// The recall span is activated from inside the mock provider's first call
// (recallSpanInjectingProvider), after the proactive pre-call compaction
// check has already run against the original, span-free messages. This
// isolates the fix to the specific call site under test — the
// timeout-recovery branch's own windowTrim call — rather than the earlier
// proactive one, which would otherwise intercept a recall span that was
// already active before the turn began.
//
// BDD: Given a turn whose assembled context is small at first, but a huge
// recall span becomes active during the (failed) first provider call,
// When the resulting transient streaming-reset error triggers the
// timeout-recovery branch,
// Then windowTrim drops the recall span, reports ok=true (a real eviction,
// not a failure),
// And the retry proceeds with the span dropped,
// And the turn SUCCEEDS via the second call.
func TestRetryOnStreamingReset_RecallSpanDropAloneStillRetries(t *testing.T) {
	tmpDir := t.TempDir()

	streamingResetErr := errors.New("streaming read error: http2: response body closed")
	inner := &failingThenSuccessProvider{
		failures: []error{streamingResetErr},
		successResp: &providers.LLMResponse{
			Content:   "retry succeeded after recall-span drop",
			ToolCalls: []providers.ToolCall{},
		},
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				// The window stays generous so the small initial (span-free)
				// messages never trip the proactive pre-call check. Both
				// checks read the ONE budget B (ADR-066 FR-028) — what makes
				// the timeout-recovery check fire is that it measures the
				// request the retry would assemble, which now includes the
				// ~150,000-token recall span injected during the failed call.
				ContextWindow:     100000,
				MaxTokens:         2000,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	const sessionKey = "recall-span-drop-retry-session"

	// A huge recall span (~150,000 estimated tokens) — comfortably over the
	// 100,000-token raw ContextWindow on its own, so once active it forces
	// windowTrim's FR-019 drop-span-first branch.
	hugeRecallContent := strings.Repeat("r", 375000)
	recallMsgs := []providers.Message{
		{Role: "user", Content: hugeRecallContent},
		{Role: "assistant", Content: "recalled answer"},
	}
	span := newRecallSpan(1, 1, recallMsgs, []int{1})

	provider := &recallSpanInjectingProvider{
		failingThenSuccessProvider: inner,
		sessionKey:                 sessionKey,
		span:                       span,
	}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	provider.al = al
	t.Cleanup(al.Close)

	agentInst := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agentInst, "default agent must be registered")

	// Seed a small prior turn so the persisted window has more than the
	// single in-flight user message — keeps this test clear of case 1
	// ("len(window) <= 1"), covered by the sibling test above.
	priorTurn := []providers.Message{
		{Role: "user", Content: "earlier question"},
		{Role: "assistant", Content: "earlier answer"},
	}
	agentInst.Sessions.SetHistory(sessionKey, priorTurn)
	require.NoError(t, agentInst.Sessions.Save(sessionKey))

	ctx := context.Background()
	_, err := al.runAgentLoop(ctx, agentInst, processOptions{
		SessionKey:      sessionKey,
		Channel:         "web",
		ChatID:          "test-chat-recall-drop",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err,
		"turn must succeed after retrying a transient streaming reset when only "+
			"the recall span (not the window) needed evicting; got error: %v", err)
	assert.Equal(t, 2, inner.callIdx,
		"provider must be called exactly twice: once for the reset, once for the success")
	assert.Nil(t, al.activeRecallSpan(sessionKey),
		"the recall span must have been dropped (FR-019 pressure eviction) during timeout recovery")
}

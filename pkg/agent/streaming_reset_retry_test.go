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
				Workspace:         workspaceDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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
		EnableSummary:   false,
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
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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
		EnableSummary:   false,
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
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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
		EnableSummary:   false,
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
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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
		EnableSummary:   false,
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
				Workspace:         tmpDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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
		EnableSummary:   false,
		SendResponse:    false,
	})
	require.NoError(t, err,
		"ScenarioProvider variant: turn must succeed after retrying the streaming reset")
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// #1882 (chief ruling Q1 A). PUT /api/v1/security/rate-limits answers
// applied with requires_restart=false, and docs/settings.md promises the new
// limit reaches running agents without a restart. The new config is published
// through the loop (the turn reads al.GetConfig on every call), but the
// per-agent window already created in al.rateLimiter keeps the limit it was
// created with (security.RateLimiterRegistry.GetOrCreate returns the existing
// window unchanged). These tests drive the real caller, runTurn's SEC-26 LLM
// call check, through runAgentLoop, and change the limit with
// al.MutateConfig, the publish the REST path ends in.
// Evidence: /workspace/logs/1857/uat/validator-rerun-report.md.

const liveLimitAgentID = "live-limit-agent"

func newLiveLimitLoop(t *testing.T, startLimit, replies int) (*AgentLoop, *AgentInstance) {
	t.Helper()
	provider := testutil.NewScenario()
	for i := 0; i < replies; i++ {
		provider = provider.WithText("scripted reply")
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: liveLimitAgentID, Name: "Live Limit Agent", Type: config.AgentTypeCustom}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			RateLimits: config.OmnipusRateLimitsConfig{MaxAgentLLMCallsPerHour: startLimit},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	inst, ok := al.GetRegistry().GetAgent(liveLimitAgentID)
	require.True(t, ok, "SETUP: custom agent must be registered")
	require.Equal(t, "custom", inst.AgentType, "SETUP: a non-core agent, so the limit applies")
	return al, inst
}

func liveLimitCall(al *AgentLoop, inst *AgentInstance, n int) (string, error) {
	return al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:      "live-limit-session-" + strconv.Itoa(n),
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "message",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
}

// liveLimitRefused applies the same test as TestRunTurn_RateLimit_LLMCallsPerHour:
// the refusal surfaces as an error or as text that names the rate limit.
func liveLimitRefused(res string, err error) bool {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "rate limit") {
		return true
	}
	return strings.Contains(strings.ToLower(res), "rate limit")
}

func setLiveLimit(t *testing.T, al *AgentLoop, limit int) {
	t.Helper()
	require.NoError(t, al.MutateConfig(func(c *config.Config) error {
		c.Sandbox.RateLimits.MaxAgentLLMCallsPerHour = limit
		return nil
	}))
	require.Equal(t, limit, al.GetConfig().Sandbox.RateLimits.MaxAgentLLMCallsPerHour,
		"SETUP: the new limit is published")
}

// Raise: window exists with limit 1, one call used; the limit becomes 3; the
// next call is ALLOWED.
func TestRunTurn_RaisedAgentLimitReachesExistingWindow_1882(t *testing.T) {
	al, inst := newLiveLimitLoop(t, 1, 3)

	res1, err1 := liveLimitCall(al, inst, 1)
	require.NoError(t, err1, "SETUP: call 1 is within the limit of 1")
	require.False(t, liveLimitRefused(res1, nil), "SETUP: call 1 is not refused; got %q", res1)

	setLiveLimit(t, al, 3)

	res2, err2 := liveLimitCall(al, inst, 2)
	assert.False(t, liveLimitRefused(res2, err2),
		"after the limit is raised from 1 to 3 with one call used, the next call must be allowed; err=%v result=%q", err2, res2)
}

// Lower: window exists with limit 5, two calls used; the limit becomes 2; the
// next call is REFUSED.
func TestRunTurn_LoweredAgentLimitReachesExistingWindow_1882(t *testing.T) {
	al, inst := newLiveLimitLoop(t, 5, 3)

	for n := 1; n <= 2; n++ {
		res, err := liveLimitCall(al, inst, n)
		require.NoError(t, err, "SETUP: call %d is within the limit of 5", n)
		require.False(t, liveLimitRefused(res, nil), "SETUP: call %d is not refused; got %q", n, res)
	}

	setLiveLimit(t, al, 2)

	res3, err3 := liveLimitCall(al, inst, 3)
	assert.True(t, liveLimitRefused(res3, err3),
		"after the limit is lowered from 5 to 2 with two calls used, the next call must be refused; err=%v result=%q", err3, res3)
}

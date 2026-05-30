// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// runTurn rate-limit test.
//
// BDD Scenario: "runTurn rate-limit enforcement via ProcessDirect"
//
// Given a per-agent LLM call budget of 1 call/hour (MaxAgentLLMCallsPerHour=1),
// When 2 scripted LLM calls are driven through runTurn via runAgentLoop,
// Then the second call is rejected at the loop level (not at the HTTP layer),
//   an audit row with event="rate_limit" is written,
//   and the user-facing error contains "rate limit".
//
// The audit found "pkg/agent/loop.go:735 emits turn.rate_limit events but no test
// drives the loop over budget". This test closes that gap.
//
// Implementation note: ProcessDirect routes to the default agent ("main") which is
// always AgentType="core" and exempt from rate limits via IsPrivilegedAgent. To
// actually trigger rate limiting we must route through a custom agent
// (AgentType="custom"). We use runAgentLoop directly (package-internal) to pass the
// custom agent instance. This is the same pattern used internally by processMessage.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 3 (Rank-9)

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestRunTurn_RateLimit_LLMCallsPerHour verifies that once the per-agent LLM
// call budget is exhausted, runTurn rejects the next call at the loop level and
// records an audit entry with event="rate_limit".
//
// Setup: MaxAgentLLMCallsPerHour = 1 (the lowest non-zero budget).
//
//	The agent under test is AgentType="custom" — NOT "core" — so that
//	IsPrivilegedAgent() returns false and rate limiting applies.
//
// Scenario:
//
//	Call 1: runAgentLoop → runTurn → allowed → LLM responds "first response".
//	Call 2: runAgentLoop → runTurn → denied before LLM is called.
//
// Assertions:
//
//	(a) Call 1 succeeds (proves the budget is consumed, not a no-op).
//	(b) Call 2 returns an error containing "rate limit".
//	(c) audit.jsonl contains an entry with event="rate_limit".
//	(d) Call 1 and Call 2 produce DIFFERENT outcomes — catches hardcoded responses.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 3 (Rank-9)
func TestRunTurn_RateLimit_LLMCallsPerHour(t *testing.T) {
	// -----------------------------------------------------------------------
	// Arrange: workspace, audit dir, AgentLoop with budget=1.
	// -----------------------------------------------------------------------
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// Scenario: two text replies (both needed to verify differentiation).
	// The second runAgentLoop call is expected to be rejected BEFORE calling
	// the provider, so the provider only needs to handle 1 real call.
	provider := testutil.NewScenario().
		WithText("first response — call 1 succeeded").
		WithText("second response — should never be reached")

	// The custom agent ID must NOT be a core agent ID (as checked by
	// coreagent.IsCoreAgent) and must NOT be DefaultAgentID ("main").
	// "rate-test-agent" is safe: it is not a reserved or core agent name.
	const customAgentID = "rate-test-agent"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// Register a custom (non-privileged) agent so IsPrivilegedAgent returns
			// false and the rate limit check in loop.go applies.
			List: []config.AgentConfig{
				{
					ID:   customAgentID,
					Name: "Rate Test Agent",
					Type: config.AgentTypeCustom,
				},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			// Enable audit logging so recordRateLimitDenial writes to audit.jsonl.
			AuditLog: true,
			RateLimits: config.OmnipusRateLimitsConfig{
				// Budget = 1 call/hour: first call allowed, second call denied.
				MaxAgentLLMCallsPerHour: 1,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	ctx := context.Background()

	// Retrieve the custom agent instance from the registry. If it doesn't
	// exist, the test setup is broken (config registration failed).
	customAgent, ok := al.GetRegistry().GetAgent(customAgentID)
	require.True(t, ok, "SETUP: custom agent %q must be registered in the registry after NewAgentLoop", customAgentID)
	require.Equal(t, "custom", customAgent.AgentType,
		"SETUP: custom agent must have AgentType=custom so rate limiting applies (not exempt via IsPrivilegedAgent)")

	// -----------------------------------------------------------------------
	// Act call 1: must succeed and consume the only allowed slot.
	//
	// We call runAgentLoop directly (package-internal) to route through the
	// custom agent. This is the same code path processMessage takes — it only
	// skips the routing/session resolution overhead.
	// -----------------------------------------------------------------------
	result1, err1 := al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:      "rl-session-1",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "first message",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})
	require.NoError(t, err1, "call 1 must succeed — budget not yet exhausted")
	assert.Contains(t, result1, "first response",
		"call 1 must return the scripted response (differentiation test)")

	// -----------------------------------------------------------------------
	// Act call 2: must be rejected by the rate limiter at the loop level.
	// -----------------------------------------------------------------------
	result2, err2 := al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:      "rl-session-2",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "second message",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})

	// Assert (b): the second call must produce an error or an error-shaped result.
	// The loop may return the rate-limit error two ways:
	//   - as err2 != nil (runAgentLoop propagates the error), or
	//   - as result2 containing "rate limit" (loop injects a rate-limit message).
	rateLimitSurfaced := false
	if err2 != nil && strings.Contains(strings.ToLower(err2.Error()), "rate limit") {
		rateLimitSurfaced = true
	}
	if strings.Contains(strings.ToLower(result2), "rate limit") {
		rateLimitSurfaced = true
	}
	assert.True(t, rateLimitSurfaced,
		"call 2 must surface a rate-limit error (either as err or in response text); "+
			"err2=%v result2=%q", err2, result2)

	// Assert (d): differentiation — call 1 and call 2 produce different outcomes.
	assert.NotEqual(t, result1, result2,
		"rate-limited call must produce a different result from the successful call (not hardcoded)")

	// -----------------------------------------------------------------------
	// Assert (c): audit log contains a rate_limit entry.
	// -----------------------------------------------------------------------
	auditPath := filepath.Join(tmpHome, "system", "audit.jsonl")
	require.FileExists(t, auditPath,
		"audit.jsonl must exist — AuditLog=true and at least one audit event expected")

	entries, err := readAuditEntries(auditPath)
	require.NoError(t, err, "audit.jsonl must be readable and valid JSONL")
	require.NotEmpty(t, entries, "audit.jsonl must contain at least one entry")

	var rateLimitEntries []map[string]any
	for _, e := range entries {
		if e["event"] == audit.EventRateLimit {
			rateLimitEntries = append(rateLimitEntries, e)
		}
	}
	assert.NotEmpty(t, rateLimitEntries,
		"audit.jsonl must contain at least one entry with event=%q; all events: %v",
		audit.EventRateLimit, collectEventNames(entries))
}

// collectEventNames is a test helper that returns all event names from a slice
// of audit entries for diagnostic output.
func collectEventNames(entries []map[string]any) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if v, ok := e["event"].(string); ok {
			names = append(names, v)
		}
	}
	return names
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// cli_failure_kinds_denial_code_test.go — #1882 follow-up, gate finding
// F-PTA-3 (test-author half): the tool-call SEC-26 denial code had no pin —
// every RateLimitPayload fixture in the tree used resource "llm_call", so a
// regression mapping ALL SEC-26 denials to agent_request_limit would pass
// green while misattributing tool-call denials (the wrong remedy: the
// agent_request_limit copy points at the agent's own hourly Settings limit,
// not at the provider the tool-call refusal has nothing to do with).
//
// Oracles, never the implementation:
//   - docs/internal/specs/notifications-spec.md §13.8.5 B3 / OQ-1857-5
//     (founder E2, 2026-11-18): the agent's own hourly model-request limit is
//     its own code agent_request_limit (attribution config), never the
//     provider-overloaded rate_limited.
//   - contracts/components/schemas/LLMError.yaml (x-user-messages keys):
//     agent_request_limit (attribution config) and rate_limited
//     (attribution provider) are both catalogue codes; the mapped constants
//     must equal exactly those strings.
//   - The tool-call case uses the resource name the production tool-call
//     gate actually sends: pkg/agent/loop_run_turn_tools.go, the tool gate's
//     rateLimiter.GetOrCreate call (key "agent:<id>:tool_call") building its
//     RateLimitPayload with Resource "tool_call" and recording the denial
//     through loop.go::recordRateLimitDenial — the sole caller of the mapper.

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// sec26DenialPayload mirrors the shape the production gates record: the LLM
// gate (pkg/agent/loop_run_turn.go::beginIteration) and the tool gate
// (pkg/agent/loop_run_turn_tools.go) both fill Scope, Resource, PolicyRule,
// RetryAfterSeconds and AgentID before calling recordRateLimitDenial.
func sec26DenialPayload(resource, policyRule string, retryAfterSeconds float64) RateLimitPayload {
	return RateLimitPayload{
		Scope:             "agent",
		Resource:          resource,
		PolicyRule:        policyRule,
		RetryAfterSeconds: retryAfterSeconds,
		AgentID:           "agent-denial-code-test",
	}
}

func TestRateLimitDenialCode(t *testing.T) {
	t.Run("llm_call_denial_maps_to_agent_request_limit", func(t *testing.T) {
		payload := sec26DenialPayload("llm_call",
			`rate_limit: llm_call limit 25/1h0m0s exceeded for agent "agent-denial-code-test"`, 600)
		got := rateLimitDenialCode(payload)
		assert.Equal(t, LLMErrorCode("agent_request_limit"), got,
			"#1857 §13.8.5 B3 (founder E2): an llm_call SEC-26 denial must be recorded as agent_request_limit "+
				"(attribution config, LLMError.yaml) — never the provider-overloaded rate_limited; saw %q", got)
	})

	t.Run("tool_call_denial_keeps_rate_limited", func(t *testing.T) {
		// Resource "tool_call" is the name the production tool-call gate
		// sends (pkg/agent/loop_run_turn_tools.go, the tool gate's
		// GetOrCreate call). The tool-call limit has no §13.8 row yet and
		// keeps the provider rate_limited code — mapping it to
		// agent_request_limit would point the operator at the hourly
		// Settings limit for a per-minute tool-call refusal.
		payload := sec26DenialPayload("tool_call",
			`rate_limit: tool_call limit 60/1m0s exceeded for agent "agent-denial-code-test"`, 30)
		got := rateLimitDenialCode(payload)
		assert.Equal(t, LLMErrorCode("rate_limited"), got,
			"F-PTA-3: a tool_call SEC-26 denial must keep rate_limited (attribution provider, LLMError.yaml) — "+
				"mapping every SEC-26 denial to agent_request_limit misattributes the tool-call limit with the wrong remedy; saw %q", got)
	})
}

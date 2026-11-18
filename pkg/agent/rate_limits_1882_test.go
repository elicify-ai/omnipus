// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED repro for GitHub issue #1882, requirement R2 — the agent-level half.
//
// Seam (stated per the brief): the REAL turn loop. The config the loop runs
// on is loaded from a config.json that carries the per-agent hourly limit
// EXACTLY where the Settings screen saves it — security.rate_limits
// (src/components/settings/SecuritySection.tsx:389-393, sent through
// PUT /api/v1/config via src/lib/api/config.ts:303-314) — and nowhere else.
// The limit's only route into the loop is therefore config.LoadConfig; the
// loop's enforcement point is beginIteration
// (pkg/agent/loop_run_turn.go), which applies cfg.Sandbox.RateLimits.
// Two turns are then driven through the real runAgentLoop → runTurn path
// with a scripted provider (the LLM is the only mock, and it sits at the
// process edge).
//
// Characterization of the DEFECT — not verification of correct behaviour:
// issue #1882's Expected is "a limit set in Settings is the limit that is
// enforced". With the limit saved the way Settings saves it, this test shows
// the turn PAST that limit is NOT stopped — the provider is actually called
// a second time within the hour. It pins today's broken behaviour and MUST
// FAIL once #1882 is fixed (the second turn must then be denied with a
// "rate limit" error before any provider call, exactly as
// TestRunTurn_RateLimit_LLMCallsPerHour shows for a limit set at the enforced
// location sandbox.rate_limits). Invert or retire this test in the fix's
// test-side reconciliation; do not delete it silently.
//
// The positive counterpart — the enforced location DOES stop the second turn —
// is the existing TestRunTurn_RateLimit_LLMCallsPerHour; the ONLY difference
// between the two harnesses is where the limit was saved. That delta is the
// issue.

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Test1882_TurnPastSettingsSavedHourlyLimitIsNotStopped drives a real turn
// loop whose config carries the per-agent hourly limit only at the
// Settings-saved location, and shows the second turn of the hour runs anyway.
func Test1882_TurnPastSettingsSavedHourlyLimitIsNotStopped(t *testing.T) {
	// --- Arrange: config.json with the limit ONLY where Settings saves it ---
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfgJSON := fmt.Sprintf(
		`{"version":%d,`+
			`"agents":{"defaults":{"workspace":%q,"default_model":{"model":"scripted-model"},"max_tokens":4096,"max_tool_iterations":10}},`+
			`"security":{"rate_limits":{"max_agent_llm_calls_per_hour":1}},`+
			`"providers":[]}`,
		config.CurrentVersion, workspaceDir)
	cfgPath := filepath.Join(tmpHome, "config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600),
		"SETUP: writing the Settings-shaped config.json must succeed")

	cfg, err := config.LoadConfig(cfgPath)
	require.NoError(t, err, "SETUP: the Settings-shaped config.json must load")

	// SETUP proof of the mechanism the issue describes: the Settings-saved key
	// decoded onto NO enforced field — sandbox.rate_limits is still unlimited.
	// If this ever loads as 1, the loader already routes the Settings key and
	// this defect-pinning test's premise is gone — reconcile it in the fix.
	require.Equal(t, 0, cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour,
		"SETUP/DEFECT PIN #1882: a limit saved under security.rate_limits (the Settings location) must currently load onto NO enforced "+
			"field (cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour stays 0/unlimited); seen %d — the premise of this defect pin has changed, "+
			"reconcile this test as part of the #1882 fix",
		cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour)

	// The live roster is never loaded from config.json (ADR-054): production
	// populates it from the entity store. This test adds the custom agent the
	// way a real boot's roster bridge would carry it.
	const customAgentID = "rate-1882-agent"
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:   customAgentID,
		Name: "Rate 1882 Agent",
		Type: config.AgentTypeCustom,
	})

	// Two scripted replies: the second one may only be reached if the turn
	// past the saved limit really did call the provider.
	provider := testutil.NewScenario().
		WithText("first response — call 1 succeeded").
		WithText("second response — call 2 ran past the Settings-saved limit")

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	customAgent, ok := al.GetRegistry().GetAgent(customAgentID)
	require.True(t, ok, "SETUP: custom agent %q must be registered so IsPrivilegedAgent does not exempt it from rate limiting", customAgentID)
	require.Equal(t, "custom", customAgent.AgentType,
		"SETUP: the agent under test must be AgentType=custom (non-privileged) so the limiter branch in beginIteration would apply")

	ctx := context.Background()

	// --- Act, call 1: within the saved hourly budget (the saved budget is 1) —
	result1, err1 := al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:      "rl1882-session-1",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "first message",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err1, "SETUP: call 1 must succeed — the hour's single saved slot is not yet spent")
	assert.Contains(t, result1, "first response",
		"SETUP: call 1 must return the scripted reply, proving the turn loop really runs")

	// --- Act, call 2: the turn PAST the Settings-saved hourly limit -----------
	// Issue #1882 Expected: a limit set in Settings IS the enforced limit, so
	// this turn must be denied before any provider call.
	result2, err2 := al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:      "rl1882-session-2",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "second message",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})

	// DEFECT PIN (assertions expect today's broken behaviour and MUST FAIL
	// after the #1882 fix — see the file header):
	assert.NoError(t, err2,
		"#1882 DEFECT PIN: with the hourly limit saved the way Settings saves it (1/hour), turn 2 errored instead of running (%v). "+
			"If the fix has landed, turn 2 past the saved limit is denied with 'rate limit' and this pin must be inverted in the fix's "+
			"test-side reconciliation; result2=%q", err2, result2)
	assert.Contains(t, result2, "second response",
		"#1882 DEFECT PIN: the provider was actually called for turn 2 — the turn past the Settings-saved hourly limit of 1 was NOT stopped. "+
			"After the #1882 fix the second turn must be denied with a rate-limit error BEFORE any provider call (see "+
			"TestRunTurn_RateLimit_LLMCallsPerHour for the enforced-location behaviour) and this assertion must be inverted; result2=%q",
		result2)
}

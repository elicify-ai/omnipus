// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// reasoning_effort_llmopts_test.go is the RED wiring test for C5's request
// shaping: the outgoing LLM options map must NEVER carry the deleted
// "thinking_level" key, and must carry "reasoning_effort" exactly when a value
// resolves — absence, never an empty-string placeholder (spec
// docs/internal/specs/thinking-reasoning-spec.md Section 1 C5, D9; Section 2.2
// row "pkg/agent/loop_run_turn.go thinking-level gate" — deleted and replaced
// by per-adapter effort plumbing keyed on reasoning_effort; Section 16 tests
// 5/32; dataset rows 1, 9).
//
// The seam is the real turn: a real AgentLoop runs one inbound message through
// the same capturing-provider harness toolcall_progress_wiring_test.go
// established (progressCapturingStreamProvider records the options map every
// Chat/ChatStream call receives — the provider IS the process edge; the unit
// under test, loop_run_turn.go::prepareLLMRequest's option shaping, stays
// real).
package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// newReasoningEffortWiringLoop builds a real, single-default-agent AgentLoop
// around the given capturing provider, mirroring
// newProgressWiringTestLoop (toolcall_progress_wiring_test.go) with the caller
// mutating the config first — that is how each C5 storage surface below plants
// its effort value. The list agent carries no own model, so the primary is
// INHERITED from agents.defaults.default_model (pkg/agent/
// instance.go::resolveAgentModel) — the exact shape of C5 step (4).
func newReasoningEffortWiringLoop(
	t *testing.T,
	provider providers.LLMProvider,
	mutateCfg func(*config.Config),
) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	tmpDir := t.TempDir()
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
	if mutateCfg != nil {
		mutateCfg(cfg)
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	return al, msgBus
}

// runOneTurn drives one inbound message through the loop and returns the most
// recent options map the provider received.
func runOneTurn(t *testing.T, al *AgentLoop, msgBus *bus.MessageBus, provider *progressCapturingStreamProvider) map[string]any {
	t.Helper()
	_, _, err := al.processMessage(t.Context(), bus.InboundMessage{
		Channel: "webchat",
		Sender:  bus.SenderInfo{CanonicalID: "user:1", DisplayName: "Tester"},
		ChatID:  "direct",
		Content: "hi",
	})
	require.NoError(t, err)

	opts := provider.lastOptions()
	require.NotNil(t, opts, "test setup invariant: the turn must have reached the provider — "+
		"an options map of nil proves nothing about option shaping")
	return opts
}

// forEachOptions invokes fn for every options map recorded from any
// Chat/ChatStream call, under the provider's lock. Same-package method on the
// shared harness type — defined here (not in
// toolcall_progress_wiring_test.go) so this pack adds no edits to files it
// does not own.
func (p *progressCapturingStreamProvider) forEachOptions(fn func(map[string]any)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, opts := range p.gotOptions {
		fn(opts)
	}
}

// assertNoThinkingLevelKey fails on ANY recorded call carrying the deleted
// "thinking_level" key — the strongest form of the never-again invariant: it
// is checked across every provider call the turn made, not just the last.
func assertNoThinkingLevelKey(t *testing.T, provider *progressCapturingStreamProvider) {
	t.Helper()
	provider.forEachOptions(func(opts map[string]any) {
		assert.NotContains(t, opts, "thinking_level",
			"the thinking_level mechanism is deleted (D1) — no provider call may ever receive the key again")
	})
}

// TestLLMOptions_ReasoningEffort_Wiring proves C5 step (4) end to end: an
// agent whose primary is inherited from the instance default model sends the
// default model's reasoning_effort to the provider.
//
// Traces to: C5 resolution order step (4); BDD "An agent riding the instance
// default model uses the default model's effort"; dataset row 9; test 32.
func TestLLMOptions_ReasoningEffort_Wiring(t *testing.T) {
	provider := &progressCapturingStreamProvider{content: "ok"}
	al, msgBus := newReasoningEffortWiringLoop(t, provider, func(cfg *config.Config) {
		cfg.Agents.Defaults.DefaultModel.ReasoningEffort = "medium" // needs config.DefaultModel.ReasoningEffort (WP-G)
	})

	opts := runOneTurn(t, al, msgBus, provider)

	assertNoThinkingLevelKey(t, provider)
	assert.Equal(t, "medium", opts["reasoning_effort"],
		"C5 step (4): an inherited primary must send DefaultModel.reasoning_effort to the provider")
}

// TestLLMOptions_StoredModelRowEffort_FlowsToProvider proves the STORED
// surface flows: a configured model row's reasoning_effort (the replacement
// for the deleted ModelConfig.ThinkingLevel) reaches the provider request.
//
// Traces to: C5 storage ("Effort is stored as a plain string per model
// setting", Section 8.1); ModelConfig.ThinkingLevel's 2.2 deletion row
// (replaced, not dropped).
func TestLLMOptions_StoredModelRowEffort_FlowsToProvider(t *testing.T) {
	provider := &progressCapturingStreamProvider{content: "ok"}
	al, msgBus := newReasoningEffortWiringLoop(t, provider, func(cfg *config.Config) {
		cfg.Providers = []*config.ModelConfig{{
			Provider:        "zai",
			Model:           "test-model",
			ReasoningEffort: "high", // needs config.ModelConfig.ReasoningEffort (WP-G)
		}}
	})

	opts := runOneTurn(t, al, msgBus, provider)

	assertNoThinkingLevelKey(t, provider)
	assert.Equal(t, "high", opts["reasoning_effort"],
		"the stored model-row effort must flow to the provider request (it replaced ThinkingLevel, it must not be dropped like CF5)")
}

// TestLLMOptions_ThinkingLevel_NeverSent_NoEffortResolves proves the nothing
// case: with no effort configured anywhere, the provider request carries NO
// "reasoning_effort" key at all — absence, never an empty-string placeholder
// (D9) — and never the deleted "thinking_level" key.
//
// Traces to: C5 resolution order (5-else); D9; dataset rows 1/5; test 5
// (unset → request carries nothing).
func TestLLMOptions_ThinkingLevel_NeverSent_NoEffortResolves(t *testing.T) {
	provider := &progressCapturingStreamProvider{content: "ok"}
	al, msgBus := newReasoningEffortWiringLoop(t, provider, nil)

	opts := runOneTurn(t, al, msgBus, provider)

	assertNoThinkingLevelKey(t, provider)
	reasoningEffort, present := opts["reasoning_effort"]
	assert.False(t, present,
		"D9: with no effort resolving, the options map must NOT carry the key at all — absence is the send-nothing signal")
	assert.Empty(t, reasoningEffort,
		"and the key's value, had it existed, must never be an empty-string placeholder")
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

// legacy_summarizer_decommission_test.go — the legacy LLM summariser is gone.
//
// The sliding window (windowTrim, ADR-028) is the authoritative history. The
// summariser that predated it was never removed: it fired after every turn on
// the normal inbound-message path, called the model with a 3-attempt retry
// budget, and re-injected its output into the system prompt of every later
// turn as a "CONTEXT_SUMMARY" block. It has been deleted in full.
//
// Two tests keep it gone, and they are deliberately independent:
//
//   - TestDecommission_NoLegacySummarizerSymbols is a grep-clean assertion over
//     the raw production sources. It catches the symbols coming back.
//   - TestDecommission_LongConversationMakesNoExtraModelCalls drives the real
//     inbound-message path (processMessage — the path that used to set
//     EnableSummary: true) for enough turns to cross the old message threshold
//     and asserts the provider is called exactly once per turn. It catches the
//     BEHAVIOUR coming back under any name.
//
// The behavioural one is the one that matters: the names are free to change,
// the extra model calls cost real money.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ---------- TestDecommission_NoLegacySummarizerSymbols ----------

// TestDecommission_NoLegacySummarizerSymbols is a grep-clean assertion that
// none of the legacy summariser's definitions, wiring, or rendered output
// exist in the production sources this decommission touched.
//
// Modelled on TestDecommission_NoForceCompressionSymbols (window_trim_test.go),
// which guards the same class of regression for the retired forceCompression
// path — a plain substring check over raw file text, so it survives any
// refactor of the surrounding code.
//
// SummarizeTokenPercent used to be the one survivor this test protected (it
// scaled the timeout-recovery windowTrim trigger). ADR-066 D6 (T066-03)
// replaced that scaling with the one budget B and deleted the knob, so the
// closing assertion now forbids it too.
func TestDecommission_NoLegacySummarizerSymbols(t *testing.T) {
	filesOwned := []string{
		"loop.go",
		"context.go",
		"turn.go",
		"events.go",
		"subturn.go",
		"steering.go",
		"instance.go",
	}

	// Function definitions that must not exist anywhere in production code.
	forbiddenDefinitions := []string{
		"func (al *AgentLoop) maybeSummarize(",
		"func (al *AgentLoop) summarizeSession(",
		"func (al *AgentLoop) summarizeBatch(",
		"func (al *AgentLoop) retryLLMCall(",
		"func (al *AgentLoop) findNearestUserMessage(",
	}

	// Identifiers and literals that must not exist anywhere in production
	// code: the per-turn trigger flag, the event surface, the turn-state
	// restore plumbing, the persisted-summary accessors, the config knob, and
	// the rendered system-prompt block itself.
	forbiddenSymbols := []string{
		"EnableSummary",
		"EventKindSessionSummarize",
		"SessionSummarizePayload",
		"restorePointSummary",
		"GetSummary(",
		"SetSummary(",
		"SummarizeMessageThreshold",
		"Legacy summary: rendered inertly",
		"CONTEXT_SUMMARY",
	}

	for _, filename := range filesOwned {
		content := readOwnedFileForTest(t, filename)
		for _, pattern := range forbiddenDefinitions {
			assert.NotContains(t, content, pattern,
				"file %s must not define the legacy summariser routine %q", filename, pattern)
		}
		for _, pattern := range forbiddenSymbols {
			assert.NotContains(t, content, pattern,
				"file %s must not reference the decommissioned summariser symbol %q", filename, pattern)
		}
	}

	// SummarizeTokenPercent — the knob this test once pinned in place as the
	// timeout-recovery trigger's scale — was deleted by ADR-066 D6 (FR-004,
	// T066-03): that trigger now reads the one budget B like every other
	// site, so the percentage has no reader left. Its absence is asserted by
	// TestMidTurnBudget_SameBudgetAsWindowTrim (context_budget_test.go); the
	// timeout-recovery gate itself is still exercised by
	// TestRetryOnStreamingReset_* (streaming_reset_retry_test.go).
	loopSrc := readOwnedFileForTest(t, "loop.go")
	assert.NotContains(t, loopSrc, "SummarizeTokenPercent",
		"loop.go must not reference the deleted SummarizeTokenPercent (FR-004)")
}

// ---------- TestDecommission_LongConversationMakesNoExtraModelCalls ----------

// turnCallCountingProvider records how many times the agent loop asked the
// model for anything. Its Chat is otherwise identical to mockProvider's.
//
// Distinct from context_paging_integration_test.go's countingProvider, which
// counts with a bare int: this one is read from the test goroutine while the
// turn machinery may still hold background goroutines, so the counter has to
// be atomic or `go test -race` reports the read itself.
type turnCallCountingProvider struct {
	calls atomic.Int64
}

func (p *turnCallCountingProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{
		Content:   "Mock response",
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *turnCallCountingProvider) GetDefaultModel() string { return "turn-call-counting-model" }

// TestDecommission_LongConversationMakesNoExtraModelCalls proves the
// summariser's BEHAVIOUR is gone, not just its names.
//
// BDD: Given a conversation long enough to cross the old
//
//	SummarizeMessageThreshold (20 messages, the shipped default),
//	When each turn is driven through the real inbound-message path
//	(processMessage — the caller that used to set EnableSummary: true),
//	Then the provider is called exactly once per turn and never again.
//
// Before the decommission this failed: after the 11th turn the live window
// held 22 messages, maybeSummarize tripped its threshold and spawned
// summarizeSession in a goroutine, which issued two summarizeBatch calls plus
// an LLM merge call — three extra model calls the user never asked for, on a
// conversation that had done nothing unusual.
//
// The post-turn assertion polls rather than checking once: the legacy
// summariser ran asynchronously, so a single immediate read could miss it.
func TestDecommission_LongConversationMakesNoExtraModelCalls(t *testing.T) {
	// Workspace two levels deep so filepath.Dir yields a stable temp parent.
	tmpRoot := t.TempDir()
	workspace := filepath.Join(tmpRoot, "home", "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o700))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspace,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				// Generous window so the proactive windowTrim budget check
				// never fires. windowTrim makes no LLM calls either way, but
				// keeping it out of the picture makes the call count mean
				// exactly one thing: one call per turn, nothing else.
				ContextWindow: 400000,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspace}},
		},
	}

	provider := &turnCallCountingProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(func() { al.Close() })

	// 12 turns × (1 user + 1 assistant) = 24 messages, comfortably past the
	// old 20-message threshold, with the crossing happening at turn 11 so
	// there are still turns left afterwards for a regression to show up in.
	const turns = 12
	const sessionKey = "agent:mia:chat:decommission-no-extra-calls"

	for i := 0; i < turns; i++ {
		_, _, err := al.processMessage(context.Background(), bus.InboundMessage{
			Channel:    "discord",
			InstanceID: "discord.test",
			ChatID:     "chat-decommission",
			SessionKey: sessionKey,
			Content:    fmt.Sprintf("turn %d: tell me something about context paging", i),
			Sender: bus.SenderInfo{
				CanonicalID: "discord:user-1",
				DisplayName: "Alice",
			},
		})
		require.NoError(t, err, "turn %d must succeed", i)
	}

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	// Precondition: the conversation really did grow past the old threshold.
	// Without this the test could pass for the wrong reason (e.g. the session
	// key not resolving, so every turn started from an empty history).
	//
	// Measured with ReadArchive, not GetHistory, and that is load-bearing:
	// the legacy summariser TRUNCATED the live window as its final act, so a
	// GetHistory-based precondition reads ~6 messages on the very code this
	// test exists to catch — it would fail on the precondition instead of on
	// the call count, proving nothing about cost. ReadArchive ignores Skip and
	// counts every message ever appended, so it reports the same long
	// conversation either way and lets the call-count assertion below be the
	// thing that actually fires.
	archive, archiveErr := agent.Sessions.ReadArchive(context.Background(), sessionKey)
	require.NoError(t, archiveErr, "reading the session archive must succeed")
	require.Greater(t, len(archive), 20,
		"precondition: the conversation must cross the old 20-message "+
			"SummarizeMessageThreshold, otherwise this test proves nothing")

	// The legacy summariser fired asynchronously. Give any resurrected
	// background work a real window to make its call before asserting.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		require.LessOrEqual(t, provider.calls.Load(), int64(turns),
			"a background model call appeared after the turns finished — "+
				"the legacy summariser (or something like it) is back")
		time.Sleep(50 * time.Millisecond)
	}

	assert.Equal(t, int64(turns), provider.calls.Load(),
		"a %d-turn conversation must cost exactly %d model calls: one per turn, "+
			"and no summarization pass", turns, turns)
}

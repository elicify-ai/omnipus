// Omnipus — ToolSearch end-to-end message-history integrity test (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// TestLoadTool_EndToEndMessageHistoryIntegrity drives a two-turn scenario that
// exercises the regression that commit 769c719e introduced: an orphaned tool
// result in the message history after a ToolSearch call. The test:
//
//  1. Uses a ScenarioProvider scripted for three LLM turns:
//     Turn 1: the model calls ToolSearch (a lazy tool it wants to activate).
//     Turn 2: after ToolSearch succeeds, the model calls the now-loaded tool.
//     Turn 3: the tool call resolves; the model emits a plain-text conclusion.
//
//  2. After the full conversation, the messages captured by the ScenarioProvider
//     are inspected for history validity: no role="tool" message may be an orphan
//     (ToolCallID must match a preceding assistant ToolCalls entry).
//
//  3. Normalization-is-noop invariant: normalizeMessagesForProvider must return
//     the same number of messages — meaning the history the agent loop produced
//     was already valid, not silently fixed after the fact.
//
// The test is unit-level (ProcessDirectWithChannel, no real LLM) and lives in
// pkg/agent (internal package) so it can call normalizeMessagesForProvider and
// reuse existing fakes and helpers from the package test suite.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// perCallRecorder wraps a providers.LLMProvider and records the messages
// snapshot passed to each Chat invocation in order. This allows tests to
// assert history validity at EVERY call, not just the final one, so a transient
// orphan at call N that gets repaired before call N+1 cannot escape detection.
type perCallRecorder struct {
	mu       sync.Mutex
	inner    providers.LLMProvider
	callMsgs [][]providers.Message // index 0 = first Chat call, etc.
}

func newPerCallRecorder(inner providers.LLMProvider) *perCallRecorder {
	return &perCallRecorder{inner: inner}
}

// Chat records the messages for this invocation, then delegates to the inner provider.
// Implements providers.LLMProvider.
func (r *perCallRecorder) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	snapshot := make([]providers.Message, len(messages))
	copy(snapshot, messages)
	r.mu.Lock()
	r.callMsgs = append(r.callMsgs, snapshot)
	r.mu.Unlock()
	return r.inner.Chat(ctx, messages, tools, model, options)
}

// GetDefaultModel delegates to the inner provider.
func (r *perCallRecorder) GetDefaultModel() string {
	return r.inner.GetDefaultModel()
}

// MessagesForCall returns the messages snapshot for call number n (0-indexed).
// Returns nil if n is out of range.
func (r *perCallRecorder) MessagesForCall(n int) []providers.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n < 0 || n >= len(r.callMsgs) {
		return nil
	}
	cp := make([]providers.Message, len(r.callMsgs[n]))
	copy(cp, r.callMsgs[n])
	return cp
}

// CallCount returns the number of Chat calls recorded so far.
func (r *perCallRecorder) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.callMsgs)
}

// assertNoOrphanToolResults verifies that every role="tool" message in msgs has
// a ToolCallID that was declared by a preceding assistant's ToolCalls slice.
// This is the core Rule D invariant that normalizeMessagesForProvider enforces —
// the history passed to the LLM must already satisfy it for the normalizer to be
// a no-op (not a silent correctness backstop).
func assertNoOrphanToolResults(t *testing.T, label string, msgs []providers.Message) {
	t.Helper()
	declared := map[string]bool{}
	var lastAssistantHasToolCalls bool
	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					declared[tc.ID] = true
				}
			}
			lastAssistantHasToolCalls = len(m.ToolCalls) > 0
		case "tool":
			if m.ToolCallID == "" {
				// Empty ToolCallID: valid only when the immediately preceding
				// message is an assistant with ToolCalls (single-call omitted-id).
				assert.True(t, lastAssistantHasToolCalls,
					"%s: role=tool with empty ToolCallID not preceded by assistant-with-toolcalls (orphan)", label)
			} else {
				assert.True(t, declared[m.ToolCallID],
					"%s: role=tool ToolCallID=%q not declared by any preceding assistant (orphan)", label, m.ToolCallID)
			}
			lastAssistantHasToolCalls = false
		default:
			lastAssistantHasToolCalls = false
		}
	}
}

// assertNormalizationIsNoop verifies that normalizeMessagesForProvider returns
// the same message slice length for msgs. Any change means the history was
// already invalid when it reached the LLM — the normalizer is a last-resort
// guard, not a substitute for the loop producing valid histories.
func assertNormalizationIsNoop(t *testing.T, label string, msgs []providers.Message) {
	t.Helper()
	normalized := normalizeMessagesForProvider(msgs)
	assert.Equal(t, len(msgs), len(normalized),
		"%s: normalizeMessagesForProvider changed history length (%d→%d); history was invalid before normalization",
		label, len(msgs), len(normalized))
}

// newE2ECfg builds a minimal config with Compressed=true and the four core
// agents seeded. It mirrors newCompressedCfg but accepts an explicit workspace
// dir so the caller can control where sessions are written.
func newE2ECfg(t *testing.T, workspaceDir string) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// Deliberately NO List here — see newCompressedCfg's identical
			// comment in tool_manifest_test.go: coreagent.SeedConfig only
			// seeds the full core roster (with real tool policies) when
			// cfg.Agents.List starts EMPTY.
		},
	}
	cfg.Tools.Manifest.Compressed = true
	coreagent.SeedConfig(cfg)
	return cfg
}

// TestLoadTool_EndToEndMessageHistoryIntegrity drives the two-turn ToolSearch
// scenario and asserts that the message history passed to ALL LLM calls is
// normalization-valid (no orphan tool results, no empty assistants).
//
// Scripted scenario (three Chat steps via ScenarioProvider):
//
//	Step 1 → model calls ToolSearch(names:["find_skills"])
//	Step 2 → model calls find_skills(query:"test")  [now loaded]
//	Step 3 → model emits a plain-text conclusion
//
// The session is driven via ProcessDirectWithChannel, which routes through the
// full runTurn path (same as production). LastMessages() gives us the exact
// slice each Chat call received; the final snapshot contains the full
// accumulated history and is the most informative to assert against.
func TestLoadTool_EndToEndMessageHistoryIntegrity(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// find_skills is ManifestLazy and policy-allowed for Jim. It is a good
	// candidate: it is a real registered tool, not a synthetic placeholder,
	// so the agent loop goes through the full ToolSearch → register → execute path.
	const lazyTool = "find_skills"

	// ScenarioProvider: three steps as described above.
	// WithToolCall generates the ToolCall ID as "<name>-0".
	scenario := testutil.NewScenario().
		WithToolCall("ToolSearch", `{"names":["find_skills"]}`).
		WithToolCall(lazyTool, `{"query":"test"}`).
		WithText("Done. Here are the skills I found.")

	// Wrap the scenario provider so we capture per-call message snapshots.
	// This lets us assert history validity at call #2 independently of the
	// final call — a transient orphan at call #2 that gets repaired before
	// call #3 would otherwise escape the assertion.
	recorder := newPerCallRecorder(scenario)

	cfg := newE2ECfg(t, workspaceDir)
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, recorder)
	defer al.Close()

	ctx := context.Background()
	const sessionKey = "e2e-load-tool-integrity"

	// Drive a full conversation via the production entry point.
	reply, err := al.ProcessDirectWithChannel(ctx,
		"please load find_skills and use it to find some skills",
		sessionKey, "cli", "test")
	require.NoError(t, err,
		"ProcessDirectWithChannel must not error; reply=%q", reply)

	// ── Sanity checks — verify all scripted steps were consumed ─────────────

	// All three scripted steps must have been consumed.
	assert.Equal(t, 0, scenario.Remaining(),
		"all 3 scripted steps must be consumed; remaining=%d", scenario.Remaining())

	// Exactly 3 LLM calls: one per scripted step.
	assert.Equal(t, 3, recorder.CallCount(),
		"expected exactly 3 LLM calls (ToolSearch step + find_skills step + conclusion step)")

	// ── Assert history validity at the SECOND LLM call (call index 1) ───────
	// Call #2 receives the history after ToolSearch executed — this is the
	// critical moment where an orphan tool-result could appear if the loop
	// inserts a tool-result without a matching assistant ToolCall entry.
	// Asserting here means a transient orphan cannot hide behind a subsequent
	// normalization pass before call #3.
	call2Messages := recorder.MessagesForCall(1)
	require.NotNil(t, call2Messages,
		"recorder must have captured messages for the 2nd LLM call (index 1)")

	assertNoOrphanToolResults(t, "2nd LLM call (after ToolSearch)", call2Messages)
	assertNormalizationIsNoop(t, "2nd LLM call (after ToolSearch)", call2Messages)

	// ── Inspect the message history passed to the FINAL (3rd) LLM call ──────
	// The final call's history contains the full accumulated messages including
	// both tool-call/tool-result pairs — the most complete view of correctness.
	finalMessages := recorder.MessagesForCall(2)
	require.NotNil(t, finalMessages,
		"recorder must have captured messages for the final (3rd) LLM call (index 2)")

	// Structural invariant: no role=tool message may be an orphan.
	assertNoOrphanToolResults(t, "final LLM call", finalMessages)

	// Normalization-is-noop invariant: the history was valid before normalization.
	assertNormalizationIsNoop(t, "final LLM call", finalMessages)

	// ── Tool-presence sanity checks ──────────────────────────────────────────

	// The ToolSearch call must appear in the final history.
	foundLoadTool := false
	for _, m := range finalMessages {
		for _, tc := range m.ToolCalls {
			if tc.Function != nil && tc.Function.Name == "ToolSearch" {
				foundLoadTool = true
			}
		}
	}
	assert.True(t, foundLoadTool,
		"ToolSearch call must appear in the message history sent to the final LLM call")

	// The find_skills call must also appear (proving it was loaded and invoked).
	foundFindSkills := false
	for _, m := range finalMessages {
		for _, tc := range m.ToolCalls {
			if tc.Function != nil && tc.Function.Name == lazyTool {
				foundFindSkills = true
			}
		}
	}
	assert.True(t, foundFindSkills,
		"find_skills call must appear in the message history sent to the final LLM call")
}

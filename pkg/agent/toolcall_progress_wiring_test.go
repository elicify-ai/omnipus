// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// G1 regression coverage (review finding, criticality 9): PRODUCER side.
//
// protocoltypes.ToolCallProgress support was added to the anthropic and
// openai_compat providers, but nothing in production ever supplied the
// callback those providers read from — verified by grep: only the two
// provider call sites and test files referenced it, never a real caller.
// The consequence: a model
// streaming a multi-kilobyte tool-call argument (a large SVG, a long file
// write) produced ZERO observable output on any surface, indistinguishable
// from a hung generation. An orchestrator polled a delegated worker 75 times
// over 46 seconds, saw nothing, and killed it mid-write — repeatedly.
//
// These tests prove loop.go actually passes the callback to ChatStream —
// the ONLY thing that would ever notice this wiring being silently dropped
// again. The callback now travels as an explicit ChatStream ARGUMENT rather
// than a key in the llmOpts map (ADR-059 W1), which is what closes the
// original hazard by construction: a BeforeLLM hook taking HookActionModify
// replaces llmOpts wholesale and previously could silently drop the
// callback. TestRunTurn_ToolCallProgressCallback_SurvivesBeforeLLMOptionsReplacement
// is kept, unchanged in intent, to prove that hazard stays closed — it now
// asserts the callback arrives even though the hook wiped the options map.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// progressCapturingStreamProvider is a minimal providers.LLMProvider +
// providers.StreamingProvider stub that records every options map it is
// called with, so a test can inspect exactly what loop.go handed the
// provider — the boundary review finding G1 identifies as unwired.
type progressCapturingStreamProvider struct {
	mu          sync.Mutex
	content     string
	gotOptions  []map[string]any
	gotProgress []protocoltypes.OnToolCallProgress
}

func (p *progressCapturingStreamProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.gotOptions = append(p.gotOptions, opts)
	p.mu.Unlock()
	return &providers.LLMResponse{Content: p.content}, nil
}

func (p *progressCapturingStreamProvider) GetDefaultModel() string { return "mock-progress-model" }

func (p *progressCapturingStreamProvider) ChatStream(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, opts map[string]any,
	onChunk func(accumulated string),
	onProgress protocoltypes.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.gotOptions = append(p.gotOptions, opts)
	p.gotProgress = append(p.gotProgress, onProgress)
	p.mu.Unlock()
	onChunk(p.content)
	return &providers.LLMResponse{Content: p.content}, nil
}

var _ providers.StreamingProvider = (*progressCapturingStreamProvider)(nil)

// lastOptions returns the options map from the most recent Chat/ChatStream
// call, or nil if the provider was never called.
func (p *progressCapturingStreamProvider) lastOptions() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.gotOptions) == 0 {
		return nil
	}
	return p.gotOptions[len(p.gotOptions)-1]
}

// lastProgress returns the onProgress argument from the most recent
// ChatStream call, and whether ChatStream was called at all. The two are
// distinct: a nil callback from a real call is the failure this file exists
// to catch, and must not be confused with "never called".
func (p *progressCapturingStreamProvider) lastProgress() (protocoltypes.OnToolCallProgress, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.gotProgress) == 0 {
		return nil, false
	}
	return p.gotProgress[len(p.gotProgress)-1], true
}

// newProgressWiringTestLoop builds a real, single-default-agent AgentLoop
// around the given provider — mirrors newTestAgentLoop (loop_test.go) but
// accepts an arbitrary providers.LLMProvider rather than a fixed
// *mockProvider, since these tests need a StreamingProvider.
func newProgressWiringTestLoop(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	tmpDir := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(tmpDir, 0o700))
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
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	return al, msgBus
}

// TestRunTurn_ToolCallProgressCallback_WiredIntoChatStream is the baseline
// producer proof (no BeforeLLM hooks registered beyond the always-present,
// no-op HookManager pass-through — see NewAgentLoop's `al.hooks =
// NewHookManager(...)`): a real turn's ChatStream call must receive a
// non-nil onProgress argument. This is exactly what would have caught G1
// before it shipped: pre-fix, no caller supplied the callback at all, so
// this argument would have arrived nil.
func TestRunTurn_ToolCallProgressCallback_WiredIntoChatStream(t *testing.T) {
	provider := &progressCapturingStreamProvider{content: "hello there"}
	al, msgBus := newProgressWiringTestLoop(t, provider)

	// Force the streaming branch: activeProvider.(StreamingProvider) is
	// satisfied by progressCapturingStreamProvider, and a live streamer
	// makes al.bus.GetStreamer return hasStreamer=true (loop.go ~line
	// 8334-8391) — otherwise the turn would silently take the non-streaming
	// activeProvider.Chat(...) path instead of ChatStream.
	streamer := &asyncResultMockStreamer{}
	msgBus.SetStreamDelegate(&asyncResultMockStreamDelegate{streamer: streamer})

	_, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "webchat",
		Sender:  bus.SenderInfo{CanonicalID: "user:1", DisplayName: "Tester"},
		ChatID:  "direct",
		Content: "hi",
	})
	require.NoError(t, err)

	require.NotEmpty(t, streamer.updates, "test setup invariant: the streaming path must have engaged "+
		"(ChatStream, not Chat) — otherwise this test proves nothing about the streaming call site")

	cb, called := provider.lastProgress()
	require.True(t, called, "ChatStream must have been called")
	require.NotNil(t, cb, "ChatStream must receive a non-nil onProgress argument — this is the exact "+
		"wiring gap review finding G1 identifies: nothing in production ever supplied this callback, "+
		"so a delegated worker streaming a large tool-call argument produced zero observable progress "+
		"and was killed as 'hung'")

	// The callback must be safe to actually invoke (it is called
	// synchronously from the provider's SSE loop in production) — a smoke
	// check that it doesn't panic on a well-formed turnState. The
	// consumer-side effect of this call (a live progress snapshot reachable
	// via AgentLoop.ProgressForSession) is covered by
	// TestToolCallProgress_RecordAndRead_RaceFree below and by
	// pkg/tools' delegate_toolcall_progress_test.go.
	require.NotPanics(t, func() {
		cb(protocoltypes.ToolCallProgress{Index: 0, Name: "web_serve", ArgsBytes: 10, TotalArgsBytes: 10})
	})
}

// optionsReplacingHook simulates a BeforeLLM hook that takes
// HookActionModify and replaces llmReq.Options wholesale with a fresh map of
// its own. This was the documented CRITICAL HAZARD in loop.go's runTurn while
// the progress callback lived in that map: `llmOpts = llmReq.Options` (the
// hook-modify branch) silently dropped any previously-set option. Moving the
// callback to a ChatStream parameter closes it structurally; this hook keeps
// the hazard under test so it cannot be reopened by moving it back.
type optionsReplacingHook struct{}

func (h *optionsReplacingHook) BeforeLLM(_ context.Context, req *LLMHookRequest) (*LLMHookRequest, HookDecision, error) {
	next := req.Clone()
	next.Options = map[string]any{"replaced_by_hook": true}
	return next, HookDecision{Action: HookActionModify}, nil
}

func (h *optionsReplacingHook) AfterLLM(_ context.Context, resp *LLMHookResponse) (*LLMHookResponse, HookDecision, error) {
	return resp, HookDecision{Action: HookActionContinue}, nil
}

var _ LLMInterceptor = (*optionsReplacingHook)(nil)

// TestRunTurn_ToolCallProgressCallback_SurvivesBeforeLLMOptionsReplacement
// proves the CRITICAL HAZARD stays closed: even when a BeforeLLM hook
// replaces llmOpts entirely (HookActionModify with a fresh Options map), the
// callback still reaches ChatStream. It must FAIL if anyone reverts the
// callback to an options-map entry set before the hook block runs.
func TestRunTurn_ToolCallProgressCallback_SurvivesBeforeLLMOptionsReplacement(t *testing.T) {
	provider := &progressCapturingStreamProvider{content: "hello there"}
	al, msgBus := newProgressWiringTestLoop(t, provider)

	streamer := &asyncResultMockStreamer{}
	msgBus.SetStreamDelegate(&asyncResultMockStreamDelegate{streamer: streamer})

	require.NoError(t, al.hooks.Mount(NamedHook("test-options-replacer", &optionsReplacingHook{})))

	_, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "webchat",
		Sender:  bus.SenderInfo{CanonicalID: "user:1", DisplayName: "Tester"},
		ChatID:  "direct",
		Content: "hi",
	})
	require.NoError(t, err)

	require.NotEmpty(t, streamer.updates, "test setup invariant: the streaming path must have engaged")

	opts := provider.lastOptions()
	require.NotNil(t, opts)

	// Sanity-check the test's own premise: the hook really did replace the
	// map (if this fails, the test proves nothing about the hazard).
	_, hasReplacedMarker := opts["replaced_by_hook"]
	require.True(t, hasReplacedMarker,
		"test setup invariant: the hook must have actually replaced llmOpts — got: %+v", opts)

	cb, called := provider.lastProgress()
	require.True(t, called, "ChatStream must have been called")
	require.NotNil(t, cb, "the progress callback must survive a BeforeLLM hook that replaces llmOpts "+
		"wholesale via HookActionModify — it travels as a ChatStream argument precisely so no hook can "+
		"reach it")
}

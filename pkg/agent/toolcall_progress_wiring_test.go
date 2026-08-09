// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// G1 regression coverage (review finding, criticality 9): PRODUCER side.
//
// protocoltypes.ToolCallProgress support was added to the anthropic and
// openai_compat providers, but nothing in production ever supplied the
// options[protocoltypes.OnToolCallProgressKey] callback those providers read
// from — verified by grep: only the two provider call sites and test files
// referenced the key, never a real caller. The consequence: a model
// streaming a multi-kilobyte tool-call argument (a large SVG, a long file
// write) produced ZERO observable output on any surface, indistinguishable
// from a hung generation. An orchestrator polled a delegated worker 75 times
// over 46 seconds, saw nothing, and killed it mid-write — repeatedly.
//
// These tests prove loop.go actually wires the callback into the options
// map ChatStream is called with — the ONLY thing that would ever notice this
// wiring being silently dropped again, e.g. by a future BeforeLLM hook
// change. TestRunTurn_ToolCallProgressCallback_SurvivesBeforeLLMOptionsReplacement
// specifically covers the documented CRITICAL HAZARD: a hook taking
// HookActionModify replaces llmOpts wholesale, which would silently drop an
// earlier-set callback were it not re-injected AFTER the hook block.

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
	mu         sync.Mutex
	content    string
	gotOptions []map[string]any
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
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.gotOptions = append(p.gotOptions, opts)
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
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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
// non-nil callback under protocoltypes.OnToolCallProgressKey. This is
// exactly what would have caught G1 before it shipped: pre-fix, llmOpts
// never carried this key at all, so protocoltypes.ToolCallProgressFromOptions
// would have returned nil here.
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

	opts := provider.lastOptions()
	require.NotNil(t, opts, "provider must have been called with a non-nil options map")

	cb := protocoltypes.ToolCallProgressFromOptions(opts)
	require.NotNil(t, cb, "options[protocoltypes.OnToolCallProgressKey] must be set on the options map "+
		"ChatStream is called with — this is the exact wiring gap review finding G1 identifies: nothing "+
		"in production ever supplied this callback, so a delegated worker streaming a large tool-call "+
		"argument produced zero observable progress and was killed as 'hung'")

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
// its own — entirely unaware of protocoltypes.OnToolCallProgressKey. This is
// the documented CRITICAL HAZARD in loop.go's runTurn: `llmOpts =
// llmReq.Options` (the hook-modify branch) can silently drop any
// previously-set option, including the progress callback, if it is not
// re-applied afterward.
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
// proves loop.go's fix for the CRITICAL HAZARD: even when a BeforeLLM hook
// replaces llmOpts entirely (HookActionModify with a fresh Options map that
// never carried the progress key), the callback still reaches ChatStream.
// This must FAIL if the progress-key injection were moved back to
// alongside the other llmOpts entries (before the hook block) instead of
// after it — the exact regression this test guards against.
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

	cb := protocoltypes.ToolCallProgressFromOptions(opts)
	require.NotNil(t, cb, "the progress callback must survive a BeforeLLM hook that replaces llmOpts "+
		"wholesale via HookActionModify — loop.go must re-inject "+
		"protocoltypes.OnToolCallProgressKey AFTER the hook block runs, unconditionally, so it survives "+
		"regardless of what BeforeLLM did to the map")
}

// subturn_rc8_dead_override_test.go — RC-8 regression coverage.
//
// RC-8 defect: spawnSubTurn used to compute an elaborate `systemOverride` via
// resolveDelegateSoul and store it as processOptions.SystemPromptOverride,
// but nothing ever read that field — ContextBuilder.BuildMessages has no
// override parameter, and newTurnState never touched it. The fix (this
// branch) deletes that dead computation entirely rather than wiring it,
// because native-dispatch persona resolution already works correctly through
// a SEPARATE, alive path: agent.ContextBuilder is copied verbatim from
// execSource (the resolved delegate) and BuildSystemPrompt/BuildMessages
// resolve the soul from THAT builder's own agentID when the child turn
// actually runs (pkg/agent/context.go).
//
// This test proves the deletion did not silently break native persona
// resolution: it delegates to the seeded "worker" agent (which itself now
// carries a real compiled prompt as of the RC-6 fix — see
// pkg/coreagent/core.go's prompts["worker"]) and asserts the system-role
// message the LLM actually receives contains the worker's real persona
// text, using ONLY the alive ContextBuilder path — no
// SystemPromptOverride field exists on processOptions anymore, so there is
// nothing left for a future edit to silently "forget to read": the field
// itself is gone from this call site, and if it were ever reintroduced
// without being wired into ContextBuilder.BuildMessages, this test's
// assertion would keep passing or failing based on the SAME live mechanism
// it exercises today, not on the reintroduced dead field.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// rc8SystemPromptCapturingProvider is a minimal providers.LLMProvider stub
// that records the content of the system-role message it receives at Chat()
// call time, mirroring the identityCapturingProvider pattern used by
// subturn_target_identity_test.go for the sibling ADR-032 identity fix.
type rc8SystemPromptCapturingProvider struct {
	mu        sync.Mutex
	sawSystem string
	calls     int
}

func (p *rc8SystemPromptCapturingProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	for _, m := range messages {
		if m.Role == "system" {
			p.sawSystem = m.Content
			break
		}
	}
	return &providers.LLMResponse{Content: "worker output"}, nil
}

func (p *rc8SystemPromptCapturingProvider) GetDefaultModel() string { return "gpt-4o-mini" }

func (p *rc8SystemPromptCapturingProvider) snapshot() (system string, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawSystem, p.calls
}

// TestSpawnSubTurn_NativeDispatch_SystemPromptComesFromTargetContextBuilder
// proves the RC-8 deletion is safe: a native sub-turn delegated to the
// seeded "worker" agent gets the worker's real compiled persona (RC-6) in
// its system message purely through childTS.agent.ContextBuilder (copied
// from execSource) — the deleted opts.SystemPromptOverride field was never
// necessary for this to work, since ContextBuilder resolves the soul from
// its own agentID independently of anything spawnSubTurn writes onto
// processOptions.
func TestSpawnSubTurn_NativeDispatch_SystemPromptComesFromTargetContextBuilder(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	parentWorkspace := t.TempDir()
	workerWorkspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         parentWorkspace,
				DefaultModel: config.DefaultModel{Provider: "mock", Model: "parent-default-model"},
			},
			List: []config.AgentConfig{
				// The delegating PARENT: an ordinary, explicitly-registered
				// non-worker agent. No "main" sentinel to fall back to
				// anymore — GetDefaultAgent's Priority 2 skips workers, so
				// without this entry the only registered agent would be the
				// worker itself and the "parent and worker target must be
				// distinct agents" invariant below would fail.
				{
					ID:   "mia",
					Type: config.AgentTypeCore,
				},
				{
					ID:   string(coreagent.IDWorker),
					Name: "Worker",
					Type: config.AgentTypeWorker,
					Home: workerWorkspace,
					// No Subagents.Executor at all -> resolves native dispatch
					// (the zero value of ExecutorConfig.Kind).
				},
			},
		},
	}
	recorder := &rc8SystemPromptCapturingProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), recorder)
	t.Cleanup(al.Close)

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	target, ok := al.registry.GetAgent(string(coreagent.IDWorker))
	if !ok {
		t.Fatal("test setup: worker target agent not registered")
	}
	if target.ID == parent.ID {
		t.Fatal("test setup invariant broken: parent and worker target must be distinct agents")
	}

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-rc8-dead-override",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     sessionStore,
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-rc8-dead-override")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "sub-turn-config-model-unused-for-target-resolution",
		SystemPrompt:  "do the delegated task",
		TargetAgentID: string(coreagent.IDWorker),
		Async:         false,
	})
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	sawSystem, calls := recorder.snapshot()
	if calls == 0 {
		t.Fatal("mock provider Chat was never called — native dispatch did not run")
	}
	const wantMarker = "delegation-only executor"
	if !strings.Contains(sawSystem, wantMarker) {
		t.Errorf(
			"system message = %q\n\nwant it to contain the worker's real compiled persona marker %q — "+
				"native persona resolution must still work via ContextBuilder after the SystemPromptOverride deletion",
			sawSystem, wantMarker,
		)
	}
	if strings.Contains(sawSystem, "helpful AI assistant powered by Omnipus") {
		t.Errorf(
			"system message = %q\n\nmust NOT be the generic getIdentity() fallback — RC-6 gave the worker a real persona",
			sawSystem,
		)
	}
}

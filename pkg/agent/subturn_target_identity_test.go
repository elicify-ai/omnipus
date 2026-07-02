// subturn_target_identity_test.go — ADR-032 fix B regression coverage: the
// dispatch-kind decision (native vs external-cli) and the external run's
// workspace/model MUST come from the resolved TARGET (TargetAgentID), never
// the delegating parent's own AgentInstance.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestSpawnSubTurn_TargetIdentity_DispatchesExternalCLIFromTargetConfig proves
// the ADR-032 fix B routing bug is fixed: a NATIVE parent (no Subagents.Executor
// at all — the registry's default "main" agent) delegating to a subagent_3p
// TARGET must dispatch external-cli using the TARGET's own workspace and model
// — not fall back to native dispatch (the pre-fix bug, since the OLD code read
// the PARENT's Subagents field to decide dispatch kind), and not run in the
// PARENT's workspace with the PARENT's model (the OLD code's field source).
func TestSpawnSubTurn_TargetIdentity_DispatchesExternalCLIFromTargetConfig(t *testing.T) {
	fr, restore := withFakeDriver(t)
	defer restore()

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	parentWorkspace := t.TempDir()
	workerWorkspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Provider:  "mock",
				Workspace: parentWorkspace,
				ModelName: "parent-default-model",
			},
			List: []config.AgentConfig{
				{
					ID:        "ext-worker",
					Type:      config.AgentTypeWorker,
					Workspace: workerWorkspace,
					Model:     &config.AgentModelConfig{Primary: "claude-sonnet-4.6"},
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})

	// The delegating PARENT is the registry's default "main" agent: it has NO
	// Subagents.Executor at all (native by construction). Pre-fix, dispatch
	// would have resolved to NATIVE here regardless of the target, because the
	// dispatch decision read the parent's own (nil) Subagents field.
	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	if parent.Subagents != nil {
		t.Fatalf("test setup invariant broken: parent must have no Subagents.Executor, got %+v", parent.Subagents)
	}

	parentTS := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-target-identity",
		depth:          0,
		childTurnIDs:   []string{},
		pendingResults: make(chan *tools.ToolResult, 4),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
		session:        &ephemeralSessionStore{},
		agent:          parent,
	}

	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "worked"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-target-identity")
	// cfg.Model is deliberately a WRONG sentinel distinct from the target's own
	// configured model — spawnSubTurn requires Model non-empty for validation,
	// but for external-cli dispatch with a resolved target this value must be
	// IGNORED in favor of the target's own Model (proving the identity fix, not
	// just that some model string flows through).
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "WRONG-parent-model-must-not-be-used",
		SystemPrompt:  "delegate this task",
		TargetAgentID: "ext-worker",
		Async:         false,
	})
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1 (dispatch must have gone external-cli)", len(opts))
	}
	if opts[0].WorkDir != workerWorkspace {
		t.Errorf("WorkDir = %q, want the TARGET worker's own workspace %q (not the parent's %q)",
			opts[0].WorkDir, workerWorkspace, parentWorkspace)
	}
	if opts[0].Model != "claude-sonnet-4.6" {
		t.Errorf("Model = %q, want the TARGET worker's own configured model %q",
			opts[0].Model, "claude-sonnet-4.6")
	}
}

// TestSpawnSubTurn_TargetIdentity_UnresolvedTargetFallsBackToParent proves the
// best-effort fallback: when TargetAgentID names an agent that is NOT in the
// registry, dispatch falls back to the parent's own executor config (native,
// since the default agent has none) rather than erroring the sub-turn.
func TestSpawnSubTurn_TargetIdentity_UnresolvedTargetFallsBackToParent(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}

	parentTS := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-unresolved-target",
		depth:          0,
		childTurnIDs:   []string{},
		pendingResults: make(chan *tools.ToolResult, 4),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
		session:        &ephemeralSessionStore{},
		agent:          parent,
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-unresolved-target")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "delegate this task",
		TargetAgentID: "does-not-exist-in-registry",
		Async:         false,
		Timeout:       5 * time.Second,
	})
	// The unresolved target must NOT surface as a dispatch error — it degrades
	// to the parent's own (native) executor config and runs the native path.
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v (unresolved target must fall back to parent's config, not fail)", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil result from the native fallback path")
	}
}

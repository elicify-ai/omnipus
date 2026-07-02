// subturn_target_identity_test.go — ADR-032 fix B regression coverage: the
// dispatch-kind decision (native vs external-cli) and the external run's
// workspace/model MUST come from the resolved TARGET (TargetAgentID), never
// the delegating parent's own AgentInstance.

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/providers"
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

// TestSpawnSubTurn_TargetIdentity_PropagatesFullTargetConfig extends
// TestSpawnSubTurn_TargetIdentity_DispatchesExternalCLIFromTargetConfig (which
// only asserted WorkDir+Model) to the FULL identity/config surface FIX 1
// touches: ID, Name, MaxIterations, and the executor's CLIPath/CLIArgs/
// EnvOverrides must all come from the resolved TARGET, never the delegating
// PARENT.
func TestSpawnSubTurn_TargetIdentity_PropagatesFullTargetConfig(t *testing.T) {
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
					ID:        "ext-worker-full",
					Name:      "Worker Full Identity",
					Type:      config.AgentTypeWorker,
					Workspace: workerWorkspace,
					Model:     &config.AgentModelConfig{Primary: "claude-sonnet-4.6"},
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{
							Kind:         config.ExecutorKindExternalCLI,
							CLI:          "claude-code",
							CLIPath:      "/opt/target-only/claude-code",
							CLIArgs:      "--sandbox strict",
							EnvOverrides: map[string]string{"TARGET_ONLY_VAR": "target-value"},
						},
					},
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	target, ok := al.registry.GetAgent("ext-worker-full")
	if !ok {
		t.Fatal("test setup: target agent not registered")
	}
	if target.ID == parent.ID {
		t.Fatal("test setup invariant broken: parent and target must be distinct agents")
	}
	// MaxIterations has no per-agent config knob (it is a single global
	// MaxToolIterations default) — mutate the resolved registry instance
	// directly (white-box, same package) so the target's value is distinct
	// from the parent's, proving MaxIterations is read from targetAgent at
	// spawn time and not merely "some int flows through".
	target.MaxIterations = parent.MaxIterations + 137

	parentTS := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-full-identity",
		depth:          0,
		childTurnIDs:   []string{},
		pendingResults: make(chan *tools.ToolResult, 4),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
		session:        &ephemeralSessionStore{},
		agent:          parent,
	}

	collector, collectCleanup := newEventCollector(t, al)

	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "worked"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-full-identity")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "WRONG-parent-model-must-not-be-used",
		SystemPrompt:  "delegate this task",
		TargetAgentID: "ext-worker-full",
		Async:         false,
	})
	collectCleanup()
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	// FIX 1: the child's attributed agent ID (fed into PolicyApprovalReq.AgentID,
	// the transcript AgentID, and the SubTurnSpawn/End WS payloads) must be the
	// TARGET's, not the parent's. SubTurnSpawnPayload.AgentID is childTS.agentID
	// (turn.go:252, set from agent.ID at newTurnState time).
	var sawSpawn bool
	var gotAgentID string
	for _, e := range collector.events {
		if e.Kind == EventKindSubTurnSpawn {
			if p, ok := e.Payload.(SubTurnSpawnPayload); ok {
				sawSpawn = true
				gotAgentID = p.AgentID
			}
		}
	}
	if !sawSpawn {
		t.Fatal("expected a SubTurnSpawn event")
	}
	if gotAgentID != "ext-worker-full" {
		t.Errorf("SubTurnSpawnPayload.AgentID = %q, want the TARGET's ID %q (FIX 1 — wrong audit attribution)",
			gotAgentID, "ext-worker-full")
	}

	// MaxIterations / CLIPath / CLIArgs / EnvOverrides must all come from the
	// TARGET's executor config, not the parent's (which has none at all).
	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	if opts[0].MaxTurns != target.MaxIterations {
		t.Errorf("driver MaxTurns = %d, want the TARGET's MaxIterations %d", opts[0].MaxTurns, target.MaxIterations)
	}
	if opts[0].CLIPath != "/opt/target-only/claude-code" {
		t.Errorf("driver CLIPath = %q, want the TARGET's configured cli_path %q",
			opts[0].CLIPath, "/opt/target-only/claude-code")
	}
	if len(opts[0].CLIArgs) == 0 || opts[0].CLIArgs[0] != "--sandbox" {
		t.Errorf("driver CLIArgs = %v, want the TARGET's configured cli_args to be present (starting with --sandbox)",
			opts[0].CLIArgs)
	}
	if opts[0].EnvOverrides["TARGET_ONLY_VAR"] != "target-value" {
		t.Errorf("driver EnvOverrides[TARGET_ONLY_VAR] = %q, want %q (TARGET's env_overrides)",
			opts[0].EnvOverrides["TARGET_ONLY_VAR"], "target-value")
	}
}

// identityCapturingProvider is a minimal providers.LLMProvider stub that
// records the model string and the calling turn's agent.Workspace observed at
// Chat() call time — the workspace is pulled from ctx via
// turnStateFromContext, the same way runTurn's own providerCtx carries the
// turn state (loop.go: providerCtx := context.WithCancel(turnCtx), and
// turnCtx already has ts injected via withTurnState). Used by
// TestSpawnSubTurn_TargetIdentity_NativeDispatchKeepsParentConfig (arch #6) to
// prove the ADR-032 override block does NOT fire for native dispatch, by
// observing which agent's Model/Workspace actually reached the LLM call.
type identityCapturingProvider struct {
	mu           sync.Mutex
	sawModel     string
	sawWorkspace string
	calls        int
}

func (p *identityCapturingProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sawModel = model
	if ts := turnStateFromContext(ctx); ts != nil && ts.agent != nil {
		p.sawWorkspace = ts.agent.Workspace
	}
	p.calls++
	return &providers.LLMResponse{Content: "native worker output"}, nil
}

func (p *identityCapturingProvider) GetDefaultModel() string { return "gpt-4o-mini" }

func (p *identityCapturingProvider) snapshot() (model, workspace string, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawModel, p.sawWorkspace, p.calls
}

// TestSpawnSubTurn_TargetIdentity_NativeDispatchKeepsParentConfig (arch #6)
// proves the ADR-032 override block in spawnSubTurn is gated on
// dispatchKind == external-cli: when the resolved TARGET's own executor
// config is native (Subagents.Executor unset), the child run must use the
// PARENT's own Workspace and Model — the pre-ADR-032 native behavior —
// even though a target agent DID resolve. A regression that widened the
// override's condition to "targetAgent != nil" (dropping the dispatchKind
// guard) would leak the target's Model/Workspace into a native LLM call —
// exactly the "Model/Candidates/Provider mismatch" the override block's own
// comment says native dispatch must avoid.
func TestSpawnSubTurn_TargetIdentity_NativeDispatchKeepsParentConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	parentWorkspace := t.TempDir()
	targetWorkspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Provider:  "mock",
				Workspace: parentWorkspace,
				ModelName: "parent-native-model",
			},
			List: []config.AgentConfig{
				{
					ID:        "native-target",
					Type:      config.AgentTypeWorker,
					Workspace: targetWorkspace,
					Model:     &config.AgentModelConfig{Primary: "target-model-must-not-leak"},
					// No Subagents.Executor at all — resolves native (the
					// zero value of ExecutorConfig.Kind).
				},
			},
		},
	}
	recorder := &identityCapturingProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), recorder)

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	target, ok := al.registry.GetAgent("native-target")
	if !ok {
		t.Fatal("test setup: target agent not registered")
	}
	if target.Workspace == parent.Workspace || target.Model == parent.Model {
		t.Fatalf("test setup invariant broken: target and parent must have distinct Workspace/Model, got target=%+v parent=%+v",
			target, parent)
	}

	parentTS := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-native-keeps-config",
		depth:          0,
		childTurnIDs:   []string{},
		pendingResults: make(chan *tools.ToolResult, 4),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
		session:        &ephemeralSessionStore{},
		agent:          parent,
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-native-keeps-config")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "sub-turn-config-model-unused-for-target-resolution",
		SystemPrompt:  "delegate this task",
		TargetAgentID: "native-target",
		Async:         false,
	})
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	sawModel, sawWorkspace, calls := recorder.snapshot()
	if calls == 0 {
		t.Fatal("mock provider Chat was never called — native dispatch did not run")
	}
	if sawModel != parent.Model {
		t.Errorf("LLM call model = %q, want the PARENT's model %q (native dispatch must not adopt the target's Model)",
			sawModel, parent.Model)
	}
	if sawWorkspace != parent.Workspace {
		t.Errorf(
			"childTS.agent.Workspace = %q, want the PARENT's workspace %q (native dispatch must not adopt the target's Workspace)",
			sawWorkspace, parent.Workspace,
		)
	}
}

// TestSpawnSubTurn_TargetIdentity_ConcurrentModelSwitchRace is a regression
// guard for FIX 2 (data race): it drives a goroutine that repeatedly mutates
// the TARGET AgentInstance's mutex-protected Model field — the exact critical
// section shape SwitchModel/ApplyAgentModel (loop.go ~3479-3496) uses on a
// live registry AgentInstance (targetAgent.mu.Lock() around the write) —
// concurrently with spawnSubTurn reading that same field while resolving
// external-cli dispatch identity. Before FIX 2, the override block in
// subturn.go read targetAgent.Model directly, with no lock at all; go test
// -race reports that as a data race the instant the writer and reader
// overlap.
//
// The writer drives the mutex directly (rather than the full
// al.ApplyAgentModel, which additionally requires a resolvable provider
// config unrelated to the race under test) so this test hits the SAME field
// under the SAME lock shape without fragile config-resolution setup.
//
// Per the operator's instruction this is run WITHOUT -race locally (this dev
// pod OOMs linking the full pkg/agent test binary under -race — see
// CLAUDE.md "Testing & building") and only asserts functional correctness
// (no panic/deadlock, a valid model value is recorded). CI's `go test -race`
// gate is what actually exercises the race detector against this scenario.
func TestSpawnSubTurn_TargetIdentity_ConcurrentModelSwitchRace(t *testing.T) {
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
					ID:        "ext-worker-race",
					Type:      config.AgentTypeWorker,
					Workspace: workerWorkspace,
					Model:     &config.AgentModelConfig{Primary: "model-a"},
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	target, ok := al.registry.GetAgent("ext-worker-race")
	if !ok {
		t.Fatal("test setup: target agent not registered")
	}

	// Writer: repeatedly flips target.Model under target.mu for the duration
	// of the spawnSubTurn call below — the same critical section shape
	// ApplyAgentModel uses.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			target.mu.Lock()
			if i%2 == 0 {
				target.Model = "model-a"
			} else {
				target.Model = "model-b"
			}
			target.mu.Unlock()
			i++
		}
	}()

	parentTS := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-race",
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

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-race")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "irrelevant-parent-model",
		SystemPrompt:  "delegate this task",
		TargetAgentID: "ext-worker-race",
		Async:         false,
	})

	close(stop)
	wg.Wait()

	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	if opts[0].Model != "model-a" && opts[0].Model != "model-b" {
		t.Errorf(
			"driver Model = %q, want one of the writer's values (model-a/model-b) — a torn/garbage read would indicate a race",
			opts[0].Model,
		)
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

	collector, collectCleanup := newEventCollector(t, al)

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-unresolved-target")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "delegate this task",
		TargetAgentID: "does-not-exist-in-registry",
		Async:         false,
		Timeout:       5 * time.Second,
	})
	collectCleanup()
	// The unresolved target must NOT surface as a dispatch error — it degrades
	// to the parent's own (native) executor config and runs the native path.
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v (unresolved target must fall back to parent's config, not fail)", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil result from the native fallback path")
	}

	// FIX 3 (silent failure F1 / arch #5): the fallback must not be silent.
	// (a) The caller/LLM must see a warning prefix on ForLLM.
	if !strings.Contains(result.ForLLM, "delegation warning") ||
		!strings.Contains(result.ForLLM, "does-not-exist-in-registry") {
		t.Errorf(
			"result.ForLLM = %q, want it prefixed with a delegation warning naming the unresolved target %q",
			result.ForLLM, "does-not-exist-in-registry",
		)
	}
	// (b) The session/audit trail must carry an EventKindError for the
	// fallback (not just the process-log slog.Warn).
	var sawFallbackError bool
	for _, e := range collector.events {
		if e.Kind != EventKindError {
			continue
		}
		if p, ok := e.Payload.(ErrorPayload); ok && p.Stage == "subturn_delegation" {
			sawFallbackError = true
			if !strings.Contains(p.Message, "does-not-exist-in-registry") {
				t.Errorf("ErrorPayload.Message = %q, want it to name the unresolved target", p.Message)
			}
		}
	}
	if !sawFallbackError {
		t.Error("expected an EventKindError (stage=subturn_delegation) audit event for the unresolved-target fallback")
	}
}

// TestSpawnSubTurn_TargetIdentity_SelfDelegationNoFallbackWarning proves the
// FIX 3 warning is scoped to a REAL unresolved-target problem, not benign
// self-delegation (cfg.TargetAgentID == "", i.e. no delegation was requested
// at all). A regression that fired the warning unconditionally would spam
// every plain spawn/subagent call with a bogus "delegation warning".
func TestSpawnSubTurn_TargetIdentity_SelfDelegationNoFallbackWarning(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}

	parentTS := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-self-delegation",
		depth:          0,
		childTurnIDs:   []string{},
		pendingResults: make(chan *tools.ToolResult, 4),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
		session:        &ephemeralSessionStore{},
		agent:          parent,
	}

	collector, collectCleanup := newEventCollector(t, al)

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-self-delegation")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:        "test-model",
		SystemPrompt: "delegate this task",
		// TargetAgentID deliberately left empty: benign self-delegation.
		Async:   false,
		Timeout: 5 * time.Second,
	})
	collectCleanup()
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a non-nil result")
	}
	if strings.Contains(result.ForLLM, "delegation warning") {
		t.Errorf("result.ForLLM = %q, must NOT carry a delegation warning for benign self-delegation (TargetAgentID empty)",
			result.ForLLM)
	}
	for _, e := range collector.events {
		if e.Kind != EventKindError {
			continue
		}
		if p, ok := e.Payload.(ErrorPayload); ok && p.Stage == "subturn_delegation" {
			t.Errorf("unexpected subturn_delegation EventKindError for benign self-delegation: %+v", p)
		}
	}
}

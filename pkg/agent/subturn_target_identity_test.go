// subturn_target_identity_test.go — ADR-032 fix B regression coverage: the
// dispatch-kind decision (native vs external-cli) and the external run's
// workspace/model MUST come from the resolved TARGET (TargetAgentID), never
// the delegating parent's own AgentInstance. Also covers the native-dispatch
// identity+tool-policy+filesystem-scope fix (native sub-turns previously kept
// running as the PARENT — wrong ContextBuilder/soul, wrong ID for audit/
// canLoad attribution, a nil tool-policy that failed every tool closed to
// deny, and workspace-scoped tool objects — read_file/write_file/bash/... —
// still bound to the parent's workspace even after the Workspace string field
// was corrected): native dispatch now adopts the target's ID/Name/Workspace/
// ContextBuilder/policy/Tools, while deliberately keeping the parent's
// Model/Candidates/Provider.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// stiMintParentSession mints a REAL, store-backed session in al's shared
// session store and returns its id plus the store itself, for use as a
// parent turnState's transcriptSessionID/routingSessionID/transcriptStore.
//
// ADR-057 FR-005 fixture repair: spawnSubTurn now mints the child via
// al.GetSessionStore().CreateSessionWithID(childID,
// parentTS.transcriptSessionID, ...) against the REAL shared store
// (subturn.go ~1036-1048) — a parent turnState literal with no
// transcriptSessionID (the `session: &ephemeralSessionStore{}` field on
// these literals is a DIFFERENT, unrelated concept — the turn's in-memory
// chat-history store, not the transcript-persistence session id) fails
// before spawnSubTurn does anything else with "subturn: create child
// session ...: unified_store: create session with id: invalid parent id:
// unified_store: invalid session ID \"\"".
func stiMintParentSession(t *testing.T, al *AgentLoop) (id string, store *session.UnifiedStore) {
	t.Helper()
	store = al.GetSessionStore()
	if store == nil {
		t.Fatal("test harness did not wire a shared session store")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	if err != nil {
		t.Fatalf("store.NewSession: %v", err)
	}
	return meta.ID, store
}

// stiNewNestedHomeAgentLoop mirrors loop_test.go's newTestAgentLoop but nests
// Home under tmpDir/agents/main rather than using tmpDir directly.
//
// ADR-057 FR-005/FR-096 fixture repair: AgentLoop resolves its shared
// session-store root as filepath.Dir(cfg.AgentHomeBasePath()) (loop.go:724).
// newTestAgentLoop's Home is a FLAT os.MkdirTemp("", "agent-test-*") result,
// so that root resolves to the literal OS temp directory — shared by EVERY
// test in this package that uses the same flat-Home pattern. Because
// CreateSessionWithID's sessions/ directory is a SIBLING of Home (not a
// child of it), newTestAgentLoop's cleanup (a bare os.RemoveAll(Home)) never
// removes it, so it accumulates real session directories across test runs.
// A deterministic child id like "subturn-1" (al.generateSubTurnID's
// per-AgentLoop counter restarts at 1 for every fresh *AgentLoop) then
// collides with a same-named leftover from any other test/run that reached
// CreateSessionWithID via the same flat-Home pattern (FR-096 refuses the
// collision). Nesting Home under its own private tmpDir isolates each
// test's session store so this test — which must actually reach a
// successful CreateSessionWithID, unlike the sibling
// TestSpawnSubTurn_TargetIdentity_UnresolvedTargetAborts, which aborts
// before ever reaching it and is unaffected — does not depend on that
// shared, never-cleaned directory being empty.
func stiNewNestedHomeAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "agent-sti-test-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	agentHome := filepath.Join(tmpDir, "agents", "main")
	if err := os.MkdirAll(agentHome, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q): %v", agentHome, err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              agentHome,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: agentHome}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})
	t.Cleanup(al.Close)
	return al
}

// TestSpawnSubTurn_TargetIdentity_DispatchesExternalCLIFromTargetConfig proves
// the ADR-032 fix B routing bug is fixed: a NATIVE parent (no Subagents.Executor
// at all — a real registered non-worker agent resolved as the registry's
// default) delegating to a subagent_3p TARGET must dispatch external-cli
// using the TARGET's own workspace and model — not fall back to native
// dispatch (the pre-fix bug, since the OLD code read the PARENT's Subagents
// field to decide dispatch kind), and not run in the PARENT's workspace with
// the PARENT's model (the OLD code's field source).
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
				Home:         parentWorkspace,
				DefaultModel: config.DefaultModel{Provider: "mock", Model: "parent-default-model"},
			},
			List: []config.AgentConfig{
				// The delegating PARENT: an ordinary, explicitly-registered
				// non-worker agent (no "main" sentinel to fall back to
				// anymore — GetDefaultAgent's Priority 2 skips workers, so
				// without this entry the only registered agent would be the
				// worker itself and the "native parent" premise below would
				// not hold).
				{
					ID:   "mia",
					Type: config.AgentTypeCore,
				},
				{
					ID:    "ext-worker",
					Type:  config.AgentTypeWorker,
					Home:  workerWorkspace,
					Model: &config.AgentModelConfig{Primary: "claude-sonnet-4.6"},
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
			},
		},
	}
	// ADR-046 P1 (FR-007/008): dispatch is always workspace-scoped, so
	// "ext-worker" needs CoreTeam membership to execute at all. Persist a
	// DEDICATED workspace listing ONLY "ext-worker" — deliberately NOT the
	// shared test-harness workspace mustNewAgentLoop would otherwise seed it
	// into (testHarnessAgentIDs' doc comment) — so this stays a genuine
	// identity-source regression check: if dispatch ever mistakenly resolved
	// the PARENT's identity instead of the TARGET's, the parent (a member of
	// the shared harness workspace only) would NOT be a member of this
	// workspace and the run would be refused rather than silently
	// succeeding in the wrong place.
	workerWsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(workerWsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces dir: %v", err)
	}
	wsJSON := `{"id":"ext-worker-ws","core_team":["ext-worker"]}`
	if err := os.WriteFile(filepath.Join(workerWsDir, "ext-worker-ws.json"), []byte(wsJSON), 0o644); err != nil {
		t.Fatalf("write dedicated workspace record: %v", err)
	}
	wantWorkDir := filepath.Join(workerWsDir, "ext-worker-ws", "work")

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})

	// The delegating PARENT is the registry's default agent ("mia", the only
	// non-worker registered): it has NO Subagents.Executor at all (native by
	// construction). Pre-fix, dispatch would have resolved to NATIVE here
	// regardless of the target, because the dispatch decision read the
	// parent's own (nil) Subagents field.
	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	if parent.Subagents != nil {
		t.Fatalf("test setup invariant broken: parent must have no Subagents.Executor, got %+v", parent.Subagents)
	}

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-target-identity",
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
	// ADR-046 P1: WorkDir is always the resolved workspace's work/ dir,
	// keyed off the TARGET's identity — never the parent's workspace, and
	// never the target's raw agent.Home (workerWorkspace) either.
	if opts[0].WorkDir != wantWorkDir {
		t.Errorf(
			"WorkDir = %q, want the TARGET worker's own workspace's work/ dir %q (not the parent's %q, not the target's raw Home %q)",
			opts[0].WorkDir,
			wantWorkDir,
			parentWorkspace,
			workerWorkspace,
		)
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
				Home:         parentWorkspace,
				DefaultModel: config.DefaultModel{Provider: "mock", Model: "parent-default-model"},
			},
			List: []config.AgentConfig{
				// The delegating PARENT: an ordinary, explicitly-registered
				// non-worker agent. No "main" sentinel to fall back to
				// anymore — GetDefaultAgent's Priority 2 skips workers, so
				// without this entry the only registered agent would be the
				// worker itself and the "parent and target must be distinct
				// agents" invariant below would fail.
				{
					ID:   "mia",
					Type: config.AgentTypeCore,
				},
				{
					ID:    "ext-worker-full",
					Name:  "Worker Full Identity",
					Type:  config.AgentTypeWorker,
					Home:  workerWorkspace,
					Model: &config.AgentModelConfig{Primary: "claude-sonnet-4.6"},
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

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-full-identity",
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
// records the model string and the calling turn's agent.Home/agent.ID
// observed at Chat() call time — pulled from ctx via turnStateFromContext,
// the same way runTurn's own providerCtx carries the turn state (loop.go:
// providerCtx := context.WithCancel(turnCtx), and turnCtx already has ts
// injected via withTurnState). Used by
// TestSpawnSubTurn_NativeDispatch_AdoptsTargetIdentityKeepsParentModel to
// observe exactly which agent's Model/Workspace/ID actually reached the LLM
// call and the tool-execution context.
type identityCapturingProvider struct {
	mu           sync.Mutex
	sawModel     string
	sawWorkspace string
	sawAgentID   string
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
		p.sawWorkspace = ts.agent.Home
		p.sawAgentID = ts.agent.ID
	}
	p.calls++
	return &providers.LLMResponse{Content: "native worker output"}, nil
}

func (p *identityCapturingProvider) GetDefaultModel() string { return "gpt-4o-mini" }

func (p *identityCapturingProvider) snapshot() (model, workspace, agentID string, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawModel, p.sawWorkspace, p.sawAgentID, p.calls
}

// TestSpawnSubTurn_NativeDispatch_AdoptsFullTargetIdentityIncludingModel
// proves the operator-confirmed no-inheritance principle: a delegated
// sub-turn inherits NOTHING from the parent — delegating to an agent means
// running that agent's own real instance. For a NATIVE target, spawnSubTurn
// adopts the TARGET's Workspace/ID/Name/ContextBuilder/Model (so the
// sub-turn's system prompt, memory, skills, file/bash cwd, AND the model it
// actually thinks with are genuinely the delegate's own). An earlier version
// of this fix deliberately kept Model/Candidates/Provider/ProviderPool as the
// PARENT's (citing a mutex/mismatch risk) — that was superseded: the risk is
// resolved by reading the WHOLE Model/Provider/Candidates/ThinkingLevel quad
// from the SAME source (execSource) under one lock, never mixing sources.
// The identity-leak bug this test also guards against: reusing the parent's
// own ContextBuilder meant a delegate answered AS the parent — observed live,
// delegating to Worker (an intentionally soul-less agent) returned Jim's own
// compiled persona verbatim.
func TestSpawnSubTurn_NativeDispatch_AdoptsFullTargetIdentityIncludingModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	parentWorkspace := t.TempDir()
	targetWorkspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         parentWorkspace,
				DefaultModel: config.DefaultModel{Provider: "mock", Model: "parent-native-model"},
			},
			List: []config.AgentConfig{
				// The delegating PARENT: an ordinary, explicitly-registered
				// non-worker agent. No "main" sentinel to fall back to
				// anymore — GetDefaultAgent's Priority 2 skips workers, so
				// without this entry the only registered agent would be the
				// worker itself and the "target and parent must have
				// distinct Workspace/Model" invariant below would fail.
				{
					ID:   "mia",
					Type: config.AgentTypeCore,
				},
				{
					ID:    "native-target",
					Type:  config.AgentTypeWorker,
					Home:  targetWorkspace,
					Model: &config.AgentModelConfig{Primary: "target-model-must-not-leak"},
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
	if target.Home == parent.Home || target.Model == parent.Model {
		t.Fatalf(
			"test setup invariant broken: target and parent must have distinct Workspace/Model, got target=%+v parent=%+v",
			target,
			parent,
		)
	}

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-native-keeps-config",
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

	sawModel, sawWorkspace, sawAgentID, calls := recorder.snapshot()
	if calls == 0 {
		t.Fatal("mock provider Chat was never called — native dispatch did not run")
	}
	// Model DOES come from the TARGET — no inheritance from the parent for
	// ANY agent-level setting, including which model the delegate thinks
	// with.
	if sawModel != target.Model {
		t.Errorf(
			"LLM call model = %q, want the TARGET's model %q (native dispatch must not inherit the parent's Model)",
			sawModel,
			target.Model,
		)
	}
	// Workspace comes from the TARGET too — the identity fix's whole point is
	// that the sub-turn's file/bash cwd and ContextBuilder-resolved context
	// (SOUL.md, memory, skills) are the delegate's own, not the parent's.
	if sawWorkspace != target.Home {
		t.Errorf(
			"childTS.agent.Home = %q, want the TARGET's workspace %q (native dispatch must adopt the target's full identity)",
			sawWorkspace,
			target.Home,
		)
	}
	// ID DOES come from the TARGET too — this is what tools.WithAgentID
	// threads into the tool-execution context (loop.go), which is what
	// load_tool's canLoad resolver and the audit/tool-approval/transcript
	// attribution all key off. A native sub-turn attributed to the parent's
	// ID is the wrong-audit-attribution half of the identity bug.
	if sawAgentID != target.ID {
		t.Errorf(
			"childTS.agent.ID = %q, want the TARGET's ID %q (native dispatch must attribute the run to the delegate, not the parent)",
			sawAgentID,
			target.ID,
		)
	}
}

// policyCapturingProvider is a providers.LLMProvider stub that records the
// child sub-turn's OWN resolved tool policy for one probe tool, observed at
// Chat() call time via turnStateFromContext — the same context path
// identityCapturingProvider uses. Used to prove which agent's policy a native
// sub-turn actually runs under, without needing the model to attempt a real
// tool call.
type policyCapturingProvider struct {
	mu           sync.Mutex
	probeTool    string
	sawVerdict   string
	sawAgentNil  bool
	sawPolicyNil bool
	calls        int
}

func (p *policyCapturingProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ts := turnStateFromContext(ctx)
	if ts == nil || ts.agent == nil {
		p.sawAgentNil = true
	} else {
		policy := ts.agent.LoadToolPolicy()
		if policy == nil {
			p.sawPolicyNil = true
		} else {
			p.sawVerdict = tools.ResolveEffectivePolicy(policy, p.probeTool)
		}
	}
	p.calls++
	return &providers.LLMResponse{Content: "native worker output"}, nil
}

func (p *policyCapturingProvider) GetDefaultModel() string { return "gpt-4o-mini" }

func (p *policyCapturingProvider) snapshot() (verdict string, agentNil, policyNil bool, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawVerdict, p.sawAgentNil, p.sawPolicyNil, p.calls
}

// TestSpawnSubTurn_NativeDispatch_AdoptsTargetToolPolicy proves the other half
// of the identity fix: a native sub-turn's resolved tool policy is the
// TARGET's own — not a nil (deny-everything) snapshot, and not silently the
// PARENT's. Before this fix, AgentInstance.toolPolicy (an unexported atomic
// field) was never copied onto the child's ad-hoc struct literal, so
// LoadToolPolicy() returned nil there — FilterToolsByPolicy treats nil as an
// empty policy, which fails EVERY tool closed to deny
// (resolveEffectivePolicyWith: no entry on either side -> deny). Separately,
// load_tool's canLoad resolver looked up "the calling agent" via a fresh
// registry lookup keyed by tools.ToolAgentID(ctx) — which, before the
// identity half of this fix, was still the PARENT's ID for native dispatch —
// so the load-gate and the execution-gate disagreed about whose policy
// applied. This test sets the parent and target to OPPOSITE verdicts for the
// same tool so either half of the old bug (nil-deny-all, or silently-the-
// parent's-policy) would be caught.
func TestSpawnSubTurn_NativeDispatch_AdoptsTargetToolPolicy(t *testing.T) {
	const probeTool = "bash"

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	parentWorkspace := t.TempDir()
	targetWorkspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         parentWorkspace,
				DefaultModel: config.DefaultModel{Provider: "mock", Model: "parent-model"},
				// ADR-054 D6.4: AgentConfig.Default (set below on
				// parent-allow-policy, kept for realism) is no longer
				// consulted by GetDefaultAgent — the settings singleton is
				// the only resolution signal now. mustNewAgentLoop's
				// construction path wires this into
				// registry.SetDefaultAgentOverride exactly as production
				// boot does (loop.go).
				DefaultAgentID: "parent-allow-policy",
			},
			List: []config.AgentConfig{
				{
					ID:      "parent-allow-policy",
					Type:    config.AgentTypeCore,
					Default: true,
					Home:    parentWorkspace,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{probeTool: config.ToolPolicyAllow},
						},
					},
				},
				{
					ID:   "native-target-policy",
					Type: config.AgentTypeWorker,
					Home: targetWorkspace,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{probeTool: config.ToolPolicyDeny},
						},
					},
					// No Subagents.Executor — resolves native.
				},
			},
		},
	}
	recorder := &policyCapturingProvider{probeTool: probeTool}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), recorder)

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	if got := tools.ResolveEffectivePolicy(parent.LoadToolPolicy(), probeTool); got != string(config.ToolPolicyAllow) {
		t.Fatalf("test setup invariant broken: parent's own %q policy = %q, want allow", probeTool, got)
	}
	target, ok := al.registry.GetAgent("native-target-policy")
	if !ok {
		t.Fatal("test setup: target agent not registered")
	}
	if got := tools.ResolveEffectivePolicy(target.LoadToolPolicy(), probeTool); got != string(config.ToolPolicyDeny) {
		t.Fatalf("test setup invariant broken: target's own %q policy = %q, want deny", probeTool, got)
	}

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-native-policy",
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

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-native-policy")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "sub-turn-config-model-unused-for-target-resolution",
		SystemPrompt:  "delegate this task",
		TargetAgentID: "native-target-policy",
		Async:         false,
	})
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	verdict, agentNil, policyNil, calls := recorder.snapshot()
	if calls == 0 {
		t.Fatal("mock provider Chat was never called — native dispatch did not run")
	}
	if agentNil {
		t.Fatal("turnStateFromContext(ctx).agent was nil during the sub-turn's own LLM call")
	}
	if policyNil {
		t.Fatal(
			"child sub-turn's LoadToolPolicy() returned nil — toolPolicy was never copied onto the child (deny-all bug)",
		)
	}
	if verdict != string(config.ToolPolicyDeny) {
		t.Errorf(
			"child sub-turn's resolved policy for %q = %q, want %q (the TARGET's own policy, not the parent's %q)",
			probeTool, verdict, config.ToolPolicyDeny, config.ToolPolicyAllow,
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
// filesystemScopedToolProvider is a two-step scripted providers.LLMProvider:
// call 1 emits a read_file tool call, call 2 returns a plain-text answer. It
// captures the "tool" role message injected before call 2 — the ACTUAL result
// the read_file tool produced — so the test can assert on real file content,
// not just an AgentInstance.Home string field.
type filesystemScopedToolProvider struct {
	mu             sync.Mutex
	calls          int
	relPath        string
	sawToolContent string
}

func (p *filesystemScopedToolProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "call-read-1", Name: "read_file", Arguments: map[string]any{"path": p.relPath}},
			},
		}, nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			p.sawToolContent = messages[i].Content
			break
		}
	}
	return &providers.LLMResponse{Content: "read the file"}, nil
}

func (p *filesystemScopedToolProvider) GetDefaultModel() string { return "gpt-4o-mini" }

func (p *filesystemScopedToolProvider) snapshot() (toolContent string, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawToolContent, p.calls
}

// TestSpawnSubTurn_NativeDispatch_AdoptsTargetWorkspaceForFileTools proves the
// fix's file/bash confinement claim actually holds, not just the
// AgentInstance.Home STRING field: workspace-scoped tools
// (read_file/write_file/edit_file/bash/...) bind their root directory ONCE,
// at NewAgentInstance construction time (tools.NewReadFileTool(workspace,
// ...)) — CloneExcept is a shallow filter that copies the SAME underlying
// tool pointers, it does not rebind them. Sourcing agent.Tools from
// baseAgent.Tools (the pre-fix behavior) would leave a native delegate's
// actual file/bash sandbox boundary silently pinned to the PARENT's
// workspace even after Workspace/ID/Name/ContextBuilder are correctly
// swapped. Both the parent's and target's workspace contain a file with the
// SAME NAME but DIFFERENT CONTENT — the sub-turn's own read_file call must
// return the TARGET's content, proving the underlying tool object (not just
// the AgentInstance.Home string) is the target's own.
func TestSpawnSubTurn_NativeDispatch_AdoptsTargetWorkspaceForFileTools(t *testing.T) {
	const relPath = "identity.txt"
	const parentContent = "I am the PARENT's file"
	const targetContent = "I am the TARGET's file"

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	// ADR-046 P1 (FR-007/008): execution is always workspace-scoped now — a
	// native agent's file tools resolve to ITS OWN workspace's work/ dir
	// (loop.go's runTurn re-root, keyed on agent identity), not a raw
	// AgentConfig.Home path directly. To keep proving the ADR-032
	// distinctness property this test exists for (native dispatch must use
	// the TARGET's own tool objects, never the PARENT's), give the parent and
	// the target their OWN DEDICATED workspaces — never the same one — and
	// write each one's file into ITS OWN workspace's work/ dir. Pre-creating
	// these before mustNewAgentLoop also means the test harness's own
	// membership seeding (test_helpers_test.go) sees both IDs already
	// covered and leaves them alone (see testHarnessAgentIDs' doc comment).
	workspacesDir := filepath.Join(home, "workspaces")
	parentWorkDir := filepath.Join(workspacesDir, "parent-ws", "work")
	targetWorkDir := filepath.Join(workspacesDir, "target-ws", "work")
	if err := os.MkdirAll(parentWorkDir, 0o755); err != nil {
		t.Fatalf("mkdir parent work dir: %v", err)
	}
	if err := os.MkdirAll(targetWorkDir, 0o755); err != nil {
		t.Fatalf("mkdir target work dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacesDir, "parent-ws.json"),
		[]byte(`{"id":"parent-ws","core_team":["parent-fs-scope"]}`), 0o644); err != nil {
		t.Fatalf("write parent-ws.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacesDir, "target-ws.json"),
		[]byte(`{"id":"target-ws","core_team":["native-target-fs-scope"]}`), 0o644); err != nil {
		t.Fatalf("write target-ws.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentWorkDir, relPath), []byte(parentContent), 0o600); err != nil {
		t.Fatalf("write parent file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetWorkDir, relPath), []byte(targetContent), 0o600); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                filepath.Join(home, "agents", "parent-fs-scope"),
				DefaultModel:        config.DefaultModel{Provider: "mock", Model: "parent-model"},
				RestrictToWorkspace: true, // required for read_file to actually root relative paths at the re-rooted workspace dir, not the raw host fs (buildFs: !restrict -> hostFs{})
			},
			List: []config.AgentConfig{
				{
					ID:      "parent-fs-scope",
					Type:    config.AgentTypeCore,
					Default: true,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow},
						},
					},
				},
				{
					ID:   "native-target-fs-scope",
					Type: config.AgentTypeWorker,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow},
						},
					},
					// No Subagents.Executor — resolves native.
				},
			},
		},
	}
	recorder := &filesystemScopedToolProvider{relPath: relPath}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), recorder)

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	if _, ok := al.registry.GetAgent("native-target-fs-scope"); !ok {
		t.Fatal("test setup: target agent not registered")
	}

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-native-fs-scope",
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

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-native-fs-scope")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "sub-turn-config-model-unused-for-target-resolution",
		SystemPrompt:  "read the identity file and report its content",
		TargetAgentID: "native-target-fs-scope",
		Async:         false,
	})
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	toolContent, calls := recorder.snapshot()
	if calls < 2 {
		t.Fatalf("mock provider Chat called %d times, want >= 2 (tool call + follow-up)", calls)
	}
	if strings.Contains(toolContent, parentContent) {
		t.Fatalf(
			"read_file result contained the PARENT's file content (%q) — the child's file tools are still scoped to the parent's workspace, not the target's",
			toolContent,
		)
	}
	if !strings.Contains(toolContent, targetContent) {
		t.Errorf(
			"read_file result = %q, want it to contain the TARGET's file content %q (native dispatch must adopt the target's own workspace-bound tool objects, not just the Workspace string field)",
			toolContent,
			targetContent,
		)
	}
}

// providerPoolCapturingProvider wraps a real providers.LLMProvider and
// records, via turnStateFromContext (the same context path every other
// capturing provider in this file uses), whether the sub-turn's OWN
// AgentInstance.providerPool was ever populated — direct field access is
// legal here (same package as AgentInstance). Wrapping (rather than
// stubbing) a real provider is necessary because buildProviderPool only
// publishes a non-nil pool when it can actually resolve+construct a
// provider for at least one candidate (findModelConfigForProvider /
// CreateProviderFromConfig against real cfg.Providers entries) — a bare
// "mock" provider config with no matching cfg.Providers entry always yields
// an empty pool, which buildProviderPool folds to nil, making it
// indistinguishable from "never copied" (the exact bug this test guards
// against).
type providerPoolCapturingProvider struct {
	wrapped    providers.LLMProvider
	mu         sync.Mutex
	sawPoolNil bool
	calls      int
}

func (p *providerPoolCapturingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	ts := turnStateFromContext(ctx)
	if ts == nil || ts.agent == nil {
		p.sawPoolNil = true
	} else if ts.agent.providerPool.Load() == nil {
		p.sawPoolNil = true
	}
	p.mu.Unlock()
	return p.wrapped.Chat(ctx, messages, toolDefs, model, opts)
}

func (p *providerPoolCapturingProvider) GetDefaultModel() string { return p.wrapped.GetDefaultModel() }

func (p *providerPoolCapturingProvider) snapshot() (poolNil bool, calls int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sawPoolNil, p.calls
}

// TestSpawnSubTurn_ProviderPoolCopiedFromParent is a regression guard for the
// deferred follow-up the security-lead review surfaced alongside the
// identity/policy/filesystem fix above: AgentInstance.providerPool is an
// unexported atomic field the struct literal in spawnSubTurn cannot copy,
// same as toolPolicy — left unset, GetProviderForCandidate silently falls
// back to agent.Provider for every FR-007 provider-pinned fallback
// candidate, in every sub-turn (native or external-cli), not just ones with
// a resolved delegation target. This test asserts the child's pool is
// non-nil — i.e., genuinely copied from baseAgent, not left at its
// atomic.Pointer zero value — using a plain self-delegation (no
// TargetAgentID) since the pool is baseAgent-sourced unconditionally, not
// gated on a resolved target. Config mirrors newPoolTestConfig
// (provider_pool_test.go) — two distinct configured providers are required
// for buildProviderPool to actually publish a non-nil pool at all (see
// providerPoolCapturingProvider's doc comment).
func TestSpawnSubTurn_ProviderPoolCopiedFromParent(t *testing.T) {
	t.Setenv("W4_17_OPENROUTER_KEY_SUBTURN", "or-key")
	t.Setenv("W4_17_ANTHROPIC_KEY_SUBTURN", "anth-key")

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	parentWorkspace := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              parentWorkspace,
				DefaultModel:      config.DefaultModel{Provider: "openrouter", Model: "anthropic/claude-sonnet-4.6"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — self-delegation
			// below resolves the parent via GetDefaultAgent, which needs a
			// REAL registered agent.
			List: []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCore}},
		},
		Providers: []*config.ModelConfig{
			{
				Provider:  "openrouter",
				Model:     "anthropic/claude-sonnet-4.6",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "W4_17_OPENROUTER_KEY_SUBTURN",
			},
			{
				Provider:  "anthropic",
				Model:     "claude-haiku-4-5-20251001",
				APIBase:   "https://api.anthropic.com",
				APIKeyRef: "W4_17_ANTHROPIC_KEY_SUBTURN",
			},
		},
	}
	// The WRAPPED provider (what actually answers Chat()) is a plain mock —
	// no network call, always succeeds. cfg.Providers above is what matters
	// for buildProviderPool: it calls providers.CreateProviderFromConfig
	// directly against those entries to populate the pool, entirely
	// independent of whichever provider is passed as this agent's primary.
	// Those pool-entry provider objects are never invoked by this test (only
	// their presence in the pool is asserted), so they need no real network
	// connectivity either.
	recorder := &providerPoolCapturingProvider{wrapped: &simpleMockProviderAPI{response: "ok"}}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), recorder)

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}
	if parent.providerPool.Load() == nil {
		t.Fatal(
			"test setup invariant broken: parent's own providerPool is nil — NewAgentInstance did not publish one, this test cannot distinguish copied-vs-not",
		)
	}

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-provider-pool",
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

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-provider-pool")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:        "sub-turn-config-model-unused-for-target-resolution",
		SystemPrompt: "do the task",
		// TargetAgentID intentionally empty — self-delegation; the pool copy
		// is unconditional, not gated on a resolved target.
		Async: false,
	})
	if err != nil {
		t.Fatalf("spawnSubTurn error: %v", err)
	}
	if result == nil || result.Err != nil {
		t.Fatalf("expected a successful result, got %+v", result)
	}

	poolNil, calls := recorder.snapshot()
	if calls == 0 {
		t.Fatal("mock provider Chat was never called — sub-turn did not run")
	}
	if poolNil {
		t.Error(
			"child sub-turn's providerPool is nil — not copied from baseAgent (FR-007 provider-pinned fallback candidates would silently fall back to the primary provider)",
		)
	}
}

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
				Home:         parentWorkspace,
				DefaultModel: config.DefaultModel{Provider: "mock", Model: "parent-default-model"},
			},
			List: []config.AgentConfig{
				// The delegating PARENT: an ordinary, explicitly-registered
				// non-worker agent. No "main" sentinel to fall back to
				// anymore — without this, GetDefaultAgent's Priority 2
				// (which skips workers) would degenerate to returning
				// "ext-worker-race" itself, collapsing this into a
				// self-delegation and defeating the parent/target
				// distinctness this test relies on.
				{
					ID:   "mia",
					Type: config.AgentTypeCore,
				},
				{
					ID:    "ext-worker-race",
					Type:  config.AgentTypeWorker,
					Home:  workerWorkspace,
					Model: &config.AgentModelConfig{Primary: "model-a"},
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

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-race",
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

// TestSpawnSubTurn_TargetIdentity_UnresolvedTargetAborts proves ADR-054
// D7/§9's revised behavior: when TargetAgentID names an agent that is NOT in
// the registry — deleted, renamed, or its entities/agents/<id>.json record
// failed to load — spawnSubTurn ABORTS the sub-turn rather than silently
// falling back to the parent's own identity/tool policy.
//
// This supersedes the earlier "best-effort fallback to baseAgent" posture
// (FIX 3, TestSpawnSubTurn_TargetIdentity_UnresolvedTargetFallsBackToParent):
// that fallback WAS the privilege-inversion bug D7 named — the child ran
// with the PARENT's tool policy (execSource.StoreToolPolicy below), defeating
// the entire purpose of delegating to a distinct, possibly more-restricted
// worker.
func TestSpawnSubTurn_TargetIdentity_UnresolvedTargetAborts(t *testing.T) {
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

	if err == nil {
		t.Fatal("expected spawnSubTurn to abort with an error for an unresolved delegation target, got nil")
	}
	if !errors.Is(err, ErrDelegationTargetUnresolved) {
		t.Errorf("err = %v, want errors.Is(err, ErrDelegationTargetUnresolved)", err)
	}
	if !strings.Contains(err.Error(), "does-not-exist-in-registry") {
		t.Errorf("err = %v, want it to name the unresolved target %q", err, "does-not-exist-in-registry")
	}
	if result != nil {
		t.Errorf("expected a nil result on abort, got %+v", result)
	}
}

// TestSpawnSubTurn_TargetIdentity_SelfDelegationNoFallbackWarning proves the
// FIX 3 warning is scoped to a REAL unresolved-target problem, not benign
// self-delegation (cfg.TargetAgentID == "", i.e. no delegation was requested
// at all). A regression that fired the warning unconditionally would spam
// every plain spawn/subagent call with a bogus "delegation warning".
func TestSpawnSubTurn_TargetIdentity_SelfDelegationNoFallbackWarning(t *testing.T) {
	// ADR-057 FR-005/FR-096 fixture repair: this test's spawnSubTurn call is
	// expected to SUCCEED (unlike TestSpawnSubTurn_TargetIdentity_UnresolvedTargetAborts,
	// which aborts before ever reaching CreateSessionWithID), so it cannot
	// use newTestAgentLoop's flat, shared-OS-temp-root Home — see
	// stiNewNestedHomeAgentLoop's doc comment for why that collides on a
	// deterministic "subturn-1" child id.
	al := stiNewNestedHomeAgentLoop(t)

	parent := al.registry.GetDefaultAgent()
	if parent == nil {
		t.Fatal("test setup: no default agent")
	}

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-self-delegation",
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
		t.Errorf(
			"result.ForLLM = %q, must NOT carry a delegation warning for benign self-delegation (TargetAgentID empty)",
			result.ForLLM,
		)
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

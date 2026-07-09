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
	"github.com/elicify-ai/omnipus/pkg/tools"
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
// records the model string and the calling turn's agent.Workspace/agent.ID
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
		p.sawWorkspace = ts.agent.Workspace
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

// TestSpawnSubTurn_NativeDispatch_AdoptsTargetIdentityKeepsParentModel proves
// the CURRENT (post-identity-fix) split: for a NATIVE target, spawnSubTurn
// adopts the TARGET's Workspace/ID/Name/ContextBuilder (so the sub-turn's
// system prompt, memory, skills, and file/bash cwd are genuinely the
// delegate's own — the pre-fix bug this replaces had a native delegate
// silently answering as the PARENT, verbatim, because it reused the parent's
// own ContextBuilder), while Model/Candidates/Provider/ProviderPool stay the
// PARENT's — those are the mutex-protected fields that drive the native
// LLM-call's own provider resolution, and swapping Model alone without also
// rebuilding Candidates/ProviderPool would risk a "model configured but no
// matching provider" mismatch. A regression that widened the swap to include
// Model (or narrowed it back to skip Workspace/ID/ContextBuilder for native)
// would violate one half of this split.
func TestSpawnSubTurn_NativeDispatch_AdoptsTargetIdentityKeepsParentModel(t *testing.T) {
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
		t.Fatalf(
			"test setup invariant broken: target and parent must have distinct Workspace/Model, got target=%+v parent=%+v",
			target,
			parent,
		)
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

	sawModel, sawWorkspace, sawAgentID, calls := recorder.snapshot()
	if calls == 0 {
		t.Fatal("mock provider Chat was never called — native dispatch did not run")
	}
	// Model stays the PARENT's — native dispatch deliberately does not adopt
	// the target's Model (see the mutex/ProviderPool rationale above).
	if sawModel != parent.Model {
		t.Errorf("LLM call model = %q, want the PARENT's model %q (native dispatch must not adopt the target's Model)",
			sawModel, parent.Model)
	}
	// Workspace DOES come from the TARGET now — the identity fix's whole
	// point is that the sub-turn's file/bash cwd and ContextBuilder-resolved
	// context (SOUL.md, memory, skills) are the delegate's own, not the
	// parent's.
	if sawWorkspace != target.Workspace {
		t.Errorf(
			"childTS.agent.Workspace = %q, want the TARGET's workspace %q (native dispatch must adopt the target's identity, just not its Model)",
			sawWorkspace,
			target.Workspace,
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
				Provider:  "mock",
				Workspace: parentWorkspace,
				ModelName: "parent-model",
			},
			List: []config.AgentConfig{
				{
					ID:        "parent-allow-policy",
					Type:      config.AgentTypeCore,
					Default:   true,
					Workspace: parentWorkspace,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{probeTool: config.ToolPolicyAllow},
						},
					},
				},
				{
					ID:        "native-target-policy",
					Type:      config.AgentTypeWorker,
					Workspace: targetWorkspace,
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

	parentTS := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-native-policy",
		depth:          0,
		childTurnIDs:   []string{},
		pendingResults: make(chan *tools.ToolResult, 4),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
		session:        &ephemeralSessionStore{},
		agent:          parent,
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
		t.Fatal("child sub-turn's LoadToolPolicy() returned nil — toolPolicy was never copied onto the child (deny-all bug)")
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
// not just an AgentInstance.Workspace string field.
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
// AgentInstance.Workspace STRING field: workspace-scoped tools
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
// the AgentInstance.Workspace string) is the target's own.
func TestSpawnSubTurn_NativeDispatch_AdoptsTargetWorkspaceForFileTools(t *testing.T) {
	const relPath = "identity.txt"
	const parentContent = "I am the PARENT's file"
	const targetContent = "I am the TARGET's file"

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	parentWorkspace := t.TempDir()
	targetWorkspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(parentWorkspace, relPath), []byte(parentContent), 0o600); err != nil {
		t.Fatalf("write parent file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(targetWorkspace, relPath), []byte(targetContent), 0o600); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Provider:            "mock",
				Workspace:           parentWorkspace,
				ModelName:           "parent-model",
				RestrictToWorkspace: true, // required for read_file to actually root relative paths at Workspace, not the raw host fs (buildFs: !restrict -> hostFs{})
			},
			List: []config.AgentConfig{
				{
					ID:        "parent-fs-scope",
					Type:      config.AgentTypeCore,
					Default:   true,
					Workspace: parentWorkspace,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow},
						},
					},
				},
				{
					ID:        "native-target-fs-scope",
					Type:      config.AgentTypeWorker,
					Workspace: targetWorkspace,
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

	parentTS := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-native-fs-scope",
		depth:          0,
		childTurnIDs:   []string{},
		pendingResults: make(chan *tools.ToolResult, 4),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
		session:        &ephemeralSessionStore{},
		agent:          parent,
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
			toolContent, targetContent,
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

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// U5B: the delegate -> external-CLI branch. A delegated session whose TARGET
// resolves to runner.DispatchKindExternalCLI must be driven through the same
// shared runner the task path uses (runExternalCLISubTurn) and the same gate
// (runner.ResolveDispatch) — never the native loop. These tests drive the real
// steer.SessionLauncher.Launch + Dispatch path (no private-function shortcut)
// against a real AgentLoop and a fake external driver.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const (
	delegateExtCLIAgentID       = "delegate-ext-worker"
	delegateRemoteA2AAgentID    = "delegate-remote-reserved"
	delegateNativeWorkerAgentID = "delegate-native-worker"
)

// delegateDispatchProvider is a thread-safe call counter: the turn under test
// runs on the dispatch goroutine, so the test's assertion reads race with the
// increment otherwise.
type delegateDispatchProvider struct {
	calls atomic.Int64
}

func (p *delegateDispatchProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{Content: "native loop answered", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *delegateDispatchProvider) GetDefaultModel() string { return "delegate-dispatch-stub" }

// newDelegateDispatchLoop builds a real AgentLoop holding a native agent
// (testDefaultAgentID) plus an external-CLI agent and a reserved remote-a2a
// agent, with the same lifecycle/inbox/boot-epoch wiring newSteerAL installs
// so the real Launch+Dispatch admission path is exercised.
func newDelegateDispatchLoop(t *testing.T, provider providers.LLMProvider) *AgentLoop {
	t.Helper()
	// Seed a default workspace carrying the ordinary caller→target edges the
	// launcher's graph gate (FR-014) requires: startingRemainingDepth resolves
	// the governing workspace and refuses (steer.ErrInvalidEdge) when no
	// caller→target edge exists, so an edge-less fixture cannot reach Launch at
	// all (the CHECK's "Launch fixture fails before body" — INCONCLUSIVE). The
	// steerer is testDefaultAgentID (newSteeringSessionWithActiveAgent switches
	// the chat to it), so the edges run from it. Must run BEFORE mustNewAgentLoop
	// so OMNIPUS_HOME is set when the membership seeder reads it.
	seedWorkspaceGraph(t, "01JXDISPATCHSEC000000000001", true, []graphEdge{
		edge(testDefaultAgentID, delegateExtCLIAgentID, nil, nil),
		edge(testDefaultAgentID, delegateNativeWorkerAgentID, nil, nil),
		// A self-delegation is an ORDINARY edge (FR-014): the launch-to-mia
		// tests need the caller→caller self-edge, exactly like any other target.
		edge(testDefaultAgentID, testDefaultAgentID, nil, nil),
	})
	tmpDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	workerHome := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: testDefaultAgentID, Home: tmpDir},
				{
					ID: delegateExtCLIAgentID, Name: "External Worker", Home: workerHome,
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
				{
					// F4 fixture: a WORKER launched with a NATIVE executor — the
					// pre-update state of the supported native→external executor
					// change (pkg/gateway/rest_agents_update.go::
					// restAPIUpdateAgentFlow.validateTarget permits a worker to
					// change executor.kind). Type=worker so the fixture matches a
					// real REST-updatable worker entity.
					ID: delegateNativeWorkerAgentID, Name: "Native Worker", Home: workerHome,
					Description: "u5b F4 runtime-change fixture (native worker)",
					Type:        config.AgentTypeWorker,
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindNative},
					},
				},
				{
					ID: delegateRemoteA2AAgentID, Name: "Reserved Remote", Home: workerHome,
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A},
					},
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	home := al.GetConfig().Agents.Defaults.Home
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)

	epochDir := filepath.Join(home, "boot_epoch")
	if err := os.MkdirAll(epochDir, 0o700); err != nil {
		t.Fatalf("create boot epoch dir: %v", err)
	}
	boot := session.NewBootEpochStore(epochDir)
	if _, err := boot.Mint(); err != nil {
		t.Fatalf("mint boot epoch: %v", err)
	}
	al.SetBootEpochStore(boot)
	t.Cleanup(func() { al.Close() })
	return al
}

// waitUntil polls cond until it holds or the timeout expires.
func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// newSteeringSessionWithActiveAgent creates the steering chat session with an
// explicit ActiveAgentID.
//
// Launch's fail-closed parent-identity guard
// (config.DelegateToolConfig.RequireParentAgentID, which resolves TRUE when
// unset) reads steererMeta.ActiveAgentID, and a freshly created session
// carries NO ActiveAgentID (session-core U1/DEL-11: createSessionLocked no
// longer seeds it). So the guard is satisfied the same way production
// satisfies it — a genuine handover: the session is created under one agent
// and switched to mia, which is what UnifiedStore.SwitchAgent is for. Writing
// the field any other way, or flipping the guard off in the harness, would
// hide that requirement instead of meeting it.
//
// (That the guard and the empty-ActiveAgentID producer disagree today is a
// PRE-EXISTING failure on this branch tip, not this lane's: on the untouched
// tip, TestLaunch_AtExhaustedDepthBudget_Refused already fails with
// "delegating agent identity is empty" on its first Launch. Reported, not
// fixed here.)
func newSteeringSessionWithActiveAgent(t *testing.T, al *AgentLoop) string {
	t.Helper()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", delegateExtCLIAgentID)
	if err != nil {
		t.Fatalf("NewSession(steerer): %v", err)
	}
	if err := al.GetSessionStore().SwitchAgent(meta.ID, testDefaultAgentID); err != nil {
		t.Fatalf("SwitchAgent(steerer -> %s): %v", testDefaultAgentID, err)
	}
	return meta.ID
}

// launchDelegateTo launches a delegate-origin session to targetAgentID and
// returns the launch result — the same front door pkg/tools/delegate_run.go's
// launchAndDispatch uses.
func launchDelegateTo(t *testing.T, al *AgentLoop, targetAgentID, task string) steer.LaunchResult {
	t.Helper()
	l := NewSteerLauncher(al)
	root := newSteeringSessionWithActiveAgent(t, al)
	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: root,
		TargetAgentID:     targetAgentID,
		Task:              task,
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-" + targetAgentID},
	})
	if err != nil {
		t.Fatalf("Launch(-> %s): %v", targetAgentID, err)
	}
	return res
}

// TestDelegateToExternalCLITarget_RunsSharedRunner_NotNativeLoop is the U5B
// regression proof: a delegate whose target resolves to DispatchKindExternalCLI
// is driven by the shared external runner (fake driver records the Run), the
// native LLM provider is never called, and the turn is cleaned up (no leaked
// active turn, which is what running a branch outside runTurn would cause).
func TestDelegateToExternalCLITarget_RunsSharedRunner_NotNativeLoop(t *testing.T) {
	provider := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	const task = "run the external job"
	res := launchDelegateTo(t, al, delegateExtCLIAgentID, task)

	if _, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	waitUntil(t, 5*time.Second, "the external runner to start", func() bool {
		return len(fr.RecordedRunOpts()) > 0
	})

	// Let the run end cleanly, then wait for the turn to be disposed.
	fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "cli output here"}})
	fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
	fr.Cancel()

	waitUntil(t, 5*time.Second, "the steered turn to be cleared from activeTurnStates", func() bool {
		_, stillActive := al.activeTurnStates.Load(res.SessionID)
		return !stillActive
	})

	if got := provider.calls.Load(); got != 0 {
		t.Fatalf("native LLM provider was called %d time(s); a delegate to an external-CLI target must never run the native loop", got)
	}
	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("external runner Run called %d time(s), want exactly 1", len(opts))
	}
	if !strings.Contains(opts[0].Input, task) {
		t.Fatalf("external runner Input = %q, want it to carry the delegated task %q", opts[0].Input, task)
	}
}

// TestDelegateToNativeTarget_RunsNativeLoop_NotExternalRunner is the
// companion guard: the fix must not route native delegates through the
// external runner.
func TestDelegateToNativeTarget_RunsNativeLoop_NotExternalRunner(t *testing.T) {
	provider := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, provider)

	fr, restore := withFakeDriver(t)
	defer restore()

	res := launchDelegateTo(t, al, testDefaultAgentID, "do the native job")

	if _, err := NewSteerLauncher(al).Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	waitUntil(t, 5*time.Second, "the native loop to call the provider", func() bool {
		return provider.calls.Load() > 0
	})
	waitUntil(t, 5*time.Second, "the steered turn to be cleared from activeTurnStates", func() bool {
		_, stillActive := al.activeTurnStates.Load(res.SessionID)
		return !stillActive
	})

	if got := len(fr.RecordedRunOpts()); got != 0 {
		t.Fatalf("external runner Run called %d time(s) for a native delegate; want 0", got)
	}
}

// TestLaunch_StampsIs3PFromTargetExecutorKind proves the durable 3P stamp the
// arch ruling requires: the child's LifecycleRecord.Is3P is resolved once, at
// launch, from the SAME runner.ResolveDispatch outcome the runtime body
// dispatch uses — true for an external-CLI target, false for a native one.
func TestLaunch_StampsIs3PFromTargetExecutorKind(t *testing.T) {
	provider := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, provider)
	lifecycle := al.GetSessionLifecycleStore()

	ext := launchDelegateTo(t, al, delegateExtCLIAgentID, "external task")
	extRec, err := lifecycle.Load(ext.SessionID)
	if err != nil {
		t.Fatalf("load external child record: %v", err)
	}
	if !extRec.Is3P {
		t.Fatalf("external-CLI child record Is3P = false, want true (stamped at launch from ResolveDispatch)")
	}

	native := launchDelegateTo(t, al, testDefaultAgentID, "native task")
	nativeRec, err := lifecycle.Load(native.SessionID)
	if err != nil {
		t.Fatalf("load native child record: %v", err)
	}
	if nativeRec.Is3P {
		t.Fatalf("native child record Is3P = true, want false")
	}
}

// TestLaunch_UnresolvableExecutorKind_Refused proves a target whose executor
// kind cannot be resolved (the reserved remote-a2a) refuses the launch before
// any child exists — never a silent native fallback, and never a stamped
// child.
func TestLaunch_UnresolvableExecutorKind_Refused(t *testing.T) {
	provider := &delegateDispatchProvider{}
	al := newDelegateDispatchLoop(t, provider)

	l := NewSteerLauncher(al)
	root := newSteeringSessionWithActiveAgent(t, al)
	_, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: root,
		TargetAgentID:     delegateRemoteA2AAgentID,
		Task:              "reserved target",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-remote"},
	})
	if err == nil {
		t.Fatalf("Launch to a reserved remote-a2a target must be refused, got nil error")
	}
	if !strings.Contains(err.Error(), delegateRemoteA2AAgentID) {
		t.Fatalf("refusal %q should name the offending agent %q", err.Error(), delegateRemoteA2AAgentID)
	}
}

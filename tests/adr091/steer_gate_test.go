// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 fix lane RX-TESTS. Three properties the suite asserted only by
// reading the code, or by a test that could not tell the right answer from
// the wrong one:
//
//  1. I-2's "Dispatch decides atomically under one lock". Every existing
//     admission test is strictly SEQUENTIAL, so a lock narrowed to exclude
//     the `len(g.active)` read would be invisible to all of them and to
//     -race (no test ever ran two Dispatch calls at once).
//  2. D9's "the timeout's scope is the session's LIFETIME across re-entries".
//     The existing integration test set a 1s limit, backdated CreatedAt by
//     500ms and bounded the call in a 5s select — both "half the budget is
//     already spent" and "the budget restarted on this wake" land inside 5s,
//     so it passed either way.
//  3. A timed-out child STARTS NO FURTHER TOOL CALLS, and its timeout notice
//     has exactly one owner. Coverage lost with the deleted
//     pkg/agent/subturn_timeout_stops_child_test.go.
//
// Everything here runs a real *agent.AgentLoop with its real session,
// lifecycle and inbox stores, the real production audience resolver and the
// real SteerLauncher — never a fake standing in for the decision under test.
package adr091_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const gateAgentID = "adr091-fixture-agent-a"

// steerFixture is a real AgentLoop wired for ADR-091 steering, with one
// ordinary-root steering session and no delegation tree — the tree fixture
// dispatches three children at construction time, which would consume the
// very admission slots the concurrency test is about.
type steerFixture struct {
	al        *agent.AgentLoop
	launcher  *agent.SteerLauncher
	lifecycle *session.LifecycleStore
	recorder  *testutil.OutboundRecorder
	upward    *recordingUpwardDeliverer
	msgBus    *bus.MessageBus
	root      string
}

func newSteerFixture(t *testing.T, provider providers.LLMProvider, runLoop bool) *steerFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	writeGateWorkspace(t, home)

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{
			Home:         filepath.Join(home, "agents"),
			DefaultModel: config.DefaultModel{Model: "scripted-model"},
			MaxTokens:    4096,
		},
		List: []config.AgentConfig{{
			ID: gateAgentID, Name: "ADR-091 fixture A", Type: config.AgentTypeCustom,
			Home: filepath.Join(home, "agents", "a"),
		}},
	}}
	al, err := agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(al.Close)

	lifecycle := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "inbox"))
	al.SetSessionMessagingStores(inbox, lifecycle)

	recorder := testutil.RecordingOutbound(t)
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	resolver := agent.NewSteerAudienceResolver(classifier)
	concrete := agent.NewSteerUpwardDeliverer()
	recording := &recordingUpwardDeliverer{inner: concrete}
	// Two calls for the same reason newE2EHarnessCustom needs two: the
	// concrete deliverer's *AgentLoop back-wiring only happens when
	// SetSteerAudienceDeps is handed the concrete type, and a wrapper hides
	// it (steer_boundary.go's type assertion).
	al.SetSteerAudienceDeps(resolver, recorder, concrete)
	al.SetSteerAudienceDeps(resolver, recorder, recording)

	launcher := agent.NewSteerLauncher(al)
	al.SetSteerSessionLauncher(launcher)

	if runLoop {
		runCtx, stopLoop := context.WithCancel(context.Background())
		loopDone := make(chan struct{})
		go func() { defer close(loopDone); _ = al.Run(runCtx) }()
		// Cancel AND WAIT. t.Cleanup runs BEFORE t.TempDir's own removal, so a
		// loop still draining its inbox keeps writing into a directory Go is
		// deleting: "TempDir RemoveAll cleanup: directory not empty" fails the
		// test for a reason unrelated to anything it asserts.
		t.Cleanup(func() {
			stopLoop()
			<-loopDone
		})
	}

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", gateAgentID)
	if err != nil {
		t.Fatalf("NewSession(steering root): %v", err)
	}
	workspaceID := "adr091-fixture-workspace"
	if err := al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &workspaceID}); err != nil {
		t.Fatalf("SetMeta(steering root): %v", err)
	}

	return &steerFixture{
		al: al, launcher: launcher, lifecycle: lifecycle, recorder: recorder,
		upward: recording, msgBus: msgBus, root: meta.ID,
	}
}

func writeGateWorkspace(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create fixture workspace directory: %v", err)
	}
	record, err := json.Marshal(map[string]any{
		"id": "adr091-fixture-workspace", "core_team": []string{gateAgentID},
	})
	if err != nil {
		t.Fatalf("marshal fixture workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "adr091-fixture-workspace.json"), record, 0o600); err != nil {
		t.Fatalf("write fixture workspace: %v", err)
	}
}

func (f *steerFixture) launchChild(t *testing.T, callID string) (string, int) {
	t.Helper()
	result, err := f.launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: f.root, TargetAgentID: gateAgentID, Label: callID,
		Task:   "gate fixture worker " + callID,
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("Launch(%s): %v", callID, err)
	}
	return result.SessionID, result.Generation
}

// blockingProvider blocks every Chat call until release is closed, so an
// admitted turn can never finish (and free its slot) while a test is
// observing the admission gate.
type blockingProvider struct {
	release <-chan struct{}
	calls   atomic.Int32
}

func (p *blockingProvider) Chat(
	ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "released"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*blockingProvider) GetDefaultModel() string { return "scripted-model" }

// TestDispatch_ConcurrentCallsAdmitExactlyOneUnderACapOfOne is the missing
// concurrency proof for I-2's "Dispatch — not Launch — decides ATOMICALLY
// under the admission lock" (pkg/agent/admission.go::steerAdmission.tryAdmit).
//
// Every pre-existing admission test calls Dispatch sequentially, so the
// read-then-write pair `len(g.active) >= effectiveCap` / `g.active[id] = gen`
// could be split across the lock and nothing would notice: no test ever had
// two callers inside tryAdmit at the same time, so -race had nothing to
// observe either. This fires eight Dispatch calls from eight goroutines
// released by one barrier against a cap of 1.
//
// The provider blocks for the whole test, so no admitted turn can finish and
// hand its slot on: "exactly one admitted, everyone else queued at a
// distinct FIFO position" is an invariant here, not a timing accident.
func TestDispatch_ConcurrentCallsAdmitExactlyOneUnderACapOfOne(t *testing.T) {
	const workers = 8
	release := make(chan struct{})
	provider := &blockingProvider{release: release}
	f := newSteerFixture(t, provider, false)
	defer close(release)
	f.al.GetConfig().Performance.MaxParallelAgents = 1

	type dispatched struct {
		result steer.DispatchResult
		err    error
	}
	results := make([]dispatched, workers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := range workers {
		sessionID, generation := f.launchChild(t, fmt.Sprintf("call-concurrent-%d", i))
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			result, err := f.launcher.Dispatch(context.Background(), sessionID, generation)
			results[i] = dispatched{result: result, err: err}
		}()
	}
	start.Done()
	done.Wait()

	running, queued := 0, 0
	positions := map[int]int{}
	for i, got := range results {
		if got.err != nil {
			t.Fatalf("Dispatch(worker %d): %v", i, got.err)
		}
		switch got.result.State {
		case steer.DispatchRunning:
			running++
		case steer.DispatchQueued:
			queued++
			positions[got.result.QueuePosition]++
		default:
			t.Fatalf("Dispatch(worker %d).State = %q, want running or queued", i, got.result.State)
		}
	}
	if running != 1 {
		t.Fatalf("%d of %d concurrent Dispatch calls were admitted against a cap of 1 — the admission "+
			"decision is NOT atomic: the `len(g.active) >= cap` read and the `g.active[id] = gen` write "+
			"are not both inside the same lock hold (I-2, admission.go::steerAdmission.tryAdmit)",
			running, workers)
	}
	if queued != workers-1 {
		t.Fatalf("queued = %d, want %d (every caller the single slot could not take)", queued, workers-1)
	}
	// FIFO positions must be a permutation of 1..workers-1: a torn
	// read/append would hand two callers the same position or skip one.
	for position := 1; position <= workers-1; position++ {
		if positions[position] != 1 {
			t.Fatalf("queue position %d was handed out %d times (all positions: %v) — want exactly once; "+
				"duplicate or missing positions mean the queue append raced the admission decision",
				position, positions[position], positions)
		}
	}
}

// --- D9: the timeout's scope is the session's lifetime, not one wake -------

const gateSlowToolName = "adr091_gate_slow_tool"
const gateSecondToolName = "adr091_gate_second_tool"

// blockUntilDoneTool blocks until its context is cancelled — the tool the
// timed-out child is still inside when its deadline fires.
type blockUntilDoneTool struct {
	tools.BaseTool
	started chan struct{}
	once    sync.Once
}

func (*blockUntilDoneTool) Name() string        { return gateSlowToolName }
func (*blockUntilDoneTool) Description() string { return "blocks until its context is done" }
func (*blockUntilDoneTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*blockUntilDoneTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (p *blockUntilDoneTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return tools.ErrorResult("slow tool cancelled: " + ctx.Err().Error())
}

// countingTool records whether it was ever executed. A timed-out child must
// never reach it.
type countingTool struct {
	tools.BaseTool
	calls atomic.Int32
}

func (*countingTool) Name() string        { return gateSecondToolName }
func (*countingTool) Description() string { return "records that it ran" }
func (*countingTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*countingTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (p *countingTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	p.calls.Add(1)
	return tools.NewToolResult("second tool ran")
}

// twoToolProvider answers the first Chat call with TWO queued tool calls (a
// slow one and a second one) and every later call with more work — so a
// child that survives its deadline visibly keeps going.
type twoToolProvider struct{ calls atomic.Int32 }

func (p *twoToolProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{
		{ID: "gate-slow-call", Function: &providers.FunctionCall{Name: gateSlowToolName, Arguments: `{}`}},
		{ID: "gate-second-call", Function: &providers.FunctionCall{Name: gateSecondToolName, Arguments: `{}`}},
	}}, nil
}

func (*twoToolProvider) GetDefaultModel() string { return "scripted-model" }

func (f *steerFixture) allowTools(t *testing.T, registered ...tools.Tool) {
	t.Helper()
	policies := map[string]config.ToolPolicy{}
	for _, tool := range registered {
		f.al.RegisterTool(tool)
		policies[tool.Name()] = config.ToolPolicyAllow
	}
	for _, agentID := range f.al.GetRegistry().ListAgentIDs() {
		if instance, ok := f.al.GetRegistry().GetAgent(agentID); ok {
			instance.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: policies})
		}
	}
}

func (f *steerFixture) awaitState(t *testing.T, sessionID string, want session.LifecycleState, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last session.LifecycleState
	for time.Now().Before(deadline) {
		record, err := f.lifecycle.Load(sessionID)
		if err == nil {
			last = record.State
			if record.State == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s state = %q after %s, want %q", sessionID, last, within, want)
}

// TestTimeout_TimedOutChild_StartsNoFurtherToolCallsAndTellsOneOwner
// restores the coverage deleted with
// pkg/agent/subturn_timeout_stops_child_test.go
// (TestSubTurn_TimedOutChild_StartsNoFurtherToolCalls and
// TestSpawnSubTurn_SettledTimeoutPublicationChoosesSingleOwner), re-aimed at
// the ADR-091 mechanism that replaced the sub-turn ring: the run deadline
// steer_launcher.go::steeredTurnRunContext installs from
// rec.SteeredBy.Limits.TimeoutSeconds.
//
// The child's model response queues [slow_tool, second_tool] and the child is
// still inside slow_tool when its limit fires. Afterwards:
//   - second_tool must NEVER have executed (nothing further starts),
//   - the model must not have been called again,
//   - the record must read timed_out,
//   - and the timeout notice must have exactly ONE owner: one upward delivery
//     to the steering session, and nothing at all on the user-facing bus.
func TestTimeout_TimedOutChild_StartsNoFurtherToolCallsAndTellsOneOwner(t *testing.T) {
	provider := &twoToolProvider{}
	f := newSteerFixture(t, provider, false)
	slow := &blockUntilDoneTool{started: make(chan struct{})}
	second := &countingTool{}
	f.allowTools(t, slow, second)

	// The budget is deliberately generous and re-anchored at NOW. D9 scopes
	// the limit to the session's LIFETIME (CreatedAt + TimeoutSeconds), so a
	// short budget left at its launch-time anchor can already be spent
	// before the turn starts on a loaded machine — the turn then never
	// reaches its first tool at all and this test would be measuring
	// nothing. Re-anchoring gives the child the whole budget from dispatch.
	const childBudget = 12 * time.Second
	childID, childGen := f.launchChild(t, "call-timeout-stops-child")
	if err := f.lifecycle.Mutate(childID, func(record *session.LifecycleRecord) error {
		record.SteeredBy.Limits.TimeoutSeconds = int(childBudget / time.Second)
		record.CreatedAt = time.Now()
		return nil
	}); err != nil {
		t.Fatalf("Mutate(child timeout): %v", err)
	}
	if _, err := f.launcher.Dispatch(context.Background(), childID, childGen); err != nil {
		t.Fatalf("Dispatch(child): %v", err)
	}

	select {
	case <-slow.started:
	case <-time.After(childBudget):
		t.Fatal("the child never entered its first tool call within its whole time budget — the fixture " +
			"proves nothing about what happens after the deadline")
	}
	// Settle on the FIRST of "the child stopped" or "the child started the
	// next queued tool anyway", so the no-further-tool-calls failure is
	// reported as itself rather than as a downstream timeout waiting for a
	// terminal state a runaway child never reaches.
	settleDeadline := time.Now().Add(childBudget + 15*time.Second)
	for time.Now().Before(settleDeadline) {
		if second.calls.Load() > 0 {
			break
		}
		if record, err := f.lifecycle.Load(childID); err == nil && record.Terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := second.calls.Load(); got != 0 {
		t.Fatalf("the second queued tool ran %d times after the child's deadline fired — a timed-out "+
			"child must START NO FURTHER TOOL CALLS (loop_run_turn_tools.go::"+
			"agentLoopRunTurnToolsExecute.validateCall's turn-context-done skip)", got)
	}
	f.awaitState(t, childID, session.LifecycleTimedOut, childBudget+15*time.Second)

	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("the model was called %d times, want exactly 1 — a timed-out child must not go back to "+
			"the model for another round", got)
	}

	// Exactly one owner for the notice: the steering session, through the one
	// upward path (steer_completion.go::completeSteeredTurn -> Deliver).
	if got := f.upward.countFor(childID); got != 1 {
		t.Fatalf("upward deliveries for the timed-out child = %d, want exactly 1 — the timeout notice "+
			"must have exactly one owner, neither lost nor announced twice", got)
	}
	// And nobody else: nothing reaches the user-facing outbound bus.
	for {
		select {
		case outbound := <-f.msgBus.OutboundChan():
			t.Fatalf("a steered child's timeout reached the user-facing outbound bus: %+v — the notice "+
				"has a second owner", outbound)
		default:
			return
		}
	}
}

// TestWake_TimeoutBudgetIsTheSessionLifetimeNotThisWake is the distinguishing
// re-entry test D9 demands ("the timeout's scope is the session's lifetime
// across re-entries").
//
// The existing integration test (pkg/agent::TestWake_AppliesConfiguredTimeout)
// sets a 1s limit, backdates CreatedAt by 500ms and bounds the call in a 5s
// select — both "500ms of a 1s budget is left" and "the budget restarted, so
// 1s is left" finish well inside 5s, so it cannot tell them apart. Replacing
// `anchor := rec.CreatedAt` with `anchor := time.Now()` in
// steer_launcher.go::steeredTurnRunContext — the session lifetime restarting
// on EVERY wake, so a child woken every few minutes runs for ever — left it
// green.
//
// This one MEASURES. The budget is 6s and 5.5s of it is already spent, so a
// lifetime-anchored deadline fires in about half a second and a
// wake-anchored one would take a full six. The bound is 3s: comfortably
// above the real ~0.5s and comfortably below the 6s the regression needs.
func TestWake_TimeoutBudgetIsTheSessionLifetimeNotThisWake(t *testing.T) {
	const (
		budget       = 20 * time.Second
		alreadySpent = 19500 * time.Millisecond
		// Anything at or above this means the budget restarted on this run.
		// The gap is deliberately wide — ~0.5s for the correct behaviour,
		// ~20s for the regression — so neither verdict can be reached by a
		// loaded machine rather than by the code under test.
		restartedThreshold = 8 * time.Second
	)
	release := make(chan struct{})
	provider := &blockingProvider{release: release}
	f := newSteerFixture(t, provider, false)
	defer close(release)

	childID, childGen := f.launchChild(t, "call-lifetime-budget")
	if err := f.lifecycle.Mutate(childID, func(record *session.LifecycleRecord) error {
		record.SteeredBy.Limits.TimeoutSeconds = int(budget / time.Second)
		record.CreatedAt = time.Now().Add(-alreadySpent)
		return nil
	}); err != nil {
		t.Fatalf("Mutate(child budget): %v", err)
	}

	started := time.Now()
	if _, err := f.launcher.Dispatch(context.Background(), childID, childGen); err != nil {
		t.Fatalf("Dispatch(child): %v", err)
	}
	f.awaitState(t, childID, session.LifecycleTimedOut, budget+4*time.Second)
	elapsed := time.Since(started)

	if elapsed >= restartedThreshold {
		t.Fatalf("the child took %s to time out, but only %s of its %s budget was left when it started — "+
			"the deadline was anchored at THIS run instead of the session's CreatedAt, so the lifetime "+
			"limit restarts on every re-entry and a repeatedly woken child never dies "+
			"(steer_launcher.go::steeredTurnRunContext)",
			elapsed.Round(time.Millisecond), (budget - alreadySpent).Round(time.Millisecond), budget)
	}
	// Positive control: the deadline must not be in the PAST either — a
	// child that times out instantly would also satisfy the bound above
	// while proving the budget is not honoured at all.
	if provider.calls.Load() == 0 {
		t.Fatal("the child never reached the model before timing out — this test cannot distinguish a " +
			"lifetime-anchored deadline from a session that never ran")
	}
}

// TestChildRunToCompletion_NeverBlocksItsParentAndWakesIt is the behavioural
// test ADR-091's headline promise never had.
//
// "No code path blocks a parent turn on a child" was asserted ONLY by a
// source grep for two retired identifiers (`executeSync`,
// `DelegationModeAwait` — tests/adr091::TestADR091_ResidualAudit's
// "Wait-inline gone" row). A grep cannot see a blocking wait reintroduced
// under a new name. The nearest behavioural check ran against a FAKE
// launcher, and TestE2E_CompletionWakesPerChild calls testutil::(*Tree).
// Reenter, which hand-builds a handback and invokes the deliverer directly —
// it never runs a child to completion and never observes a parent woken BY
// one.
//
// This runs both halves for real, through the production launcher, the
// production turn loop and the production upward path:
//
//	half 1 (never blocks): Dispatch must RETURN while the child is still
//	        inside its first, still-blocked model call.
//	half 2 (really wakes): releasing the child must run it to completion,
//	        deliver exactly one upward event, and re-enter the parent —
//	        observed as a model call that can only be the parent's, since
//	        the child makes exactly one and is finished.
func TestChildRunToCompletion_NeverBlocksItsParentAndWakesIt(t *testing.T) {
	release := make(chan struct{})
	provider := &blockingProvider{release: release}
	f := newSteerFixture(t, provider, true)
	var releaseOnce sync.Once
	releaseChild := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseChild()

	childID, childGen := f.launchChild(t, "call-nonblocking")

	// --- half 1: launching a child must not block on it ------------------
	type dispatchOutcome struct {
		result steer.DispatchResult
		err    error
	}
	returned := make(chan dispatchOutcome, 1)
	go func() {
		result, err := f.launcher.Dispatch(context.Background(), childID, childGen)
		returned <- dispatchOutcome{result: result, err: err}
	}()

	var outcome dispatchOutcome
	select {
	case outcome = <-returned:
	case <-time.After(20 * time.Second):
		t.Fatal("Dispatch did not return while the child was still inside its first model call — " +
			"something on the launch path WAITS for the child to finish. That is wait-inline delegation " +
			"back under a new name, which the residual audit's identifier grep cannot see")
	}
	if outcome.err != nil {
		t.Fatalf("Dispatch(child): %v", outcome.err)
	}
	if outcome.result.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(child).State = %q, want running", outcome.result.State)
	}
	// The non-blocking claim is only meaningful if the child really was
	// still working when Dispatch returned.
	record, err := f.lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(child) right after Dispatch: %v", err)
	}
	if record.Terminal() {
		t.Fatalf("the child was already terminal (%q) when Dispatch returned — it never blocked, so this "+
			"test cannot tell a non-blocking launch from a child that finished instantly", record.State)
	}

	// --- half 2: the completing child must wake its parent ---------------
	releaseChild()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if f.upward.countFor(childID) > 0 && provider.calls.Load() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := f.upward.countFor(childID); got != 1 {
		t.Fatalf("upward deliveries for the completed child = %d, want exactly 1 — the child ran to "+
			"completion but its result never reached its parent through the one upward path "+
			"(steer_completion.go::completeSteeredTurn -> Deliver)", got)
	}
	// The child makes exactly ONE model call (its provider answers with
	// plain text, no tools, and its turn ends). Any second call therefore
	// belongs to a session that was re-entered by the child's completion —
	// the parent. Without this, "the parent was told" would be satisfied by
	// an inbox row nobody ever acted on.
	if got := provider.calls.Load(); got < 2 {
		t.Fatalf("the model was called %d times in total — the child's single call and nothing else. The "+
			"parent was never RE-ENTERED by its child's completion: the handback reached its inbox and "+
			"stopped there", got)
	}
	parentTranscript, err := f.al.GetSessionStore().ReadTranscript(f.root)
	if err != nil {
		t.Fatalf("read parent transcript: %v", err)
	}
	raw, err := json.Marshal(parentTranscript)
	if err != nil {
		t.Fatalf("marshal parent transcript: %v", err)
	}
	if !strings.Contains(string(raw), childID) {
		t.Fatalf("the parent's own transcript never mentions the child session %s — the second model call "+
			"was not the parent processing its child's completion", childID)
	}
}

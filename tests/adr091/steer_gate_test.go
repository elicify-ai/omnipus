// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 fix lane RX-TESTS. Properties the suite asserted only by reading
// the code, or by a test that could not tell the right answer from the wrong
// one:
//
//  1. I-2's "Dispatch decides atomically under one lock". Every existing
//     admission test is strictly SEQUENTIAL, so a lock narrowed to exclude
//     the `len(g.active)` read would be invisible to all of them and to
//     -race (no test ever ran two Dispatch calls at once).
//  2. A child run to completion never blocks its parent and wakes it.
//
// (D9's session-lifetime timeout tests were removed with the timeout itself:
// issue #1306 — delegated work has no time limit. The no-limit regression
// tests live in pkg/agent/no_time_limit_test.go.)
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
	// The U5a launch gate resolves the is_default workspace's delegation graph
	// for this fixture's launches; writeGateWorkspace's record is not
	// is_default, so seed the one the gate actually consults.
	seedADR091DelegationGraph(t, home, []string{gateAgentID})

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

	// Dispatch requires the real boot epoch; the launcher mints each run_id.
	boot := session.NewBootEpochStore(home)
	epoch, err := boot.Mint()
	if err != nil {
		t.Fatalf("SETUP Mint genuine boot epoch: %v", err)
	}
	if epoch == 0 || boot.Current() != epoch {
		t.Fatalf("SETUP minted/current boot epoch = %d/%d, require one genuine nonzero epoch", epoch, boot.Current())
	}
	al.SetBootEpochStore(boot)

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

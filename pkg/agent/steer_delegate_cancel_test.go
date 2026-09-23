// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order §0 / I-6, AC-8 — what an AGENT's own
// delegate(action="cancel") has to reach, proved through the REAL wired tool
// on a real *AgentLoop (newSteerAL wires the session-messaging tool surface),
// never through a stub cancel hook.
//
// Two halves of one defect:
//
//  1. A QUEUED worker. The at-limit tool result tells the model, in so many
//     words, to use this action to "drop this queued session"
//     (delegate_run.go::launchAndDispatch). The live-turn interrupt it used
//     to be wired to reached nothing — a queued session has no turn — so the
//     call reported success, the record stayed `queued`, and the worker
//     started the moment a slot freed.
//
//  2. A RUNNING worker's grandchildren. The ScopeSubtree walk finds
//     descendants through parentTurnID links between live turns
//     (steering.go::collectLiveDescendantTurnStates); no steered turn has one
//     (reconstructSteeredTurn builds every child standalone), so the
//     grandchildren kept running. The durable parent-child edge — what the
//     human Stop path already walks — has neither problem.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// parkedProvider blocks every turn it is asked to run until release is
// closed or the turn's own context is cancelled, and announces each entry on
// entered — so a test can hold several steered turns live at once.
type parkedProvider struct {
	entered chan string
	release chan struct{}
}

func (p *parkedProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	select {
	case p.entered <- "in":
	default:
	}
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "finished"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *parkedProvider) GetDefaultModel() string { return "parked-test-provider" }

// installParkedProvider points the harness agent at a provider that parks
// every turn, and returns a function that releases them all exactly once.
func installParkedProvider(t *testing.T, al *AgentLoop) (*parkedProvider, func()) {
	t.Helper()
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test agent is not registered")
	}
	provider := &parkedProvider{entered: make(chan string, 8), release: make(chan struct{})}
	agentInst.Provider = provider
	var once sync.Once
	release := func() { once.Do(func() { close(provider.release) }) }
	t.Cleanup(release)
	return provider, release
}

// delegateToolFor returns the wired delegate tool the harness agent carries.
func delegateToolFor(t *testing.T, al *AgentLoop) *tools.DelegateTool {
	t.Helper()
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test agent is not registered")
	}
	tool, ok := agentInst.Tools.Get("delegate")
	if !ok {
		t.Fatal("the harness agent has no delegate tool registered")
	}
	dt, ok := tool.(*tools.DelegateTool)
	if !ok {
		t.Fatalf("registered delegate tool has type %T, want *tools.DelegateTool", tool)
	}
	return dt
}

// runDelegateCancel calls the real tool's cancel action as the parent would.
func runDelegateCancel(t *testing.T, al *AgentLoop, callerSessionID, targetSessionID string, hard bool) *tools.ToolResult {
	t.Helper()
	ctx := tools.WithTranscriptSessionID(context.Background(), callerSessionID)
	return delegateToolFor(t, al).Execute(ctx, map[string]any{
		"action": "cancel", "session_id": targetSessionID, "hard": hard,
	})
}

// TestDelegateCancel_QueuedSubagentIsDroppedAndNeverStarts proves the promise
// the at-limit result makes to the model: a queued worker can be dropped, it
// leaves the start queue, it never runs, and the tool says so honestly.
func TestDelegateCancel_QueuedSubagentIsDroppedAndNeverStarts(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxParallelAgents = 1
	provider, releaseAll := installParkedProvider(t, al)

	launcher := NewSteerLauncher(al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	busyID, busyGen := launchSteeredChild(t, al, parentID, "call-cancel-busy", "occupies the only slot")
	queuedID, queuedGen := launchSteeredChild(t, al, parentID, "call-cancel-queued", "must never start")

	if busy, err := launcher.Dispatch(context.Background(), busyID, busyGen); err != nil {
		t.Fatalf("Dispatch(busy): %v", err)
	} else if busy.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(busy) = %+v, want State=running", busy)
	}
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the busy child never reached its provider")
	}
	queued, err := launcher.Dispatch(context.Background(), queuedID, queuedGen)
	if err != nil {
		t.Fatalf("Dispatch(queued): %v", err)
	}
	if queued.State != steer.DispatchQueued {
		t.Fatalf("Dispatch(queued) = %+v, want State=queued", queued)
	}

	res := runDelegateCancel(t, al, parentID, queuedID, false)
	if res.IsError {
		t.Fatalf("delegate(cancel) on a queued session = error %q", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "no action needed") {
		t.Errorf("delegate(cancel) answered %q — a success-shaped no-op for a session it left queued and running-to-be", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "queued") || !strings.Contains(res.ForLLM, "never run") {
		t.Errorf("delegate(cancel) answered %q — it must say plainly that a worker which had not started was dropped", res.ForLLM)
	}

	gate := al.steerAdmission()
	if got := gate.queueLen(); got != 0 {
		t.Errorf("start queue length after the cancel = %d, want 0 — the cancelled worker is still waiting to run", got)
	}
	rec, loadErr := al.GetSessionLifecycleStore().Load(queuedID)
	if loadErr != nil {
		t.Fatalf("Load(queued): %v", loadErr)
	}
	if rec.Stop == nil && !rec.Terminal() {
		t.Fatalf("the cancelled queued session is neither stopped nor terminal (state=%q) — nothing stops admission promoting it", rec.State)
	}

	// Free the slot: the FIFO drain now runs, and the cancelled worker must
	// not be what it starts.
	releaseAll()
	if ts := al.getActiveTurnState(busyID); ts != nil {
		select {
		case <-ts.Finished():
		case <-time.After(30 * time.Second):
			t.Fatal("the busy child did not finish after its provider was released")
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ts := al.getActiveTurnState(queuedID); ts != nil {
			t.Fatalf("the cancelled session %s started a turn once a slot freed", queuedID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	after, loadErr := al.GetSessionLifecycleStore().Load(queuedID)
	if loadErr != nil {
		t.Fatalf("Load(queued, after the drain): %v", loadErr)
	}
	if after.State == session.LifecycleRunning {
		t.Fatalf("the cancelled session is `running` after a slot freed — it was promoted anyway")
	}
}

// TestDelegateCancel_RunningSubagentStopsItsGrandchildren proves AC-8 for an
// agent's own cancel: stopping a worker stops the workers IT started.
func TestDelegateCancel_RunningSubagentStopsItsGrandchildren(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	provider, _ := installParkedProvider(t, al)

	launcher := NewSteerLauncher(al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, parentID, "call-cancel-child", "the worker being stopped")
	if _, err := launcher.Dispatch(context.Background(), childID, childGen); err != nil {
		t.Fatalf("Dispatch(child): %v", err)
	}
	grandID, grandGen := launchSteeredChild(t, al, childID, "call-cancel-grandchild", "the worker the worker started")
	if _, err := launcher.Dispatch(context.Background(), grandID, grandGen); err != nil {
		t.Fatalf("Dispatch(grandchild): %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-provider.entered:
		case <-time.After(30 * time.Second):
			t.Fatalf("only %d of the 2 steered turns reached the provider", i)
		}
	}
	grandTS := al.getActiveTurnState(grandID)
	if grandTS == nil {
		t.Fatal("no live turn registered for the grandchild; this test would prove nothing")
	}

	res := runDelegateCancel(t, al, parentID, childID, true)
	if res.IsError {
		t.Fatalf("delegate(cancel, hard) on a running child = error %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "hard-cancelled immediately") {
		t.Errorf("delegate(cancel, hard) answered %q — a worker that WAS stopped must be reported as stopped", res.ForLLM)
	}

	lifecycle := al.GetSessionLifecycleStore()
	grandRec, loadErr := lifecycle.Load(grandID)
	if loadErr != nil {
		t.Fatalf("Load(grandchild): %v", loadErr)
	}
	if grandRec.Stop == nil || grandRec.Stop.Generation != grandRec.Generation {
		t.Fatalf("the grandchild carries no Stop marker for its current generation (stop=%+v, generation=%d) — "+
			"a cancel on its parent never reached it", grandRec.Stop, grandRec.Generation)
	}
	childRec, loadErr := lifecycle.Load(childID)
	if loadErr != nil {
		t.Fatalf("Load(child): %v", loadErr)
	}
	if childRec.Stop == nil {
		t.Fatalf("the named child carries no Stop marker (state=%q)", childRec.State)
	}
	select {
	case <-grandTS.Finished():
	case <-time.After(30 * time.Second):
		t.Fatal("the grandchild's live turn is still running 30s after its parent was hard-cancelled")
	}
}

// TestDelegateCancel_PartialCascadeIsNeverReportedAsCleanSuccess is Finding 3
// (ADR-091 fix lane 2): cancelDelegatedSubtree consulted report.Unreachable
// ONLY when nothing was reached at all, and discarded
// report.SkippedNewerGeneration unconditionally — so reaching one node and
// losing another read as a clean, unqualified success to the model
// (pkg/tools/delegate_run.go's "cooperatively cancelled"/"hard-cancelled
// immediately" wording, driven purely off cerr == nil). A partial cascade
// must surface as an error, exactly as the neighbouring background-shell-kill
// warning does for a lesser resource (delegate_run.go::cancelBackgroundShellWarnings).
func TestDelegateCancel_PartialCascadeIsNeverReportedAsCleanSuccess(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()

	parentID := newTestSteeringSession(t, al, "ws-1")
	reachedID, _ := launchSteeredChild(t, al, parentID, "call-cancel-reached", "reached and stamped")
	// unreachableID is reachedID's OWN child, so it sits inside reachedID's
	// cascade — reachedID itself stamps fine; its own child does not.
	unreachableID, _ := launchSteeredChild(t, al, reachedID, "call-cancel-unreachable", "corrupted on disk before the cascade runs")

	lifecycle := al.GetSessionLifecycleStore()
	// Make the grandchild's record genuinely UNREADABLE (permission denied)
	// so the cascade's own stamp attempt for it fails partway through —
	// reachedID still gets stamped fine. A merely malformed/torn line would
	// self-heal to "not found" (LifecycleStore.tail tolerates a torn write
	// by design) and prove nothing here.
	unreachablePath := filepath.Join(lifecycle.Dir(), unreachableID+".jsonl")
	if err := os.Chmod(unreachablePath, 0o000); err != nil {
		t.Fatalf("make unreachable record unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreachablePath, 0o600) })

	_, err := al.cancelDelegatedSubtree(reachedID, steer.Principal{Kind: steer.PrincipalKindAgent, ID: parentID}, true, "test")
	if err == nil {
		t.Fatal("cancelDelegatedSubtree reported a clean success for a cascade that reached a sibling but corrupted a descendant's own record")
	}

	rec, loadErr := lifecycle.Load(reachedID)
	if loadErr != nil {
		t.Fatalf("Load(reached): %v", loadErr)
	}
	if rec.Stop == nil {
		t.Fatalf("the reachable session was not actually stamped despite the partial failure — the cascade itself, not just the report, regressed")
	}
}

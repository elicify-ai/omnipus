// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order §0 / I-6, AC-8 — what an AGENT's own
// delegate(action="stop_all") has to reach, proved through the REAL wired tool
// on a real *AgentLoop (newSteerAL wires the session-messaging tool surface),
// never through a stub stop hook. (ADR-20261004 renamed the delegate action
// cancel → stop_all with no alias; the cascade itself is unchanged.)
//
// Updated for the founder's one-stop decision (2026-10-05): stop_all is the
// SAME stop a human's /cancel runs (polite first, forced at 3 s), its reply is
// one fixed text, the `hard` argument is gone, and -- ADR-20260928 D2/D4 --
// only the owning execution lands `stopped`, after its running work shut
// down, so landing is asserted as an eventual state, never as part of the
// request's own return.
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

// runDelegateCancel calls the real tool's stop_all action as the parent would.
func runDelegateCancel(t *testing.T, al *AgentLoop, callerSessionID, targetSessionID string) *tools.ToolResult {
	t.Helper()
	ctx := tools.WithTranscriptSessionID(context.Background(), callerSessionID)
	return delegateToolFor(t, al).Execute(ctx, map[string]any{
		"action": "stop_all", "session_id": targetSessionID,
	})
}

// oneStopReply is the single stop_all reply text (founder one-stop decision;
// team-lead ruling).
func oneStopReply(sessionID string) string {
	return "Stop requested for session " + sessionID + " and its helpers; " +
		"they will show as stopped once their running work has shut down."
}

// awaitLandedStopped waits for the OWNING execution to land id `stopped`:
// state stopped, fence cleared, note kept, never terminal (D2/CRIT-001).
func awaitLandedStopped(t *testing.T, al *AgentLoop, id, what string) *session.LifecycleRecord {
	t.Helper()
	waitForGate(t, what+" to land stopped with its note and no fence", func() bool {
		rec, err := al.GetSessionLifecycleStore().Load(id)
		return err == nil && rec.State == session.LifecycleStopped && rec.Stop == nil && rec.StopNote != nil
	})
	rec, err := al.GetSessionLifecycleStore().Load(id)
	if err != nil {
		t.Fatalf("Load(%s): %v", id, err)
	}
	if rec.Terminal() {
		t.Fatalf("%s landed terminal (%q); a stop is never terminal", what, rec.State)
	}
	return rec
}

// TestDelegateCancel_QueuedSubagentIsDroppedAndNeverStarts proves the promise
// the at-limit result makes to the model: a queued worker can be dropped, it
// leaves the start queue, it never runs, and it lands stopped through the
// owning path (D2 table row "working, queued": stopped, not admitted until
// resumed, frees its queue position). The reply is the single stop_all text;
// the retired tool-side "queued ... dropped" wording and writer are gone.
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

	res := runDelegateCancel(t, al, parentID, queuedID)
	if res.IsError {
		t.Fatalf("delegate(stop_all) on a queued session = error %q", res.ForLLM)
	}
	if res.ForLLM != oneStopReply(queuedID) {
		t.Errorf("delegate(stop_all) answered %q, want exactly %q", res.ForLLM, oneStopReply(queuedID))
	}

	// The owner lands the never-ran session; the tool does not.
	awaitLandedStopped(t, al, queuedID, "the stopped queued session")
	if got := al.steerAdmission().queueLen(); got != 0 {
		t.Errorf("start queue length after the stop = %d, want 0 — the stopped worker is still waiting to run", got)
	}

	// Free the slot: the FIFO drain now runs, and the stopped worker must
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
			t.Fatalf("the stopped session %s started a turn once a slot freed", queuedID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	after, loadErr := al.GetSessionLifecycleStore().Load(queuedID)
	if loadErr != nil {
		t.Fatalf("Load(queued, after the drain): %v", loadErr)
	}
	if after.State != session.LifecycleStopped {
		t.Fatalf("the stopped session is %q after a slot freed — it was promoted anyway", after.State)
	}

	// Chat-transcript meta mirror (replaces the retired tool-side queued-drop
	// tests, delegate_meta_mirror_947_test.go): a stopped helper stays
	// coarse-active in sessions/<id>/meta.json (ADR D4/MAJ-009), landed by the
	// owner, with the lifecycle record itself stopped.
	meta, metaErr := al.GetSessionStore().GetMeta(queuedID)
	if metaErr != nil {
		t.Fatalf("GetMeta(queued): %v", metaErr)
	}
	if meta.Status != session.StatusActive {
		t.Errorf("transcript meta status = %q, want %q — a stopped helper stays coarse-active", meta.Status, session.StatusActive)
	}
}

// TestDelegateCancel_RunningSubagentStopsItsGrandchildren proves AC-8 for an
// agent's own stop: stopping a worker stops the workers IT started. Landing is
// pending-until-tail (D2/D4): the request returns once the stop is accepted;
// the named child and its grandchild each land `stopped` (note kept, fence
// cleared) only after their own turns shut down.
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

	res := runDelegateCancel(t, al, parentID, childID)
	if res.IsError {
		t.Fatalf("delegate(stop_all) on a running child = error %q", res.ForLLM)
	}
	if res.ForLLM != oneStopReply(childID) {
		t.Errorf("delegate(stop_all) answered %q, want exactly %q", res.ForLLM, oneStopReply(childID))
	}

	select {
	case <-grandTS.Finished():
	case <-time.After(30 * time.Second):
		t.Fatal("the grandchild's live turn is still running 30s after its parent was stopped")
	}
	childRec := awaitLandedStopped(t, al, childID, "the named child")
	grandRec := awaitLandedStopped(t, al, grandID, "the grandchild")
	if childRec.StopNote.Cause != session.StopCauseStop {
		t.Errorf("named child StopNote.Cause = %q, want %q (it is the direct target)", childRec.StopNote.Cause, session.StopCauseStop)
	}
	if grandRec.StopNote.Cause != session.StopCauseCascade {
		t.Errorf("grandchild StopNote.Cause = %q, want %q (reached through its parent's stop-all)", grandRec.StopNote.Cause, session.StopCauseCascade)
	}
}

// TestDelegateCancel_PartialCascadeIsNeverReportedAsCleanSuccess is Finding 3
// (ADR-091 fix lane 2): the cascade consulted report.Unreachable ONLY when
// nothing was reached at all, and discarded report.SkippedNewerGeneration
// unconditionally — so reaching one node and losing another read as a clean,
// unqualified success to the model. A partial cascade must surface as an
// error naming the session it could not stop, exactly as the neighbouring
// background-shell-kill warning does for a lesser resource
// (delegate_run.go::cancelBackgroundShellWarnings). Driven through the real
// tool (the one stop method), not an internal cascade function.
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

	res := runDelegateCancel(t, al, parentID, reachedID)
	if !res.IsError {
		t.Fatalf("stop_all reported a clean success (%q) for a cascade that reached one session but could not read a descendant's own record", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, unreachableID) {
		t.Errorf("the partial-cascade error %q must name the session that was not stopped (%s)", res.ForLLM, unreachableID)
	}

	rec, loadErr := lifecycle.Load(reachedID)
	if loadErr != nil {
		t.Fatalf("Load(reached): %v", loadErr)
	}
	if rec.StopNote == nil {
		t.Fatalf("the reachable session was not actually stopped despite the partial failure (state=%q, no StopNote) — "+
			"the cascade itself, not just the report, regressed", rec.State)
	}
}

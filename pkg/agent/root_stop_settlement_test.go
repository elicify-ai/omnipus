package agent

// Frozen ADR-20260928 D2/D9/T27; ADR-20261004 decision 1/C1/C6:
// an accepted root Stop must settle its selected execution, not leave a
// permanent in-flight fence. This pack uses actual Launch/Dispatch, the D9
// session Stop adapter, a joined disposal tail and a reopened owning store.
// It never manufactures running state, an execution identity or a stop note.

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Counts actual process-edge model calls; all admissions/identities remain real.
type rootMessageProbe struct {
	gate  *goalRunGate
	calls atomic.Int32
}

func (p *rootMessageProbe) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	p.gate.enteredOnce.Do(func() { close(p.gate.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.gate.release:
		return &providers.LLMResponse{Content: "new human instruction considered"}, nil
	}
}
func (*rootMessageProbe) GetDefaultModel() string { return "root-real-message-probe" }

type rootHumanResult struct {
	err      error
	response string
}

func rootHumanMessage(sessionID string) bus.InboundMessage {
	return bus.InboundMessage{Channel: "webchat", Sender: bus.SenderInfo{CanonicalID: "root-owner"}, ChatID: sessionID,
		Content: "Continue from the saved conversation; do not repeat completed work.", SessionID: sessionID,
		GatewayUserID: "root-owner", UserInitiated: true, Metadata: map[string]string{"agent_id": testDefaultAgentID}}
}

func rootReopenedRecord(t *testing.T, al *AgentLoop, id string) *session.LifecycleRecord {
	t.Helper()
	// The owning store in newSteerAL uses this directory; reopen, never infer
	// durable settlement from a cached live handle or a polled state field.
	reopened := session.NewLifecycleStore(filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle"))
	rec, err := reopened.Load(id)
	if err != nil {
		t.Fatalf("reopen selected root lifecycle: %v", err)
	}
	return rec
}

func rootLaunchWithGoal(t *testing.T, al *AgentLoop) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: testDefaultAgentID, Task: "Keep the root conversation and its goal open",
		Origin: steer.Origin{Kind: steer.OriginKindChat}, Owner: "root-owner",
		Goal: &steer.GoalSpec{Criteria: []steer.Criterion{{Text: "root work complete"}}, DoD: []steer.Criterion{{Text: "root evidence complete"}}},
	})
	if err != nil {
		t.Fatalf("real ordinary-root Launch: %v", err)
	}
	rec := rootReopenedRecord(t, al, res.SessionID)
	if rec.SteeredBy != nil || rec.GoalRef == "" || rec.State != session.LifecycleQueued || rec.ExecutionID != nil {
		t.Fatalf("SETUP: ordinary-root real no-admission shape = %+v", rec)
	}
	return rec
}

func TestStopRoot_AcceptedFenceEventuallyLandsOrReportsError(t *testing.T) {
	for _, mode := range []string{"live_real_admission", "idle_after_real_admission", "never_admitted_delegated_root"} {
		t.Run(mode, func(t *testing.T) { rootStopSettlementCase(t, mode) })
	}
}

func rootStopSettlementCase(t *testing.T, mode string) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	oldGate := newGoalRunGate("root partial work without a completion claim", nil)
	installGoalRunProvider(t, al, oldGate)
	root := rootLaunchWithGoal(t, al)
	var selected *turnState
	if mode != "never_admitted_delegated_root" {
		dispatchChild(t, al, root.SessionID, root.Generation, true)
		awaitGoalProvider(t, oldGate)
		selected = al.getActiveTurnState(root.SessionID)
		root = rootReopenedRecord(t, al, root.SessionID)
		if selected == nil || root.ExecutionID == nil || al.tsExecutionClaim(selected, root.SessionID) != al.executionClaimFor(root) {
			t.Fatal("SETUP: live root lacks a genuinely admitted immutable execution")
		}
		if mode == "idle_after_real_admission" {
			oldGate.open()
			joinGoalFixtureRuns(t, al)
		}
	}
	// Delegation is real even for the never-admitted root. No parent running
	// state or run id is written by this test; its record stays production-owned.
	childRes, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: root.SessionID, TargetAgentID: testDefaultAgentID, Task: "independent helper must not be stopped by root single Stop",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "root-stop-helper"},
	})
	if err != nil {
		t.Fatalf("real root delegation: %v", err)
	}
	childBefore := rootReopenedRecord(t, al, childRes.SessionID)
	goalBefore := mustGoalRecord(t, root.GoalRef)
	fired, armed, stopErr := al.StopSessionTurn(context.Background(), root.SessionID, "root-owner", "webchat")
	if stopErr != nil {
		// Allowed alternative is a real error returned by the caller, not a log.
		if !strings.Contains(strings.ToLower(stopErr.Error()), "stop") {
			t.Fatalf("root Stop error lacks an actionable Stop context: %v", stopErr)
		}
		t.Logf("root Stop failed visibly at real caller: %v", stopErr)
		oldGate.open()
		joinGoalFixtureRuns(t, al)
		return
	}
	if !fired && !armed {
		t.Errorf("real root Stop acknowledged no accepted effect in mode %s", mode)
	}
	if selected != nil && mode == "live_real_admission" {
		select {
		case <-selected.Finished():
		case <-time.After(5 * time.Second):
			t.Fatal("selected live root did not exit after actual Stop")
		}
	}
	// Joins the real dispatched turn AND its disposal/admission tail.
	joinGoalFixtureRuns(t, al)
	landed := rootReopenedRecord(t, al, root.SessionID)
	if landed.State != session.LifecycleStopped || landed.Terminal() || landed.Stop != nil || landed.StopNote == nil || landed.StopNote.Cause != session.StopCauseStop || landed.Generation != root.Generation {
		t.Errorf("accepted root Stop returned nil but reopened selected tail is not settled: state=%q generation=%d fence=%+v note=%+v; mode=%s — want same-generation nonterminal stopped/no active fence or visible caller error", landed.State, landed.Generation, landed.Stop, landed.StopNote, mode)
	}
	if g := mustGoalRecord(t, root.GoalRef); g.State != generated.GoalStateActive || g.GoalID != goalBefore.GoalID || !reflect.DeepEqual(g.Criteria, goalBefore.Criteria) {
		t.Errorf("root Stop lost its active goal: %+v", g)
	}
	childAfter := rootReopenedRecord(t, al, childRes.SessionID)
	if !reflect.DeepEqual(childBefore, childAfter) {
		t.Errorf("single root Stop cascaded into helper: before=%+v after=%+v", childBefore, childAfter)
	}
	rootAssertFreshHumanAdmission(t, al, root)
}

func rootAssertFreshHumanAdmission(t *testing.T, al *AgentLoop, previous *session.LifecycleRecord) {
	t.Helper()
	gate := newGoalRunGate("", nil)
	probe := &rootMessageProbe{gate: gate}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("registered root agent missing")
	}
	inst.Provider = probe
	t.Cleanup(gate.open)
	done := make(chan rootHumanResult, 1)
	go func() {
		response, _, err := al.processMessage(context.Background(), rootHumanMessage(previous.SessionID))
		done <- rootHumanResult{response: response, err: err}
	}()
	select {
	case result := <-done:
		t.Errorf("fresh human message ended without one admission: err=%v response=%q provider_calls=%d", result.err, result.response, probe.calls.Load())
		return
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("fresh human message neither entered a real turn nor returned a visible error")
	}
	hook := al.activeTurnForCancel(previous.SessionID, CancelScope{SessionID: previous.SessionID, TurnOnly: true})
	live, _ := hook.(*turnState)
	fresh := rootReopenedRecord(t, al, previous.SessionID)
	if live == nil {
		t.Error("fresh human message has no registered selected handle")
	} else {
		claim := al.tsExecutionClaim(live, previous.SessionID)
		if !claim.matches(fresh) || claim.BootSeq != al.bootEpochFor() || (previous.ExecutionID != nil && claim.RunID == previous.ExecutionID.RunID) {
			t.Errorf("fresh human admission lacks distinct actual immutable same-boot identity: claim=%+v record=%+v previous=%+v", claim, fresh.ExecutionID, previous.ExecutionID)
		}
	}
	if fresh.Generation != previous.Generation || fresh.Stop != nil || fresh.StopNote != nil || fresh.StopEffect != nil {
		t.Errorf("human resume did not atomically clear only old stop on same generation: %+v", fresh)
	}
	if probe.calls.Load() != 1 {
		t.Errorf("one new human message produced %d calls, want exactly one admitted run", probe.calls.Load())
	}
	gate.open()
	select {
	case result := <-done:
		if result.err != nil {
			t.Errorf("resumed human turn failed: %v", result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fresh human turn did not join")
	}
	joinGoalFixtureRuns(t, al)
}

func TestStopSessionTurn_GoalHelperLandsNonfatalStopped(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	gate := newGoalRunGate("helper partial work", nil)
	installGoalRunProvider(t, al, gate)
	parent := newTestSteeringSession(t, al, "ws-root-positive-helper")
	child := launchGoalBearingChild(t, al, parent, "root-positive-goal-helper", goalChildLaunchOptions{live: true})
	awaitGoalProvider(t, gate)
	ts := al.getActiveTurnState(child.SessionID)
	if ts == nil {
		t.Fatal("SETUP: real helper handle missing")
	}
	fired, armed, err := al.StopSessionTurn(context.Background(), child.SessionID, "root-owner", "webchat")
	if err != nil || !fired || armed {
		t.Fatalf("real helper Stop outcome=%v/%v err=%v", fired, armed, err)
	}
	select {
	case <-ts.Finished():
	case <-time.After(5 * time.Second):
		t.Fatal("selected helper did not exit")
	}
	joinGoalFixtureRuns(t, al)
	landed := rootReopenedRecord(t, al, child.SessionID)
	if landed.State != session.LifecycleStopped || landed.Stop != nil || landed.StopNote == nil || landed.Generation != child.Generation || landed.FinalDelivery != nil {
		t.Errorf("real session Stop failed to land nonterminal helper/no losing final: %+v", landed)
	}
	if g := mustGoalRecord(t, child.GoalRef); g.State != generated.GoalStateActive {
		t.Errorf("single Stop ended helper goal: %+v", g)
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(parent, child.SessionID, "", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("helper's direct-parent notices=%d err=%v, want exactly one nonfatal stopped_child", len(msgs), err)
	}
	notice, err := msgs[0].AsSessionMessageError()
	if err != nil || notice.Fatal || !strings.HasPrefix(notice.Text, "stopped_child:") {
		t.Errorf("helper Stop notice=%+v err=%v, want nonfatal stopped_child", notice, err)
	}
}

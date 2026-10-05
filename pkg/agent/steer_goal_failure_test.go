// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Frozen ADR-20260928 D2/T11 and ADR-20261004 decisions 1–2/C6:
// a genuine provider failure commits failed+outbox before its fatal parent
// report; Stop and lifetime timeout are nonfatal, resumable stopped outcomes.
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type goalChildLaunchOptions struct {
	live           bool
	timeoutSeconds int
}

// launchGoalBearingChild uses real boot, Launch and Dispatch. Default callers
// receive an admitted but idle goal child after the whole dispatch tail joins;
// provider-gated cases explicitly request its live immutable handle instead.
func launchGoalBearingChild(t *testing.T, al *AgentLoop, parentID, callID string, options ...goalChildLaunchOptions) *session.LifecycleRecord {
	t.Helper()
	opts := goalChildLaunchOptions{}
	if len(options) != 0 {
		opts = options[0]
	}
	if al.bootEpochFor() == 0 {
		dir := filepath.Join(al.GetConfig().Agents.Defaults.Home, "goal_fixture_boot")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create real boot directory: %v", err)
		}
		boot := session.NewBootEpochStore(dir)
		epoch, err := boot.Mint()
		if err != nil || epoch == 0 || boot.Current() != epoch {
			t.Fatalf("real boot Mint/Current = %d/%d, err=%v", epoch, boot.Current(), err)
		}
		al.SetBootEpochStore(boot)
	}
	launcher := NewSteerLauncher(al)
	res, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID, Task: "prove the goal",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID}, Limits: steer.Limits{TimeoutSeconds: opts.timeoutSeconds},
		Goal: &steer.GoalSpec{Criteria: []steer.Criterion{{Text: "the work is complete"}}, DoD: []steer.Criterion{{Text: "the evidence is sufficient"}}},
	})
	if err != nil {
		t.Fatalf("Launch(goal child): %v", err)
	}
	dispatched, err := launcher.Dispatch(context.Background(), res.SessionID, res.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(goal child) = %+v, err=%v, want real admission", dispatched, err)
	}
	if !opts.live {
		joinGoalFixtureRuns(t, al)
	}
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(admitted goal child): %v", err)
	}
	if rec.GoalRef == "" || rec.ExecutionID == nil || rec.ExecutionID.RunID == "" || rec.ExecutionID.BootSeq != al.bootEpochFor() {
		t.Fatalf("SETUP: goal reference and real admitted execution required: %+v", rec)
	}
	return rec
}

func joinGoalFixtureRuns(t *testing.T, al *AgentLoop) {
	t.Helper()
	done := make(chan struct{})
	go func() { al.steerAdmission().turns.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatched turn and durable completion tail did not join")
	}
}

// Only the external model provider is controlled; all run/commit identities
// and lifecycle writes come from production admission and completion.
type goalRunGate struct {
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
	answer      string
	err         error
}

func newGoalRunGate(answer string, err error) *goalRunGate {
	return &goalRunGate{entered: make(chan struct{}), release: make(chan struct{}), answer: answer, err: err}
}
func (g *goalRunGate) open() { g.releaseOnce.Do(func() { close(g.release) }) }

type goalRunProvider struct {
	mu    sync.Mutex
	gates []*goalRunGate
	next  int
}

func (p *goalRunProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := p.next
	if index >= len(p.gates) {
		index = len(p.gates) - 1
	}
	if index < 0 {
		p.mu.Unlock()
		return nil, errors.New("goal provider: no external call staged")
	}
	g := p.gates[index]
	p.next++
	p.mu.Unlock()
	g.enteredOnce.Do(func() { close(g.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.release:
		if g.err != nil {
			return nil, g.err
		}
		return &providers.LLMResponse{Content: g.answer}, nil
	}
}
func (*goalRunProvider) GetDefaultModel() string { return "goal-failure-process-edge" }
func installGoalRunProvider(t *testing.T, al *AgentLoop, gates ...*goalRunGate) {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("registered goal child agent missing")
	}
	inst.Provider = &goalRunProvider{gates: gates}
	t.Cleanup(func() {
		for _, g := range gates {
			g.open()
		}
	})
}
func awaitGoalProvider(t *testing.T, g *goalRunGate) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real dispatched goal turn did not reach its external provider")
	}
}

// This normal publication dependency delegates unchanged to the real deliverer.
// The barrier exposes the actual committed bytes BEFORE any parent publication;
// it neither writes an outcome nor substitutes a successful delivery.
type goalCommitPublicationGate struct {
	real    steer.UpwardDeliverer
	event   chan steer.UpwardEvent
	release chan struct{}
	once    sync.Once
}

func (g *goalCommitPublicationGate) Deliver(ctx context.Context, event steer.UpwardEvent) (steer.Delivery, error) {
	g.event <- event
	<-g.release
	return g.real.Deliver(ctx, event)
}
func (g *goalCommitPublicationGate) open() { g.once.Do(func() { close(g.release) }) }
func installGoalCommitGate(t *testing.T, al *AgentLoop) *goalCommitPublicationGate {
	t.Helper()
	real := al.getUpwardDeliverer()
	gate := &goalCommitPublicationGate{real: real, event: make(chan steer.UpwardEvent, 8), release: make(chan struct{})}
	al.SetSteerAudienceDeps(al.getSteerAudienceResolver(), steer.NopBoundaryObserver{}, gate)
	t.Cleanup(gate.open)
	return gate
}

func assertGoalFailureCommitBeforePublication(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord, gate *goalCommitPublicationGate) {
	t.Helper()
	var event steer.UpwardEvent
	select {
	case event = <-gate.event:
	case <-time.After(5 * time.Second):
		t.Fatal("provider failure never reached the real committed-final publication boundary")
	}
	got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(before publication): %v", err)
	}
	wantID := fmt.Sprintf("%s:%d:final", rec.SessionID, rec.Generation)
	if got.State != session.LifecycleFailed || got.FinalDelivery == nil {
		t.Fatalf("before parent publication: state=%q outbox=%+v, want failed+committed outbox (D2/T11)", got.State, got.FinalDelivery)
	}
	out := got.FinalDelivery
	raw, err := event.Message.MarshalJSON()
	if err != nil {
		t.Fatalf("encode real final: %v", err)
	}
	if out.CommitID != rec.ExecutionID.RunID || out.Generation != rec.Generation || out.MessageID != wantID || out.ParentSessionID != rec.SteeringSessionID() || out.Outcome != string(steer.OutcomeFailed) || !bytes.Equal(out.Payload, raw) {
		t.Fatalf("committed final tuple/payload does not equal producing identity and exact upward event: %+v", out)
	}
	if event.ChildSessionID != rec.SessionID || event.Generation != rec.Generation || event.Outcome != steer.OutcomeFailed {
		t.Fatalf("upward event identity/outcome = %+v, want selected provider failure", event)
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(rec.SteeringSessionID(), rec.SessionID, "", 10)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("publication barrier parent messages=%d err=%v, want none before real delivery", len(msgs), err)
	}
	gate.open()
}

// The historical symbol is retained so the failure map remains traceable;
// its old fatal Stop/timeout oracle is superseded, not weakened (ADR C6).
func TestGoalDelegation_DeadChildReportsUpwardAndLandsTerminal(t *testing.T) {
	cases := []struct {
		name        string
		providerErr error
		cause       session.StopCause
		fatal       bool
		state       session.LifecycleState
	}{
		{name: "provider_failure_commits_fatal_outbox_then_one_parent_message", providerErr: errors.New("downstream provider failure"), fatal: true, state: session.LifecycleFailed},
		{name: "lifetime_timeout_is_nonfatal_stopped_and_resumable", cause: session.StopCauseTimeout, state: session.LifecycleStopped},
		{name: "single_stop_is_nonfatal_stopped_and_resumable", cause: session.StopCauseStop, state: session.LifecycleStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMNIPUS_HOME", t.TempDir())
			al, _ := newSteerAL(t)
			wireSteerCompletionDeps(t, al)
			providerGate := newGoalRunGate("no completion claim", tc.providerErr)
			installGoalRunProvider(t, al, providerGate)
			var publication *goalCommitPublicationGate
			if tc.fatal {
				publication = installGoalCommitGate(t, al)
			}
			parentID := newTestSteeringSession(t, al, "ws-goal-failure")
			opts := goalChildLaunchOptions{live: true}
			if tc.cause == session.StopCauseTimeout {
				opts.timeoutSeconds = 1
			}
			rec := launchGoalBearingChild(t, al, parentID, "call-goal-outcome", opts)
			beforeGoal := mustGoalRecord(t, rec.GoalRef)
			if beforeGoal.State != generated.GoalStateActive {
				t.Fatalf("SETUP: goal state=%q, want active", beforeGoal.State)
			}
			awaitGoalProvider(t, providerGate)
			ts := al.getActiveTurnState(rec.SessionID)
			if ts == nil || al.tsExecutionClaim(ts, rec.SessionID) != al.executionClaimFor(rec) {
				t.Fatal("SETUP: real producing turn does not match admitted record identity")
			}
			if tc.fatal {
				providerGate.open()
				assertGoalFailureCommitBeforePublication(t, al, rec, publication)
			} else if tc.cause == session.StopCauseStop {
				report, err := al.steerCanceller().StopTurns(context.Background(), rec.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "goal-owner"}, false, al.SteerGenerationCancel)
				if err != nil || len(report.Unreachable) != 0 || !reflect.DeepEqual(report.Reached, []string{rec.SessionID}) {
					t.Fatalf("real Stop report=%+v err=%v, want own child reached without error", report, err)
				}
			}
			// Timeout is the real lifetime context's expiry, not a fabricated runErr.
			joinGoalFixtureRuns(t, al)
			got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
			if err != nil {
				t.Fatalf("Load(disposed goal child): %v", err)
			}
			if got.State != tc.state || got.Generation != rec.Generation || got.Terminal() != tc.fatal {
				t.Fatalf("goal child outcome=%q gen=%d terminal=%v, want %q gen=%d terminal=%v", got.State, got.Generation, got.Terminal(), tc.state, rec.Generation, tc.fatal)
			}
			if got.Stop != nil {
				t.Fatalf("landed outcome retains active Stop fence: %+v", got.Stop)
			}
			if tc.fatal {
				if got.StopNote != nil || got.FinalDelivery == nil {
					t.Fatalf("provider failure note/outbox = %+v/%+v, want no stop note and committed final", got.StopNote, got.FinalDelivery)
				}
			} else {
				if got.StopNote == nil || got.StopNote.Cause != tc.cause || got.FinalDelivery != nil {
					t.Fatalf("nonfatal stop note/outbox = %+v/%+v, want cause=%q and no final (D2/T11)", got.StopNote, got.FinalDelivery, tc.cause)
				}
			}
			msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, rec.SessionID, "", 10)
			if err != nil || len(msgs) != 1 {
				t.Fatalf("direct-parent messages=%d err=%v, want exactly one", len(msgs), err)
			}
			e, err := msgs[0].AsSessionMessageError()
			if err != nil {
				t.Fatalf("parent message must be error envelope: %v", err)
			}
			if e.Fatal != tc.fatal {
				t.Errorf("parent fatal=%v, want %v (Stop/timeout are not provider failures)", e.Fatal, tc.fatal)
			}
			if tc.fatal {
				if !strings.HasPrefix(e.Text, "failed:") || !strings.Contains(e.Text, tc.providerErr.Error()) {
					t.Errorf("provider failure text=%q, want failed: plus actual provider error", e.Text)
				}
				raw, err := msgs[0].MarshalJSON()
				if err != nil || !bytes.Equal(raw, got.FinalDelivery.Payload) {
					t.Errorf("parent payload differs from committed outbox: err=%v", err)
				}
			} else if !strings.HasPrefix(e.Text, "stopped_child:") || !strings.Contains(e.Text, "cause: "+string(tc.cause)) {
				t.Errorf("nonfatal notice=%q, want stopped_child: cause: %s", e.Text, tc.cause)
			}
			select {
			case wake := <-al.bus.InboundChan():
				if wake.AsyncTranscriptSessionID != parentID {
					t.Errorf("wake session=%q, want direct parent %q", wake.AsyncTranscriptSessionID, parentID)
				}
			default:
				t.Error("disposed child did not wake its direct parent")
			}
			select {
			case extra := <-al.bus.InboundChan():
				t.Errorf("extra wake after joined completion: %+v", extra)
			default:
			}
			afterGoal := mustGoalRecord(t, rec.GoalRef)
			if afterGoal.State != generated.GoalStateActive || afterGoal.GoalID != beforeGoal.GoalID || got.GoalRef != rec.GoalRef || !reflect.DeepEqual(afterGoal.Criteria, beforeGoal.Criteria) || !reflect.DeepEqual(afterGoal.DoD, beforeGoal.DoD) {
				t.Fatalf("outcome ended/replaced the active goal or its criteria: before=%+v after=%+v", beforeGoal, afterGoal)
			}
			blocked, err := al.hasRunningOrQueuedDescendant(parentID)
			if err != nil || blocked {
				t.Fatalf("joined outcome leaves parent blocked=%v err=%v, want false,nil", blocked, err)
			}
			if !tc.fatal {
				assertGoalChildRealResume(t, al, rec)
			}
		})
	}
}

func assertGoalChildRealResume(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord) {
	t.Helper()
	gen, err := al.steerCanceller().Revive(context.Background(), rec.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "goal-owner"})
	if err != nil || gen != rec.Generation {
		t.Fatalf("real RESUME gen=%d err=%v, want same generation %d", gen, err, rec.Generation)
	}
	queued, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil || queued.State != session.LifecycleQueued || queued.Stop != nil || queued.StopNote != nil || queued.StopEffect != nil {
		t.Fatalf("atomic resume did not clear note/fence/effect into queued: %+v err=%v", queued, err)
	}
	resumeGate := newGoalRunGate("continue without repeating work", nil)
	installGoalRunProvider(t, al, resumeGate)
	dispatchChild(t, al, rec.SessionID, gen, true)
	awaitGoalProvider(t, resumeGate)
	fresh, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
	if err != nil || fresh.ExecutionID == nil || fresh.ExecutionID.RunID == rec.ExecutionID.RunID || fresh.ExecutionID.BootSeq != rec.ExecutionID.BootSeq {
		t.Fatalf("real resumed identity=%+v err=%v, want fresh run in same boot", fresh, err)
	}
	resumeGate.open()
	joinGoalFixtureRuns(t, al)
	if g := mustGoalRecord(t, rec.GoalRef); g.State != generated.GoalStateActive {
		t.Fatalf("resume lost active goal: %+v", g)
	}
}

// TestGoalDelegation_StopNoteCauseRequiredByControlPlane was a
// t.Fatal("BLOCKED: ...") stub (ADR-20260928-sub-agent-control-plane
// Vocabulary lines 133/137, D2 line ~209) because pkg/session's separate
// persisted stop_note.cause field did not exist in production. It now does
// (commit 57c1a20ba, pkg/session/lifecycle_edge.go::StopNote/StopCause), so
// this proves the field is populated CORRECTLY by the actual I-6
// control-plane write path (steer_cancel.go::SteerCanceller.CancelSubtree ->
// stampStop/cascade) for a goal-bearing subtree — not the
// steer_completion.go synthesis path the table test above already covers —
// per the commit's own split: "stop for the direct target, cascade for
// swept descendants".
func TestGoalDelegation_StopNoteCauseRequiredByControlPlane(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	root := testSteerLifecycleRecord("goal-root", "", session.LifecycleRunning, 1)
	root.GoalRef = "goal-root-ref"
	persistSteerLifecycle(t, store, root)
	child := testSteerLifecycleRecord("goal-child", "goal-root", session.LifecycleRunning, 1)
	child.GoalRef = "goal-child-ref"
	persistSteerLifecycle(t, store, child)

	canceller := NewSteerCanceller(store)
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"}
	if _, err := canceller.CancelSubtree(context.Background(), "goal-root", by); err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}

	gotRoot, err := store.Load("goal-root")
	if err != nil {
		t.Fatalf("load goal-root: %v", err)
	}
	if gotRoot.StopNote == nil {
		t.Fatalf("goal-root stop_note = nil, want a persisted note (D2/CRIT-001, ADR-20260928-sub-agent-control-plane line ~209)")
	}
	if gotRoot.StopNote.Cause != session.StopCauseStop {
		t.Errorf("goal-root stop_note.cause = %q, want %q — the direct target of the Stop call (steer_cancel.go::cascade's process([]string{sessionID}, StopCauseStop))",
			gotRoot.StopNote.Cause, session.StopCauseStop)
	}
	if wantBy := session.StopActorFromPrincipal(by); gotRoot.StopNote.By != wantBy {
		t.Errorf("goal-root stop_note.by = %q, want %q", gotRoot.StopNote.By, wantBy)
	}

	gotChild, err := store.Load("goal-child")
	if err != nil {
		t.Fatalf("load goal-child: %v", err)
	}
	if gotChild.StopNote == nil {
		t.Fatalf("goal-child stop_note = nil, want a persisted note (D2/CRIT-001, ADR-20260928-sub-agent-control-plane line ~209)")
	}
	if gotChild.StopNote.Cause != session.StopCauseCascade {
		t.Errorf("goal-child stop_note.cause = %q, want %q — swept in only because its ancestor was stopped (steer_cancel.go::cascade's process(first, StopCauseCascade); ADR D7 line 397: \"cause cascade\")",
			gotChild.StopNote.Cause, session.StopCauseCascade)
	}
}

// A waiting parent must re-enter on its child's failure wake and produce its
// OWN handback. No stale prior parent response may be copied to the root.
func TestGoalDelegation_DeadChildUnblocksTheWaitingParent(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-goal-parent")
	parentGate := newGoalRunGate("stale parent answer must not reach root", nil)
	childGate := newGoalRunGate("", errors.New("downstream provider failure"))
	installGoalRunProvider(t, al, parentGate, childGate)
	parentID, parentGen := launchParkedChild(t, al, rootID, "call-real-waiting-parent", "wait for the delegated worker")
	dispatchChild(t, al, parentID, parentGen, true)
	awaitGoalProvider(t, parentGate)
	child := launchGoalBearingChild(t, al, parentID, "call-real-goal-worker", goalChildLaunchOptions{live: true})
	if child.SteeringSessionID() != parentID {
		t.Fatal("SETUP: launched goal child's direct parent was not retained")
	}
	awaitGoalProvider(t, childGate)
	parentTS := al.getActiveTurnState(parentID)
	if parentTS == nil {
		t.Fatal("SETUP: real parent turn missing")
	}
	parentGate.open()
	select {
	case <-parentTS.Finished():
	case <-time.After(5 * time.Second):
		t.Fatal("parent's initial turn did not exit")
	}
	// Slot release is the last deferred dispatch action, after the parent
	// completion tail; waiting on it is not merely waiting on a state field.
	waitForGate(t, "the parent's initial completion tail to release its slot", func() bool { return !al.steerAdmission().hasReservation(parentID, parentGen) })
	before, err := al.GetSessionLifecycleStore().Load(parentID)
	if err != nil || before.State != session.LifecycleRunning {
		t.Fatalf("waiting parent=%+v err=%v, want still running while child works", before, err)
	}
	rootMsgs, _, _, err := al.GetMessageInboxStore().Drain(rootID, parentID, "", 10)
	if err != nil || len(rootMsgs) != 0 {
		t.Fatalf("stale parent final reached root before re-entry: messages=%d err=%v", len(rootMsgs), err)
	}
	childGate.open()
	joinGoalFixtureRuns(t, al)
	var wake bus.InboundMessage
	select {
	case wake = <-al.bus.InboundChan():
	case <-time.After(5 * time.Second):
		t.Fatal("real failed goal child never woke its direct waiting parent")
	}
	if wake.AsyncTranscriptSessionID != parentID {
		t.Fatalf("child wake targets %q, want direct parent %q", wake.AsyncTranscriptSessionID, parentID)
	}
	before, err = al.GetSessionLifecycleStore().Load(parentID)
	if err != nil || before.State != session.LifecycleRunning {
		t.Fatalf("parent completed before consuming its wake: %+v err=%v", before, err)
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("registered parent agent missing")
	}
	inst.Provider = &depthEchoProvider{}
	if _, err := al.processSystemMessage(context.Background(), wake); err != nil {
		t.Fatalf("real parent wake re-entry: %v", err)
	}
	joinGoalFixtureRuns(t, al)
	parent, err := al.GetSessionLifecycleStore().Load(parentID)
	if err != nil || parent.State != session.LifecycleCompleted {
		t.Fatalf("parent after genuine wake turn=%+v err=%v, want completed", parent, err)
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(rootID, parentID, "", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("root handbacks=%d err=%v, want exactly one from the re-entered parent", len(msgs), err)
	}
	handback, err := msgs[0].AsSessionMessageHandback()
	if err != nil {
		t.Fatalf("root handback: %v", err)
	}
	if !strings.Contains(handback.ResultSoFar, "failed:") || !strings.Contains(handback.ResultSoFar, "downstream provider failure") || strings.Contains(handback.ResultSoFar, "stale parent answer must not reach root") {
		t.Errorf("root received %q, want real re-entry's child failure, never stale parent text", handback.ResultSoFar)
	}
}

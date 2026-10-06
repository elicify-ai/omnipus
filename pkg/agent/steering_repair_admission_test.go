// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// admitSteeringRepairExecution repairs only a fixture's admission boundary.
// Frozen control-plane ADR D2, Execution identity and effect-boundary checks:
// the runtime must persist the identity before admission, and the real queue
// entry/live handle must retain it. Do not fabricate ExecutionID in a fixture.
// The model boundary is held while the test drives the completion under test;
// cleanup releases it before newSteerAL closes and joins its execution tail.
func admitSteeringRepairExecution(t *testing.T, al *AgentLoop, id string, gen int) *session.LifecycleRecord {
	t.Helper()
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default test agent is not registered")
	}
	originalProvider := agentInst.Provider
	provider, release := installParkedProvider(t, al)
	defer func() { agentInst.Provider = originalProvider }()
	result, err := NewSteerLauncher(al).Dispatch(context.Background(), id, gen)
	if err != nil {
		t.Fatalf("SETUP: real Dispatch(%s): %v", id, err)
	}
	if result.State != steer.DispatchRunning || result.Generation != gen {
		t.Fatalf("SETUP: real Dispatch(%s) = %+v, want running generation %d", id, result, gen)
	}
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		release()
		t.Fatal("SETUP: admitted execution did not reach the held model boundary")
	}
	rec, err := al.GetSessionLifecycleStore().Load(id)
	if err != nil {
		t.Fatalf("SETUP: Load(admitted execution): %v", err)
	}
	ts := al.getActiveTurnState(id)
	claim := al.tsExecutionClaim(ts, id)
	if rec.ExecutionID == nil || !claim.matches(rec) || !al.steerAdmission().hasExecutionReservation(claim) {
		t.Fatalf("SETUP: real admission did not retain its persisted execution identity: record=%+v claim=%+v", rec.ExecutionID, claim)
	}
	return rec
}

// awaitSteeringRepairStopped waits for the owning execution's actual landing,
// not merely a current Stop fence. D2/Vocabulary require stopped + lasting note
// with no current fence. The 30s wait is the existing steering fixture bound.
func awaitSteeringRepairStopped(t *testing.T, al *AgentLoop, id string, gen int) *session.LifecycleRecord {
	t.Helper()
	deadline := time.After(30 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		rec, err := al.GetSessionLifecycleStore().Load(id)
		if err != nil {
			t.Fatalf("Load(stopping execution): %v", err)
		}
		if rec.State == session.LifecycleStopped {
			if rec.Generation != gen || rec.Stop != nil || rec.StopNote == nil {
				t.Fatalf("landed stop = generation %d fence=%+v note=%+v, want same generation %d, no fence and durable note (D2/Vocabulary)", rec.Generation, rec.Stop, rec.StopNote, gen)
			}
			return rec
		}
		select {
		case <-deadline:
			t.Fatalf("Stop did not land: state=%q generation=%d; a current fence alone is not a stopped landing", rec.State, rec.Generation)
		case <-tick.C:
		}
	}
}

// awaitSteeringRepairStopTail joins the real dispatched owner's full output
// tail. A lifecycle landing precedes its D6 publication; observing stopped
// alone is not evidence the publisher has finished (false-green patterns §7).
// Call only where this fixture has no other held execution.
func awaitSteeringRepairStopTail(t *testing.T, al *AgentLoop) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		al.steerAdmission().turns.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("stopped execution's notice/output tail did not finish")
	}
}

// assertSteeringRepairStopNotice pins D6's separate direct-parent notice.
// D2/T11 forbid a stopped landing's legacy fatal event/final id/outbox;
// the parent must still learn cause/actor/time through one durable D6 notice.
func assertSteeringRepairStopNotice(t *testing.T, al *AgentLoop, parentID string, stopped *session.LifecycleRecord) {
	t.Helper()
	if stopped.FinalDelivery != nil || stopped.FailedReason != "" {
		t.Fatalf("stopped landing carries terminal-failure publication: final=%+v reason=%q", stopped.FinalDelivery, stopped.FailedReason)
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, stopped.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent stopped-child notice): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("direct-parent message count = %d, want exactly one D6 notice and no losing final", len(msgs))
	}
	msg, err := msgs[0].AsSessionMessageError()
	if err != nil {
		t.Fatalf("stopped landing published a non-notice message: %v", err)
	}
	wantID := fmt.Sprintf("stopped-notice:%s:%s:%d:%d", parentID, stopped.SessionID, stopped.Generation, stopped.StopNote.Seq)
	if msg.MessageId != wantID || msg.SessionId != stopped.SessionID || msg.ParentSessionId == nil || *msg.ParentSessionId != parentID || msg.Generation == nil || *msg.Generation != stopped.Generation || msg.Fatal {
		t.Fatalf("D6 notice identity/fatal flag = %+v, want id=%q for its direct parent, generation %d and non-fatal", msg, wantID, stopped.Generation)
	}
	for _, want := range []string{string(stopped.StopNote.Cause), stopped.StopNote.By, stopped.StopNote.At.UTC().Format(time.RFC3339Nano), "resume", "redirect"} {
		if !strings.Contains(strings.ToLower(msg.Text), strings.ToLower(want)) {
			t.Errorf("D6 notice text %q does not identify %q", msg.Text, want)
		}
	}
}

// TestReportSteeredSessionTerminalUpward_StopLandingPublishesOnlyD6Notice
// independently covers the stop arm without removing the genuine failure
// scenarios in steer_cancel_test.go. Frozen D2 CRIT-001, T11 and D6.
func TestReportSteeredSessionTerminalUpward_StopLandingPublishesOnlyD6Notice(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parentID := newTestSteeringSession(t, al, "ws-1")
	id, gen := launchSteeredChild(t, al, parentID, "repair-stop-no-final", "stop this execution")
	admitSteeringRepairExecution(t, al, id, gen)
	deliverer := &recordingUpwardDeliverer{err: fmt.Errorf("legacy final publication must not run")}
	wireTerminalReportDeliverer(al, deliverer)
	result, err := al.StopSession(context.Background(), StopRequest{
		SessionID: id, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"},
	})
	if err != nil || result.RootErr != nil || len(result.Report.Unreachable) != 0 {
		t.Fatalf("StopSession = %+v, %v, want a visible successful stop", result, err)
	}
	stopped := awaitSteeringRepairStopped(t, al, id, gen)
	awaitSteeringRepairStopTail(t, al)
	if deliverer.calls() != 0 {
		t.Fatalf("legacy UpwardDeliverer called %d times for STOPPED, want zero (D2 CRIT-001/T11)", deliverer.calls())
	}
	assertSteeringRepairStopNotice(t, al, parentID, stopped)
	if _, err := al.deliverLandedStopNotices(context.Background(), stopped); err != nil {
		t.Fatalf("retry D6 notice delivery: %v", err)
	}
	assertSteeringRepairStopNotice(t, al, parentID, stopped)
}

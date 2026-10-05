package agent

// D2's accepted Stop must land or expose its owning writer failure at the real
// caller. A healthy control ledger/read is not proof the lifecycle append worked.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestSelectedStop_RealOwningAppendFailureReachesCaller(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprintf("owning_append_fault_%v", fault), func(t *testing.T) { selectedStopAppendCase(t, fault) })
	}
}
func selectedStopAppendCase(t *testing.T, fault bool) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 1
	blocker := newGoalRunGate("stop append blocker final", nil)
	installGoalRunProvider(t, al, blocker)
	parentID := newTestSteeringSession(t, al, "ws-selected-append-fault")
	u2LaunchLive(t, al, parentID, "call-stop-append-blocker", blocker)
	launcher := NewSteerLauncher(al)
	launch, launchErr := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID, Task: "queued goal child owning stop append",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-owning-stop-append"},
		Goal:   &steer.GoalSpec{Criteria: []steer.Criterion{{Text: "work done"}}, DoD: []steer.Criterion{{Text: "evidence done"}}},
	})
	if launchErr != nil {
		t.Fatalf("real queued goal Launch: %v", launchErr)
	}
	dispatchChild(t, al, launch.SessionID, launch.Generation, false)
	selected := rootReopenedRecord(t, al, launch.SessionID)
	if selected.ExecutionID == nil || selected.ExecutionID.RunID == "" || selected.State != session.LifecycleQueued {
		t.Fatal("SETUP: queued admission lacks genuine selected run")
	}
	lifecycle := al.GetSessionLifecycleStore()
	journal := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", selected.SessionID+".jsonl")
	controls := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", "controls", selected.SessionID+".jsonl")
	var accepted *session.LifecycleRecord
	var effectInvoked bool
	callback := func(ctx context.Context, id string, generation int) (GenerationCancelResult, error) {
		snapshot, loadErr := lifecycle.Load(id)
		if loadErr != nil {
			return GenerationCancelResult{}, loadErr
		}
		accepted = snapshot
		if snapshot.Stop == nil || snapshot.Stop.Generation != generation || snapshot.StopEffect == nil || snapshot.StopEffect.ControlID == "" || snapshot.StopEffect.Target.RunID != selected.ExecutionID.RunID || snapshot.StopEffect.Target.BootSeq != selected.ExecutionID.BootSeq {
			return GenerationCancelResult{}, fmt.Errorf("real accepted selected fence/control identity missing: %+v", snapshot)
		}
		effects, controlsErr := lifecycle.AcceptedStopEffects(id)
		if controlsErr != nil {
			return GenerationCancelResult{}, fmt.Errorf("read accepted control ledger: %w", controlsErr)
		}
		if len(effects) != 1 || !reflect.DeepEqual(effects[0], *snapshot.StopEffect) {
			return GenerationCancelResult{}, fmt.Errorf("accepted control ledger is not independently healthy: %+v", effects)
		}
		// The real controls writer can open its existing file; no fabricated
		// control, receipt or history line is written by this instrument.
		controlFile, controlOpenErr := os.OpenFile(controls, os.O_RDWR|os.O_APPEND, 0o600)
		if controlOpenErr != nil {
			return GenerationCancelResult{}, controlOpenErr
		}
		if controlCloseErr := controlFile.Close(); controlCloseErr != nil {
			return GenerationCancelResult{}, controlCloseErr
		}
		if fault {
			denySelectedStopAppend(t, journal)
		}
		healthy, readErr := lifecycle.Load(id)
		if readErr != nil {
			return GenerationCancelResult{}, fmt.Errorf("owning Load under append-only fault: %w", readErr)
		}
		if healthy.StopEffect == nil || !reflect.DeepEqual(healthy.StopEffect, snapshot.StopEffect) {
			return GenerationCancelResult{}, fmt.Errorf("owning Load moved under append-only fault: %+v", healthy)
		}
		effectInvoked = true
		return al.SteerGenerationCancel(ctx, id, generation)
	}
	report, stopErr := al.steerCanceller().StopTurns(context.Background(), selected.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "append-owner"}, false, callback)
	if accepted == nil || !effectInvoked {
		t.Fatalf("instrument did not reach the real post-acceptance effect cut: report=%+v err=%v", report, stopErr)
	}
	if !reflect.DeepEqual(report.Reached, []string{selected.SessionID}) {
		t.Errorf("real caller reached=%v,want own selected child", report.Reached)
	}
	after := rootReopenedRecord(t, al, selected.SessionID)
	if al.steerAdmission().queueLen() != 0 {
		t.Errorf("selected actual queued admission was not removed: queue=%d", al.steerAdmission().queueLen())
	}
	if g := mustGoalRecord(t, selected.GoalRef); g.State != generated.GoalStateActive {
		t.Errorf("owning Stop fault/control ended goal: %+v", g)
	}
	if fault {
		if stopErr == nil && (len(report.Unreachable) != 1 || report.Unreachable[0].ID != selected.SessionID || strings.TrimSpace(report.Unreachable[0].Reason) == "") {
			t.Errorf("owning stopped append was genuinely denied but actual StopTurns returned clean/no visible failure: report=%+v err=%v (Warn+nil is not error propagation)", report, stopErr)
		}
		if stopErr != nil && strings.TrimSpace(stopErr.Error()) == "" {
			t.Error("owning writer error returned no visible reason")
		}
		if after.State != session.LifecycleQueued || after.Stop == nil || after.StopEffect == nil || !reflect.DeepEqual(after.StopEffect, accepted.StopEffect) || after.FinalDelivery != nil {
			t.Errorf("failed append falsely landed/replaced the accepted recoverable fence: %+v", after)
		}
	} else {
		if stopErr != nil || len(report.Unreachable) != 0 || after.State != session.LifecycleStopped || after.Stop != nil || after.StopNote == nil || after.FinalDelivery != nil {
			t.Fatalf("healthy owning writer did not land selected nonterminal stop: %+v report=%+v err=%v", after, report, stopErr)
		}
	}
	messages, _, _, inboxErr := al.GetMessageInboxStore().Drain(parentID, selected.SessionID, "", 10)
	if inboxErr != nil {
		t.Fatalf("direct parent inbox: %v", inboxErr)
	}
	if fault && len(messages) != 0 {
		t.Errorf("failed owning stop append published %d false landed notices", len(messages))
	}
	if !fault {
		if len(messages) != 1 {
			t.Fatalf("healthy stopped writer notices=%d,want exactly1", len(messages))
		}
		notice, decodeErr := messages[0].AsSessionMessageError()
		if decodeErr != nil || notice.Fatal || !strings.HasPrefix(notice.Text, "stopped_child:") {
			t.Errorf("healthy stopped writer notice=%+v err=%v", notice, decodeErr)
		}
	}
	if fault {
		if restoreErr := os.Chmod(journal, 0o600); restoreErr != nil {
			t.Fatalf("repair real stopped writer: %v", restoreErr)
		}
	}
	blocker.open()
	joinGoalFixtureRuns(t, al)
}

func denySelectedStopAppend(t *testing.T, journal string) {
	t.Helper()
	if chmodErr := os.Chmod(journal, 0o400); chmodErr != nil {
		t.Fatalf("fault real owning append: %v", chmodErr)
	}
	t.Cleanup(func() {
		if restoreErr := os.Chmod(journal, 0o600); restoreErr != nil {
			t.Errorf("restore owning append permissions: %v", restoreErr)
		}
	})
	// Exact real AppendJSONL open mode: reads stay allowed, write-open is denied.
	f, openErr := os.OpenFile(journal, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if openErr == nil {
		if closeErr := f.Close(); closeErr != nil {
			t.Errorf("close faulty instrument handle: %v", closeErr)
		}
		t.Fatal("BLOCKED: environment bypasses owning append denial; instrument cannot prove a real Stop save failure")
	}
	if !errors.Is(openErr, os.ErrPermission) {
		t.Fatalf("owning append instrument error=%v,want permission refusal", openErr)
	}
}

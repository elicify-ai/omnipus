package agent

// D2/T26/T27 + October4 C1/C3: only CURRENT stopped state projects to the
// parent's display. An untaken historical stopped_child notice may ring after
// same-generation resume, but must not publish stopped over the newer run.
// All stops, identities, history, frames and stores below are production-owned.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type stopDisplayTap struct {
	mu     sync.Mutex
	events []Event
}

func observeStopDisplay(t *testing.T, al *AgentLoop) *stopDisplayTap {
	t.Helper()
	tap := &stopDisplayTap{}
	al.SetEventSyncTap(func(event Event) {
		if event.Kind != EventKindSubagentState && event.Kind != EventKindSubTurnEnd {
			return
		}
		tap.mu.Lock()
		tap.events = append(tap.events, event)
		tap.mu.Unlock()
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	return tap
}
func (tap *stopDisplayTap) snapshot() []Event {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	return append([]Event(nil), tap.events...)
}

type stopDisplayFrames struct {
	states []generated.SubagentStateFrame
	ends   []generated.SubagentEndFrame
}

func reopenedStopDisplay(t *testing.T, al *AgentLoop, parentID, childID, spanID string) stopDisplayFrames {
	t.Helper()
	store, openErr := session.NewUnifiedStore(al.GetSessionStore().BaseDir())
	if openErr != nil {
		t.Fatalf("reopen actual parent transcript store: %v", openErr)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close reopened parent store: %v", closeErr)
		}
	})
	entries, readErr := store.ReadTranscript(parentID)
	if readErr != nil {
		t.Fatalf("read actual reopened replay input: %v", readErr)
	}
	var frames stopDisplayFrames
	for _, entry := range entries {
		if entry.SubagentState != nil && entry.SubagentState.ChildSessionId != nil && *entry.SubagentState.ChildSessionId == childID {
			frame := *entry.SubagentState
			if frame.Type != string(generated.WsFrameTypeSubagentState) || frame.SessionId != parentID || frame.SpanId != spanID {
				t.Errorf("persisted current-state frame wrong identity/route: %+v", frame)
			}
			frames.states = append(frames.states, frame)
		}
		if entry.SubagentEnd != nil && entry.SubagentEnd.SpanId == spanID {
			frame := *entry.SubagentEnd
			if frame.Type != string(generated.WsFrameTypeSubagentEnd) || frame.SessionId != parentID {
				t.Errorf("persisted end frame wrong identity/route: %+v", frame)
			}
			frames.ends = append(frames.ends, frame)
		}
	}
	return frames
}

func liveStopDisplay(t *testing.T, events []Event, parentID, childID, spanID string) []generated.SubagentStateFrame {
	t.Helper()
	var frames []generated.SubagentStateFrame
	for _, event := range events {
		if event.Kind != EventKindSubagentState {
			continue
		}
		payload, ok := event.Payload.(SubagentStatePayload)
		if !ok {
			t.Fatalf("real state publisher used malformed payload %T", event.Payload)
		}
		if payload.Frame.ChildSessionId == nil || *payload.Frame.ChildSessionId != childID {
			continue
		}
		if payload.SessionID != parentID || payload.Frame.SessionId != parentID || payload.Frame.SpanId != spanID || payload.MessageID == "" {
			t.Errorf("live state frame wrong parent/child/span/persisted identity: %+v", payload)
		}
		frames = append(frames, payload.Frame)
	}
	return frames
}
func stopDisplaySpan(t *testing.T, al *AgentLoop, parentID, childID string) string {
	t.Helper()
	entries, readErr := al.GetSessionStore().ReadTranscript(parentID)
	if readErr != nil {
		t.Fatalf("read actual launch bracket: %v", readErr)
	}
	for _, entry := range entries {
		if entry.SubagentStart != nil && entry.SubagentStart.ChildSessionId != nil && *entry.SubagentStart.ChildSessionId == childID {
			return entry.SubagentStart.SpanId
		}
	}
	t.Fatal("SETUP: real child launch emitted no persisted start bracket; display instrument disconnected")
	return ""
}
func countStopDisplayState(frames []generated.SubagentStateFrame, state string) int {
	count := 0
	for _, frame := range frames {
		if frame.State == state {
			count++
		}
	}
	return count
}

func TestStopDisplay_ActualCompletionPublishesLiveAndDurableFrames(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	tap := observeStopDisplay(t, al)
	gate := newGoalRunGate("publication instrument real final", nil)
	installGoalRunProvider(t, al, gate)
	parentID := newTestSteeringSession(t, al, "ws-stop-display-positive")
	child, _ := u2LaunchLive(t, al, parentID, "call-display-instrument", gate)
	spanID := stopDisplaySpan(t, al, parentID, child.SessionID)
	gate.open()
	joinGoalFixtureRuns(t, al)
	durable := reopenedStopDisplay(t, al, parentID, child.SessionID, spanID)
	live := liveStopDisplay(t, tap.snapshot(), parentID, child.SessionID, spanID)
	if countStopDisplayState(live, "completed") != 1 || countStopDisplayState(durable.states, "completed") != 1 {
		t.Fatalf("instrument did not see actual completed publisher: live=%+v reopened=%+v", live, durable.states)
	}
	if len(durable.ends) != 1 || durable.ends[0].Status != "success" {
		t.Errorf("instrument did not see real success end contract: %+v", durable.ends)
	}
	var liveEnd int
	for _, event := range tap.snapshot() {
		if event.Kind != EventKindSubTurnEnd {
			continue
		}
		payload, ok := event.Payload.(SubTurnEndPayload)
		if !ok {
			t.Fatalf("real end publisher used malformed payload %T", event.Payload)
		}
		if payload.SessionID == parentID && payload.SpanID == spanID && payload.Status == SubTurnStatusSuccess {
			liveEnd++
		}
	}
	if liveEnd != 1 {
		t.Errorf("actual success end events=%d, want exactly1", liveEnd)
	}
}

// Both winning paths are actual producers: cancelled live turn completion and
// selected never-ran queued finalization. The observer never calls an emitter.
func TestStopDisplay_WinningStoppedPublishesCurrentLiveAndDurableState(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(fmt.Sprintf("queued_%v", queued), func(t *testing.T) { winningStopDisplayCase(t, queued) })
	}
}
func winningStopDisplayCase(t *testing.T, queued bool) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	tap := observeStopDisplay(t, al)
	parentID := newTestSteeringSession(t, al, "ws-winning-stop-display")
	oldGate := newGoalRunGate("stopped work must never publish final", nil)
	var blocker *goalRunGate
	if queued {
		al.GetConfig().Performance.MaxParallelAgents = 1
		blocker = newGoalRunGate("independent blocker final", nil)
		installGoalRunProvider(t, al, blocker)
		u2LaunchLive(t, al, parentID, "call-display-blocker", blocker)
	} else {
		installGoalRunProvider(t, al, oldGate)
	}
	launch, launchErr := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID, Task: "current stopped row must remain resumable",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-winning-stop-display"},
		Goal:   &steer.GoalSpec{Criteria: []steer.Criterion{{Text: "work complete"}}, DoD: []steer.Criterion{{Text: "evidence complete"}}},
	})
	if launchErr != nil {
		t.Fatalf("real goal child Launch: %v", launchErr)
	}
	dispatchChild(t, al, launch.SessionID, launch.Generation, !queued)
	if !queued {
		awaitGoalProvider(t, oldGate)
	}
	before := rootReopenedRecord(t, al, launch.SessionID)
	if before.ExecutionID == nil || before.ExecutionID.RunID == "" || before.ExecutionID.BootSeq != al.bootEpochFor() {
		t.Fatal("SETUP: real selected execution missing")
	}
	spanID := stopDisplaySpan(t, al, parentID, before.SessionID)
	liveBefore := len(tap.snapshot())
	report, stopErr := al.steerCanceller().StopTurns(context.Background(), before.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "display-owner"}, false, al.SteerGenerationCancel)
	if stopErr != nil || len(report.Unreachable) != 0 || !reflect.DeepEqual(report.Reached, []string{before.SessionID}) {
		t.Fatalf("real selected Stop did not reach its own producer: report=%+v err=%v", report, stopErr)
	}
	if queued {
		blocker.open()
	}
	joinGoalFixtureRuns(t, al)
	after := rootReopenedRecord(t, al, before.SessionID)
	if after.State != session.LifecycleStopped || after.Terminal() || after.Stop != nil || after.StopNote == nil || after.Generation != before.Generation || after.FinalDelivery != nil {
		t.Fatalf("winning producer did not actually land nonterminal stopped: %+v", after)
	}
	if g := mustGoalRecord(t, before.GoalRef); g.State != generated.GoalStateActive {
		t.Errorf("winning Stop ended active goal: %+v", g)
	}
	meta, metaErr := al.GetSessionStore().GetMeta(before.SessionID)
	if metaErr != nil || meta.Status != session.StatusActive {
		t.Errorf("stopped metadata=%+v err=%v, want coarse active (T26)", meta, metaErr)
	}
	durable := reopenedStopDisplay(t, al, parentID, before.SessionID, spanID)
	live := liveStopDisplay(t, tap.snapshot()[liveBefore:], parentID, before.SessionID, spanID)
	if countStopDisplayState(live, "stopped") != 1 {
		t.Errorf("winning stopped producer emitted %d current stopped frames, want exactly1; actual live=%+v", countStopDisplayState(live, "stopped"), live)
	}
	if countStopDisplayState(durable.states, "stopped") != 1 || len(durable.states) == 0 || durable.states[len(durable.states)-1].State != "stopped" {
		t.Errorf("reopened parent replay input does not end at current stopped: %+v", durable.states)
	}
	for _, frame := range durable.ends {
		if frame.Status == "interrupted" || frame.Status == "error" || frame.Status == "timeout" {
			t.Errorf("nonfatal stopped state must not restore a losing legacy end status: %+v", frame)
		}
	}
	msgs, _, _, readErr := al.GetMessageInboxStore().Drain(parentID, before.SessionID, "", 10)
	if readErr != nil || len(msgs) != 1 {
		t.Fatalf("winning Stop notices=%d err=%v, want one nonfatal stopped_child", len(msgs), readErr)
	}
	notice, noticeErr := msgs[0].AsSessionMessageError()
	if noticeErr != nil || notice.Fatal || !strings.HasPrefix(notice.Text, "stopped_child:") {
		t.Errorf("winning Stop restored fatal/final outcome instead of nonfatal notice: %+v err=%v", notice, noticeErr)
	}
}

// A historical notice rings from the real ledger even after the active note
// clears. Re-ringing is not a current lifecycle/display publication.
func TestStopDisplay_HistoricalNoticeAfterSameGenerationResumeDoesNotOverwriteCurrent(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	tap := observeStopDisplay(t, al)
	oldGate, freshGate := newGoalRunGate("old losing final", nil), newGoalRunGate("fresh turn held through historical rings", nil)
	installGoalRunProvider(t, al, oldGate, freshGate)
	parentID := newTestSteeringSession(t, al, "ws-historical-stop-display")
	child := launchGoalBearingChild(t, al, parentID, "call-historical-display", goalChildLaunchOptions{live: true})
	awaitGoalProvider(t, oldGate)
	spanID := stopDisplaySpan(t, al, parentID, child.SessionID)
	canceller := al.steerCanceller()
	report, stopErr := canceller.StopTurns(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "history-display-owner"}, false, al.SteerGenerationCancel)
	if stopErr != nil || len(report.Unreachable) != 0 {
		t.Fatalf("real old Stop: %+v err=%v", report, stopErr)
	}
	joinGoalFixtureRuns(t, al)
	stopped := rootReopenedRecord(t, al, child.SessionID)
	if stopped.State != session.LifecycleStopped || stopped.StopNote == nil || stopped.Stop != nil {
		t.Fatalf("SETUP: old selected run did not actually land stopped: %+v", stopped)
	}
	transitions, historyErr := al.GetSessionLifecycleStore().ListStoppedTransitions(child.SessionID)
	if historyErr != nil || len(transitions) != 1 || transitions[0].SessionID != child.SessionID || transitions[0].Generation != child.Generation {
		t.Fatalf("SETUP: old Stop has no exact real durable ledger transition: %+v err=%v", transitions, historyErr)
	}
	noticeID := fmt.Sprintf("stopped-notice:%s:%s:%d:%d", parentID, child.SessionID, child.Generation, transitions[0].StopSeq)
	original := stopDisplayNotice(t, al, parentID, child.SessionID, noticeID)
	resumedGen, resumeErr := canceller.Revive(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "history-display-owner"})
	if resumeErr != nil || resumedGen != child.Generation {
		t.Fatalf("real same-generation Resume=%d err=%v", resumedGen, resumeErr)
	}
	dispatchChild(t, al, child.SessionID, resumedGen, true)
	awaitGoalProvider(t, freshGate)
	fresh := rootReopenedRecord(t, al, child.SessionID)
	liveHandle := al.getActiveTurnState(child.SessionID)
	if fresh.ExecutionID == nil || fresh.ExecutionID.RunID == child.ExecutionID.RunID || fresh.ExecutionID.BootSeq != child.ExecutionID.BootSeq || fresh.State != session.LifecycleRunning || fresh.Stop != nil || fresh.StopNote != nil || fresh.StopEffect != nil || liveHandle == nil || !al.tsExecutionClaim(liveHandle, child.SessionID).matches(fresh) {
		t.Fatalf("SETUP: actual fresh same-generation/current-boot execution missing: %+v", fresh)
	}
	beforeFrames := reopenedStopDisplay(t, al, parentID, child.SessionID, spanID)
	if len(beforeFrames.states) == 0 || beforeFrames.states[len(beforeFrames.states)-1].State != "running" {
		t.Errorf("real Resume did not make reopened CURRENT display working: %+v", beforeFrames.states)
	}
	for {
		select {
		case <-al.bus.InboundChan():
			continue
		default:
			goto oldWakeDrained
		}
	}
oldWakeDrained:
	for ring := 0; ring < 2; ring++ {
		eventStart := len(tap.snapshot())
		pending, ringErr := al.deliverLandedStopNotices(context.Background(), stopped)
		if ringErr != nil || !pending {
			t.Fatalf("actual untaken original notice did not ring: pending=%v err=%v", pending, ringErr)
		}
		select {
		case wake := <-al.bus.InboundChan():
			if wake.AsyncTranscriptSessionID != parentID || wake.Metadata["steer_message_id"] != noticeID {
				t.Errorf("historical ring identity/parent=%+v, want original %s to direct parent %s", wake, noticeID, parentID)
			}
		default:
			t.Error("untaken original historical notice did not re-ring its parent")
		}
		nowNotice := stopDisplayNotice(t, al, parentID, child.SessionID, noticeID)
		if !reflect.DeepEqual(original, nowNotice) {
			t.Error("historical ring rewrote original notice id/cause/actor/time instead of delivering historical identity")
		}
		afterFrames := reopenedStopDisplay(t, al, parentID, child.SessionID, spanID)
		if !reflect.DeepEqual(beforeFrames, afterFrames) {
			t.Errorf("historical ring rewrote CURRENT parent replay input: before=%+v after=%+v", beforeFrames, afterFrames)
		}
		for _, event := range tap.snapshot()[eventStart:] {
			if event.Kind == EventKindSubagentState {
				payload, valid := event.Payload.(SubagentStatePayload)
				if !valid {
					t.Fatalf("state payload type=%T", event.Payload)
				}
				if payload.Frame.ChildSessionId != nil && *payload.Frame.ChildSessionId == child.SessionID {
					t.Errorf("historical ring emitted current state %q for fresh run (must not overwrite working)", payload.Frame.State)
				}
			}
			if event.Kind == EventKindSubTurnEnd {
				payload, valid := event.Payload.(SubTurnEndPayload)
				if !valid {
					t.Fatalf("end payload type=%T", event.Payload)
				}
				if payload.SpanID == spanID {
					t.Errorf("historical ring closed fresh live span: %+v", payload)
				}
			}
		}
		current := rootReopenedRecord(t, al, child.SessionID)
		if current.State != fresh.State || current.Generation != fresh.Generation || !reflect.DeepEqual(current.ExecutionID, fresh.ExecutionID) || current.Stop != nil || current.StopNote != nil || current.StopEffect != nil || liveHandle.hardAbortRequested() || !liveHandle.IsAlive() {
			t.Errorf("historical notice touched fresh same-generation execution: %+v", current)
		}
	}
	// Positive newer Stop remains real and effective; do not make a stale-ring
	// negative pass by unplugging all stop effects/publication machinery.
	newerReport, newerErr := canceller.StopTurns(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "history-display-owner"}, false, al.SteerGenerationCancel)
	if newerErr != nil || len(newerReport.Unreachable) != 0 {
		t.Fatalf("genuine newer Stop failed: %+v err=%v", newerReport, newerErr)
	}
	joinGoalFixtureRuns(t, al)
	final := rootReopenedRecord(t, al, child.SessionID)
	if final.State != session.LifecycleStopped || final.Stop != nil || final.StopNote == nil || final.ExecutionID == nil || *final.ExecutionID != *fresh.ExecutionID {
		t.Errorf("positive newer Stop did not stop the actual fresh run: %+v", final)
	}
	if goal := mustGoalRecord(t, child.GoalRef); goal.State != generated.GoalStateActive {
		t.Errorf("historical/newer Stop ended goal: %+v", goal)
	}
}

func stopDisplayNotice(t *testing.T, al *AgentLoop, parentID, childID, noticeID string) generated.SessionMessage {
	t.Helper()
	messages, _, _, readErr := al.GetMessageInboxStore().Drain(parentID, childID, "", 10)
	if readErr != nil || len(messages) != 1 {
		t.Fatalf("original notice messages=%d err=%v, want exactly1 untaken historical notice", len(messages), readErr)
	}
	notice, decodeErr := messages[0].AsSessionMessageError()
	if decodeErr != nil || notice.Fatal || notice.MessageId != noticeID || !strings.HasPrefix(notice.Text, "stopped_child:") {
		t.Fatalf("original nonfatal notice identity/content=%+v err=%v, want %s", notice, decodeErr, noticeID)
	}
	return messages[0]
}

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Real store before/after coverage is paired with TestR1Completion_InputAfterCommit...
// This case pins exact text/correlation identity at the pre-commit boundary.
func TestSteeredTurnDrain1020Round4c_CommitWindowAcceptsLateSteer(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parent := newTestSteeringSession(t, al, "ws-round4c")
	child, provider := r1AdmitChild(t, al, parent, "round4c-actual-commit", "candidate answer", "late instruction processed")
	late := steeringQueueItem{
		message:       providers.Message{Role: "user", Content: "late instruction"},
		correlationID: "late-steer-receipt",
	}
	var got []steeringQueueItem
	var status EnqueueStatus
	var enqueueErr error
	r1InjectBeforeCommit(t, al, child, func() {
		_, status, enqueueErr = al.EnqueueSteeringMessageWithStatus(child.SessionID, testDefaultAgentID, late.message, late.correlationID)
		al.steering.mu.Lock()
		if transition := al.steering.terminalizing[child.SessionID]; transition != nil {
			got = append([]steeringQueueItem(nil), transition.finishingItems...)
		}
		al.steering.mu.Unlock()
	})
	provider.open(0)
	r1AwaitProvider(t, provider, 1)
	if enqueueErr != nil || status != EnqueueStatusPostFinish {
		t.Fatalf("real commit-window acceptance status=%v error=%v", status, enqueueErr)
	}
	if len(got) != 1 {
		t.Fatalf("finishing handoff contains %d items, want exactly one", len(got))
	}
	if !reflect.DeepEqual(got[0].message, late.message) || got[0].correlationID != late.correlationID {
		t.Errorf("finishing handoff=(%+v,%q), want exact (%+v,%q)", got[0].message, got[0].correlationID, late.message, late.correlationID)
	}
	r1AssertNoFinal(t, al, child)
	assertSteerRevivalInput1020(t, provider.Requests()[1:], []string{late.message.Content})
	provider.open(1)
	joinGoalFixtureRuns(t, al)
	r1RequireSameGenerationFinal(t, al, child, "late instruction processed")
}

// Historical name retained. R1/D5 supersede indefinite retention of EARLIER
// pending steers after a NEWER Stop: each needs an explicit superseded receipt,
// never silent loss or a false abandonment. Input AFTER a landed Stop resumes G.
func TestSteeredTurnDrain1020Round4_OuterExhaustionDefersQueuedSteersToFutureRevival(t *testing.T) {
	t.Run("earlier_pending_input_requires_explicit_newer_Stop_supersession", func(t *testing.T) {
		al, _ := newSteerAL(t)
		wireSteerCompletionDeps(t, al)
		parent := newTestSteeringSession(t, al, "ws-round4c-precedence")
		child, provider := r1AdmitChild(t, al, parent, "round4c-stop-precedence", "must not publish after newer Stop")
		accepted := []string{"round4c-tail-1", "round4c-tail-2"}
		for _, text := range accepted {
			if _, err := al.EnqueueSteeringMessage(child.SessionID, testDefaultAgentID,
				providers.Message{Role: "user", Content: text}, text); err != nil {
				t.Fatalf("accept older steer %q: %v", text, err)
			}
		}
		al.steering.mu.Lock()
		queued := append([]steeringQueueItem(nil), al.steering.queues[child.SessionID]...)
		al.steering.mu.Unlock()
		if len(queued) != len(accepted) {
			t.Fatalf("accepted earlier queue=%d, want two", len(queued))
		}
		for i, text := range accepted {
			if queued[i].message.Content != text || queued[i].correlationID != text {
				t.Errorf("accepted earlier item[%d]=%+v, want unchanged text/identity %q in order", i, queued[i], text)
			}
		}
		// Capture the real accepted control IDs and complete acceptance records
		// BEFORE Stop; matching only text after Stop could accept forged receipts.
		acceptances := qaSnapshotQueuedControlAcceptances(t, al, child.SessionID, queued)
		result, err := al.StopSession(context.Background(), StopRequest{
			SessionID: child.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "newer-stop"},
			HooksFor: func(string) CancelHooks { return CancelHooks{} },
		})
		if err != nil || result.RootErr != nil || len(result.Report.Unreachable) != 0 {
			t.Fatalf("newer real Stop=%+v error=%v", result, err)
		}
		provider.openAll()
		joinGoalFixtureRuns(t, al)
		rec := rootReopenedRecord(t, al, child.SessionID)
		if rec.State != session.LifecycleStopped || rec.Generation != child.Generation || rec.Stop != nil || rec.StopNote == nil || rec.FinalDelivery != nil {
			t.Fatalf("newer Stop lost or published a final: %+v", rec)
		}
		if len(provider.Requests()) != 1 {
			t.Errorf("older pending steering ran past the winning Stop: provider calls=%d", len(provider.Requests()))
		}
		childEntries, err := al.GetSessionStore().ReadTranscript(child.SessionID)
		if err != nil {
			t.Fatalf("ReadTranscript(child): %v", err)
		}
		for _, entry := range childEntries {
			if entry.Status == "error" && strings.Contains(entry.Content, "queued follow-up message could not be processed") {
				t.Errorf("superseded input falsely reported as an abandoned delivery: %q", entry.Content)
			}
		}
		parentEntries, err := al.GetSessionStore().ReadTranscript(parent)
		if err != nil {
			t.Fatalf("ReadTranscript(parent): %v", err)
		}
		for _, entry := range parentEntries {
			frame := entry.SubagentMessage
			if frame != nil && frame.Kind == "error" && frame.ChildSessionId != nil && *frame.ChildSessionId == child.SessionID {
				t.Errorf("parent falsely told superseded (not abandoned) input failed: %+v", frame)
			}
		}
		// D4's real ledger file is the storage surface, not a fake receipt store.
		// Its Stop line is the positive instrument before checking missing steers.
		ledgerPath := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", "controls", child.SessionID+".jsonl")
		raw, err := os.ReadFile(ledgerPath)
		if err != nil {
			t.Fatalf("read real control ledger: %v", err)
		}
		ledgerLines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		lines := make([]map[string]any, 0, len(ledgerLines))
		sawStop := false
		for _, line := range ledgerLines {
			var fields map[string]any
			if err := json.Unmarshal([]byte(line), &fields); err != nil {
				t.Fatalf("decode control ledger: %v", err)
			}
			if fields["verb"] == "stop" {
				sawStop = true
			}
			lines = append(lines, fields)
		}
		if !sawStop {
			t.Fatal("instrument failure: real accepted Stop was not visible in the control ledger")
		}
		for _, text := range accepted {
			superseded := 0
			for _, line := range lines {
				if line["verb"] == "steer" && line["text"] == text && line["state"] == "superseded" {
					superseded++
				}
			}
			if superseded != 1 {
				t.Fatalf("BLOCKED: accepted steer %q has %d explicit superseded ledger receipts; durable steer intent/newer-Stop supersession not implemented — required by frozen D4/D5 and R1 outer-exhaustion disposition (remaining queue=%d)", text, superseded, al.pendingSteeringCountForScope(child.SessionID))
			}
		}
		qaAssertAcceptedControlsSuperseded(t, acceptances, lines, rec.StopNote.Seq)
		if pending := al.pendingSteeringCountForScope(child.SessionID); pending != 0 {
			t.Errorf("D5: Stop left %d accepted earlier steers in the final queue, want zero", pending)
		}
		unfinished, err := al.GetSessionLifecycleStore().UnfinishedStopIntents(child.SessionID)
		if err != nil || len(unfinished) != 0 {
			t.Errorf("landed winning Stop still has unfinished intents: %+v error=%v, want none", unfinished, err)
		}
	})
	t.Run("new_input_after_landed_Stop_resumes_same_generation", func(t *testing.T) {
		al, _ := newSteerAL(t)
		wireSteerCompletionDeps(t, al)
		parent := newTestSteeringSession(t, al, "ws-round4c-post-stop")
		child, provider := r1AdmitChild(t, al, parent, "round4c-after-landed-stop", "stopped partial answer", "new instruction after Stop processed")
		result, err := al.StopSession(context.Background(), StopRequest{
			SessionID: child.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"},
			HooksFor: func(string) CancelHooks { return CancelHooks{} },
		})
		if err != nil || result.RootErr != nil || len(result.Report.Unreachable) != 0 {
			t.Fatalf("Stop: result=%+v error=%v", result, err)
		}
		provider.open(0)
		joinGoalFixtureRuns(t, al)
		stopped := rootReopenedRecord(t, al, child.SessionID)
		if stopped.State != session.LifecycleStopped || stopped.Stop != nil {
			t.Fatalf("Stop has not landed: %+v", stopped)
		}
		noticeID, _ := assertU1StoppedChildNotice(t, al, parent, child, string(session.StopCauseStop), "operator")
		const text = "ROUND4C-NEW-INSTRUCTION-AFTER-LANDED-STOP"
		resultText := runDelegateSteer(t, al, parent, child.SessionID, text)
		if resultText == nil || resultText.IsError {
			t.Fatalf("registered delegate refused input after landed Stop: %+v", resultText)
		}
		r1AwaitProvider(t, provider, 1)
		running := rootReopenedRecord(t, al, child.SessionID)
		if running.Generation != child.Generation || running.State != session.LifecycleRunning || running.ExecutionID == nil || running.ExecutionID.RunID == child.ExecutionID.RunID {
			t.Fatalf("post-Stop message did not resume same G with fresh execution: %+v", running)
		}
		assertSteerRevivalInput1020(t, provider.Requests()[1:], []string{text})
		provider.open(1)
		joinGoalFixtureRuns(t, al)
		if al.pendingSteeringCountForScope(child.SessionID) != 0 {
			t.Error("post-Stop accepted instruction stranded")
		}
		// The landed Stop's separate D6 notice remains owed/history. It is
		// not a second final; require exactly that notice and one G final.
		messages, _, more, readErr := al.GetMessageInboxStore().Drain(parent, child.SessionID, "", 10)
		if readErr != nil || more || len(messages) != 2 {
			t.Fatalf("post-Stop inbox=%d more=%v error=%v, want one notice plus one final", len(messages), more, readErr)
		}
		finalID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
		finals, notices := 0, 0
		for _, message := range messages {
			switch messageIDOf(message) {
			case noticeID:
				notices++
			case finalID:
				finals++
				answer, decodeErr := message.AsSessionMessageHandback()
				if decodeErr != nil || answer.ResultSoFar != "new instruction after Stop processed" {
					t.Errorf("post-Stop G final=%q error=%v, want exact new-instruction answer", answer.ResultSoFar, decodeErr)
				}
			default:
				t.Errorf("unexpected post-Stop message %q", messageIDOf(message))
			}
		}
		if finals != 1 || notices != 1 {
			t.Errorf("post-Stop messages duplicated/lost: finals=%d notices=%d, want one each", finals, notices)
		}
	})
}

// Internal ledger snapshots, not gateway wire types. Derive IDs from real
// acceptance, never invent them or infer them from a post-Stop receipt.
func qaSnapshotQueuedControlAcceptances(t *testing.T, al *AgentLoop, childID string, queued []steeringQueueItem) map[string]map[string]any {
	t.Helper()
	path := filepath.Join(al.GetConfig().Agents.Defaults.Home, "session_lifecycle", "controls", childID+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read control ledger BEFORE Stop: %v", err)
	}
	var before []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("decode acceptance before Stop: %v", err)
		}
		before = append(before, fields)
	}
	out := make(map[string]map[string]any, len(queued))
	for _, item := range queued {
		id := item.steerControlID
		if id == "" || out[id] != nil {
			t.Fatalf("SETUP: real queued steers must carry distinct nonempty accepted IDs: %q", id)
		}
		matches := 0
		for _, line := range before {
			if line["control_id"] == id {
				matches++
				if line["verb"] != "steer" || line["state"] != "queued" || line["text"] != item.message.Content || line["correlation_id"] != item.correlationID {
					t.Fatalf("SETUP: queue and original durable acceptance differ for %q: %+v", id, line)
				}
				out[id] = line
			}
		}
		if matches != 1 {
			t.Fatalf("SETUP: accepted control %q has %d original ledger records before Stop, want exactly one", id, matches)
		}
	}
	return out
}

func qaAssertAcceptedControlsSuperseded(t *testing.T, accepted map[string]map[string]any, lines []map[string]any, stopSeq uint64) {
	t.Helper()
	if stopSeq == 0 {
		t.Fatal("instrument failure: winning landed Stop has no sequence")
	}
	latest := make(map[string]map[string]any)
	counts := make(map[string]int)
	for _, line := range lines {
		if line["verb"] != "steer" {
			continue
		}
		id, ok := line["control_id"].(string)
		if !ok || accepted[id] == nil {
			t.Errorf("supersession fabricated an unaccepted steer identity: %+v", line)
			continue
		}
		latest[id] = line
		if line["state"] == "superseded" {
			counts[id]++
		}
	}
	for id, original := range accepted {
		if counts[id] != 1 {
			t.Errorf("D4/D5: originally accepted control %q has %d superseded receipts, want exactly one", id, counts[id])
		}
		want := make(map[string]any, len(original)+2)
		for key, value := range original {
			want[key] = value
		}
		want["state"], want["reason"], want["superseded_by_seq"] = "superseded", "stop", float64(stopSeq)
		if !reflect.DeepEqual(latest[id], want) {
			t.Errorf("D4/D5: original control %q final disposition=%+v, want its exact acceptance plus winning Stop supersession=%+v", id, latest[id], want)
		}
	}
}

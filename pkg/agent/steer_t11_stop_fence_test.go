package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// T11 Stop-first oracle, written from the ADR before asserting on this tree.
//
// Source: ADR-20260928-sub-agent-control-plane (asset cd20cf8b) D2
// "One outcome/publication commit boundary" and acceptance test T11.
// If the current-generation Stop fence commits before the completion's
// terminal/outbox mutation, that completion is not allowed to publish
// <child>:<generation>:final. The parent inbox has no such message, Deliver
// writes no frame for it, nothing acknowledges it, and the child lands
// stopped with the lasting stop note and no current fence.
//
// What this test actually drives: the production AgentLoop.completeSteeredTurn
// and SteerCanceller.CancelSubtree, on the one real LifecycleStore and the
// one real parent inbox the loop already uses. No completeStateWriteTestHook
// and no second fake store.
//
// Not claimed here (no shared mutation dependency, and no
// LifecycleStore.UpdateFinalDelivery / ListPendingFinalDeliveries yet):
// pausing either writer before and after the commit, crash cuts, payload
// retirement, and all-generation discovery. Those observations are blocked
// on the seam the ADR names; they are not asserted below.
const t11StopFirstAnswer = "t11-stop-first-answer-must-stay-unpublished"

func TestT11_StopFenceBeforeFinal_PublishesNoLosingFinal(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	parentID := newTestSteeringSession(t, al, "ws-t11-stop-first")
	const callID = "call-t11-stop-first"
	rec := launchRunningChild(t, al, parentID, callID)
	generation := rec.Generation
	childID := rec.SessionID
	// ADR D2 / D4: the losing completion's id is exactly this string.
	finalID := fmt.Sprintf("%s:%d:final", childID, generation)

	var wakeMu sync.Mutex
	wakeFinal := 0
	if al.asyncNotifier != nil {
		al.asyncNotifier.registerObserver(func(ev AsyncNotifyEvent) {
			id, _ := ev.Metadata["steer_message_id"].(string)
			if id != finalID {
				return
			}
			wakeMu.Lock()
			wakeFinal++
			wakeMu.Unlock()
		})
	}

	canceller := NewSteerCanceller(al.GetSessionLifecycleStore(), al.SteerGenerationCancel)
	report, err := canceller.CancelSubtree(context.Background(), childID, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   "t11-owner",
	})
	if err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	if len(report.Unreachable) != 0 {
		t.Fatalf("CancelSubtree left the child unreachable: %+v", report.Unreachable)
	}
	reached := false
	for _, id := range report.Reached {
		if id == childID {
			reached = true
			break
		}
	}
	if !reached {
		t.Fatalf("CancelSubtree did not stamp %s (reached=%v skippedTerminal=%v)", childID, report.Reached, report.SkippedTerminal)
	}

	// The in-flight turn then finishes. Its snapshot is the pre-stop record.
	// A correct completion refuses the final; today's writers publish first.
	completeErr := al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: t11StopFirstAnswer}, nil)

	got, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load(child after Stop-then-complete): %v", err)
	}
	inboxEntries, err := al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(parent inbox): %v", err)
	}
	frames, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript(parent): %v", err)
	}

	finalMessages, finalAcks := t11FinalInbox(inboxEntries, finalID)
	endFrames, handbackFrames, completedStates, childMessages := t11FinalFrames(frames, childID, callID, generation)
	wakeMu.Lock()
	wakes := wakeFinal
	wakeMu.Unlock()

	if len(finalMessages) != 0 {
		t.Errorf("parent inbox messages with id %s = %d, want 0 (Stop fence committed first, so the losing final must not be published); bodies: %s; completeSteeredTurn err: %v",
			finalID, len(finalMessages), finalMessages, completeErr)
	}
	if finalAcks != 0 {
		t.Errorf("acks of %s = %d, want 0", finalID, finalAcks)
	}
	if wakes != 0 {
		t.Errorf("parent wakes for %s = %d, want 0", finalID, wakes)
	}
	if endFrames != 0 || handbackFrames != 0 || completedStates != 0 || childMessages != 0 {
		t.Errorf("losing-final frames for child %s generation %d: subagent_end=%d handback=%d state=completed=%d subagent_message=%d, want all 0",
			childID, generation, endFrames, handbackFrames, completedStates, childMessages)
	}
	if got.Generation != generation {
		t.Errorf("generation = %d, want %d (Stop-first must not mint a new generation)", got.Generation, generation)
	}
	if got.State != session.LifecycleStopped {
		t.Errorf("state = %q, want %q (the stop path lands stopped; a later completion must not replace it with done)", got.State, session.LifecycleStopped)
	}
	if got.Stop != nil && got.Stop.Generation == got.Generation {
		t.Errorf("current-generation Stop fence still set; landing stopped clears the fence and keeps stop_note")
	}
	if got.StopNote == nil {
		t.Errorf("stop_note = nil, want the lasting stop cause retained")
	} else if got.StopNote.Cause != session.StopCauseStop {
		t.Errorf("stop_note.cause = %q, want %q (this child is the named Stop target)", got.StopNote.Cause, session.StopCauseStop)
	}
}

func t11FinalInbox(entries []session.InboxEntry, finalID string) (bodies []string, acks int) {
	for _, entry := range entries {
		if entry.Kind == session.InboxEntryAck {
			for _, id := range entry.AckedIDs {
				if id == finalID {
					acks++
				}
			}
			continue
		}
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		if messageIDOf(*entry.Message) != finalID {
			continue
		}
		raw, err := entry.Message.MarshalJSON()
		if err != nil {
			bodies = append(bodies, fmt.Sprintf("<marshal: %v>", err))
			continue
		}
		bodies = append(bodies, string(raw))
	}
	return bodies, acks
}

func t11FinalFrames(entries []session.TranscriptEntry, childID, callID string, generation int) (endFrames, handbackFrames, completedStates, childMessages int) {
	spanID := SubagentSpanID(callID, generation)
	for _, entry := range entries {
		switch entry.SystemSubtype {
		case session.SystemSubtypeSubagentEnd:
			if entry.SubagentEnd != nil && entry.SubagentEnd.SpanId == spanID {
				endFrames++
			}
		case session.SystemSubtypeSubagentState:
			if entry.SubagentState == nil || entry.SubagentState.ChildSessionId == nil || *entry.SubagentState.ChildSessionId != childID {
				continue
			}
			if entry.SubagentState.State == string(session.LifecycleCompleted) {
				completedStates++
			}
		case session.SystemSubtypeSubagentMessage:
			if entry.SubagentMessage == nil || entry.SubagentMessage.ChildSessionId == nil || *entry.SubagentMessage.ChildSessionId != childID {
				continue
			}
			childMessages++
			if entry.SubagentMessage.Kind == "handback" {
				handbackFrames++
			}
		}
	}
	return endFrames, handbackFrames, completedStates, childMessages
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for parent-wake visibility, Tests 12, 23, 27 and 28.
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md
//   - FR-010, FR-011, FR-025, FR-026
//   - PW-D4, PW-D13, PW-D19
//   - BDD chatter outline, questions/blockers/failures, ordinary non-fatal error
//   - Planned tests TestProgress_DoesNotCallWakeParent,
//     TestDeliver_QuestionBlockerFailureWakeOnce,
//     TestDeliver_OrdinaryNonFatalDoesNotWake,
//     TestStopNotice_LandedStopWithoutMarkerBlocksWake
//
// Step 4 items 2–3 are deferred to CHECK.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestProgress_DoesNotCallWakeParent(t *testing.T) {
	al, _, _, deliverer := newPWDeliverTestLoop(t)
	const parentID, childID = "parent-pw-progress", "child-pw-progress"
	pwSeedDeliverySessions(t, al, parentID, childID)

	kinds := []struct {
		name    string
		outcome steer.Outcome
		msg     generated.SessionMessage
	}{
		{"progress", steer.OutcomeProgress, pwAgentProgress(t, childID, "p-1")},
		{"checkpoint", steer.OutcomeCheckpoint, pwAgentCheckpoint(t, childID, "c-1")},
		{"artifact", steer.OutcomeCheckpoint, pwAgentArtifact(t, childID, "a-1")},
	}
	for i, tc := range kinds {
		delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
			ChildSessionID: childID, Outcome: tc.outcome, Message: tc.msg,
		})
		if err != nil {
			t.Fatalf("Deliver(%s): %v", tc.name, err)
		}
		if delivery.Outcome != steer.DeliveryStoredNotWoken {
			t.Errorf("delivery %d (%s) outcome = %q, want stored_not_woken — chatter is stored and does not wake (PW-D4 / FR-011 / SC-004)", i+1, tc.name, delivery.Outcome)
		}
	}
	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	var sidePanel int
	for _, e := range entries {
		if e.SystemSubtype == session.SystemSubtypeSubagentMessage {
			sidePanel++
		}
	}
	if sidePanel < 1 {
		t.Errorf("parent side-panel lines = %d, want at least 1 — accepted chatter is still shown (FR-011)", sidePanel)
	}
}

func TestDeliver_QuestionBlockerFailureWakeOnce(t *testing.T) {
	al, _, inbox, deliverer := newPWDeliverTestLoop(t)
	const parentID, childID = "parent-pw-ask", "child-pw-ask"
	pwSeedDeliverySessions(t, al, parentID, childID)

	cases := []struct {
		name    string
		outcome steer.Outcome
		msg     generated.SessionMessage
		text    string
		kind    string
	}{
		{"question", steer.OutcomeParkedQuestion, pwAgentQuestion(t, childID, "q-1", "which file?"), "which file?", "question"},
		{"blocker", steer.OutcomeBlocker, pwAgentBlocker(t, childID, "b-1", "need a decision"), "need a decision", "blocker"},
		{"fatal", steer.OutcomeFailed, pwAgentFatal(t, childID, "f-1", "the helper failed"), "the helper failed", "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
				ChildSessionID: childID, Outcome: tc.outcome, Message: tc.msg,
			})
			if err != nil {
				t.Fatalf("Deliver: %v", err)
			}
			if delivery.Outcome != steer.DeliveryWoke && delivery.Outcome != steer.DeliveryQueuedIntoLiveTurn {
				t.Fatalf("outcome = %q, want one wake (woke or joined) — a %s wakes an idle parent once (FR-025)", delivery.Outcome, tc.name)
			}
			rows := pwHelperEventRows(t, al, parentID, tc.kind)
			if len(rows) != 1 {
				t.Fatalf("parent helper_event rows of kind %s = %d, want 1 — the parent chat shows the text (FR-025 / §9.3)", tc.kind, len(rows))
			}
			if !strings.Contains(rows[0].Content, tc.text) {
				t.Fatalf("row content = %q, want it to contain %q", rows[0].Content, tc.text)
			}
			again, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
				ChildSessionID: childID, Outcome: tc.outcome, Message: tc.msg,
			})
			if err != nil {
				t.Fatalf("second Deliver: %v", err)
			}
			if again.Outcome == steer.DeliveryWoke || again.Outcome == steer.DeliveryQueuedIntoLiveTurn {
				t.Fatalf("second delivery outcome = %q, want no second wake for the same id (FR-022 / Test 27)", again.Outcome)
			}
			if got := len(pwHelperEventRows(t, al, parentID, tc.kind)); got != 1 {
				t.Fatalf("after redelivery, kind %s rows = %d, want 1", tc.kind, got)
			}
		})
	}
	_ = inbox
}

func TestDeliver_OrdinaryNonFatalDoesNotWake(t *testing.T) {
	al, _, _, deliverer := newPWDeliverTestLoop(t)
	const parentID, childID = "parent-pw-err", "child-pw-err"
	// PW-D12 / §9.4: a positive wake control needs a real recipient's
	// metadata. A missing parent is a refusal, not a tool-ceiling exception.
	pwSeedDeliverySessions(t, al, parentID, childID)

	ordinary, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID,
		Outcome:        steer.OutcomeLifecycleNotice,
		Message:        pwAgentError(t, childID, "e-ordinary", false, "disk full"),
	})
	if err != nil {
		t.Fatalf("Deliver ordinary: %v", err)
	}
	if ordinary.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("ordinary non-fatal outcome = %q, want stored_not_woken (FR-026)", ordinary.Outcome)
	}

	stopped, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID,
		Outcome:        steer.OutcomeLifecycleNotice,
		Message:        pwAgentError(t, childID, "e-stopped", false, session.LifecycleNoticePrefixStoppedChild+" cause: stop"),
	})
	if err != nil {
		t.Fatalf("Deliver stopped_child: %v", err)
	}
	if stopped.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("stopped_child: notice outcome = %q, want stored_not_woken — the prefix alone does not wake (PW-D13)", stopped.Outcome)
	}

	ceiling, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID,
		Outcome:        steer.OutcomeLifecycleNotice,
		Message:        pwAgentError(t, childID, "e-ceiling", false, session.LifecycleNoticeReasonPrefixToolIterations+" 10"),
	})
	if err != nil {
		t.Fatalf("Deliver max_tool_iterations: %v", err)
	}
	if ceiling.Outcome != steer.DeliveryWoke && ceiling.Outcome != steer.DeliveryQueuedIntoLiveTurn {
		t.Fatalf("max_tool_iterations outcome = %q, want one wake — a tool-iteration ceiling is a failure, not a Stop", ceiling.Outcome)
	}
}

// PW-D12 / §9.4 (final spec, §22 keeps that refusal): a missing
// recipient's metadata is never waived for a max_tool_iterations failure.
func TestDeliver_ToolIterationMissingParentMetadataDoesNotWake(t *testing.T) {
	al, _, _, deliverer := newPWDeliverTestLoop(t)
	const parentID, childID = "parent-pw-missing-meta", "child-pw-missing-meta"
	pwSeedDeliverySessions(t, al, parentID, childID)
	if err := al.GetSessionStore().DeleteSession(parentID); err != nil {
		t.Fatalf("SETUP: remove only the recipient metadata: %v", err)
	}
	if _, err := al.GetSessionStore().GetMeta(parentID); err == nil {
		t.Fatal("SETUP: missing-metadata refusal needs an absent recipient")
	}
	wakes := pwObserveNotifier(t, al)
	delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID,
		Outcome:        steer.OutcomeLifecycleNotice,
		Message:        pwAgentError(t, childID, "ceiling-without-parent", false, session.LifecycleNoticeReasonPrefixToolIterations+" 10"),
	})
	if err != nil {
		t.Fatalf("Deliver missing parent metadata: %v", err)
	}
	if delivery.Outcome != steer.DeliveryStoredNotWoken {
		t.Errorf("tool-iteration failure with missing parent metadata outcome=%q, want stored_not_woken (PW-D12)", delivery.Outcome)
	}
	if got := wakes(); got != 0 {
		t.Errorf("tool-iteration failure with missing parent metadata published %d wakes, want 0 (PW-D12)", got)
	}
}

func TestStopNotice_LandedStopWithoutMarkerBlocksWake(t *testing.T) {
	al, lifecycle, _, deliverer := newPWDeliverTestLoop(t)
	const parentID, childID = "parent-pw-stopmark", "child-pw-stopmark"
	pwSeedDeliverySessions(t, al, parentID, childID)

	t.Run("landed intentional stop whose marker was cleared stores the report and starts no turn", func(t *testing.T) {
		if err := lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
			rec.State = session.LifecycleStopped
			rec.Stop = nil
			rec.StopNote = &session.StopNote{
				At: time.Now().UTC(), By: session.StopActorHumanUser("owner"), Cause: session.StopCauseStop, Seq: 1,
			}
			return nil
		}); err != nil {
			t.Fatalf("mutate parent: %v", err)
		}
		delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
			ChildSessionID: childID, Outcome: steer.OutcomeFinalAnswer,
			Message: pwAgentHandback(t, childID, "hb-cleared"),
		})
		if err != nil {
			t.Fatalf("Deliver: %v", err)
		}
		if delivery.Outcome != steer.DeliveryStoredNotWoken {
			t.Fatalf("outcome = %q, want stored_not_woken — a landed intentional Stop still refuses a wake after the marker is cleared (PW-D19 / FR-010)", delivery.Outcome)
		}
	})
}

func pwHelperEventRows(t *testing.T, al *AgentLoop, sessionID, kind string) []session.TranscriptEntry {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessionID, err)
	}
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.SystemSubtype != "helper_event" {
			continue
		}
		if kind == "" || strings.Contains(e.Content, kind) || strings.Contains(e.ID, kind) {
			out = append(out, e)
		}
	}
	return out
}

func pwAgentProgress(t *testing.T, childID, id string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: id, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: "working on it",
	}); err != nil {
		t.Fatalf("progress: %v", err)
	}
	return sm
}

func pwAgentCheckpoint(t *testing.T, childID, id string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageCheckpoint(generated.SessionMessageCheckpoint{
		MessageId: id, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Summary: "halfway",
	}); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	return sm
}

func pwAgentArtifact(t *testing.T, childID, id string) generated.SessionMessage {
	t.Helper()
	note := "draft"
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageArtifact(generated.SessionMessageArtifact{
		MessageId: id, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Paths: []string{"out.md"}, Note: &note,
	}); err != nil {
		t.Fatalf("artifact: %v", err)
	}
	return sm
}

func pwAgentQuestion(t *testing.T, childID, id, text string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId: id, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", CorrelationId: "corr-" + id, Text: text,
	}); err != nil {
		t.Fatalf("question: %v", err)
	}
	return sm
}

func pwAgentBlocker(t *testing.T, childID, id, text string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageBlocker(generated.SessionMessageBlocker{
		MessageId: id, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: text, Severity: generated.SessionMessageBlockerSeverityHigh,
	}); err != nil {
		t.Fatalf("blocker: %v", err)
	}
	return sm
}

func pwAgentFatal(t *testing.T, childID, id, text string) generated.SessionMessage {
	t.Helper()
	return pwAgentError(t, childID, id, true, text)
}

func pwAgentError(t *testing.T, childID, id string, fatal bool, text string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageError(generated.SessionMessageError{
		MessageId: id, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: text, Fatal: fatal,
	}); err != nil {
		t.Fatalf("error message: %v", err)
	}
	return sm
}

func pwAgentHandback(t *testing.T, childID, id string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: id, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Mode: generated.SessionMessageHandbackModeFinal,
		ResultSoFar: "done", Artifacts: []string{}, OpenQuestions: []string{},
	}); err != nil {
		t.Fatalf("handback: %v", err)
	}
	return sm
}

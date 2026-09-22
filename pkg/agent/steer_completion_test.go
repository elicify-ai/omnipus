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

func wireSteerCompletionDeps(t *testing.T, al *AgentLoop) {
	t.Helper()
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(classifier),
		steer.NopBoundaryObserver{},
		NewSteerUpwardDeliverer(),
	)
}

func launchRunningChild(t *testing.T, al *AgentLoop, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "do delegated work",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if err := al.GetSessionLifecycleStore().Persist(rec); err != nil {
		t.Fatalf("Persist(running): %v", err)
	}
	return rec
}

func TestCompletion_Disposition_PersistedAndValidated(t *testing.T) {
	tests := []struct {
		name       string
		answer     string
		withQueued bool
		wantState  session.LifecycleState
		wantKind   string
		wantText   string
	}{
		{name: "non-empty quiet", answer: "finished", wantState: session.LifecycleCompleted, wantKind: "handback"},
		{name: "empty quiet", answer: "   ", wantState: session.LifecycleFailed, wantKind: "error", wantText: "empty_answer:"},
		{name: "non-empty queued descendant", answer: "parent answer", withQueued: true, wantState: session.LifecycleRunning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			wireSteerCompletionDeps(t, al)
			parentID := newTestSteeringSession(t, al, "ws-1")
			rec := launchRunningChild(t, al, parentID, "call-complete")
			if tc.withQueued {
				if _, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
					SteeringSessionID: rec.SessionID,
					TargetAgentID:     testDefaultAgentID,
					Task:              "queued grandchild",
					Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-grandchild"},
				}); err != nil {
					t.Fatalf("Launch(grandchild): %v", err)
				}
			}

			al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: tc.answer}, nil)

			got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
			if err != nil {
				t.Fatalf("Load(after completion): %v", err)
			}
			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.State, tc.wantState)
			}
			msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, rec.SessionID, "", 10)
			if err != nil {
				t.Fatalf("Drain(parent): %v", err)
			}
			if tc.wantKind == "" {
				if len(msgs) != 0 {
					t.Fatalf("messages = %d, want none while descendant is queued", len(msgs))
				}
				return
			}
			if len(msgs) != 1 {
				t.Fatalf("messages = %d, want 1", len(msgs))
			}
			kind, err := msgs[0].Discriminator()
			if err != nil || kind != tc.wantKind {
				t.Fatalf("message kind = %q (%v), want %q", kind, err, tc.wantKind)
			}
			if tc.wantText != "" {
				v, err := msgs[0].AsSessionMessageError()
				if err != nil || !strings.Contains(v.Text, tc.wantText) {
					t.Fatalf("error message = %q (%v), want contains %q", v.Text, err, tc.wantText)
				}
			}
		})
	}
}

func TestCompletion_LastChildCompletesWaitingParent(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	waitingParent := launchRunningChild(t, al, rootID, "call-parent")
	if err := al.GetSessionStore().AppendTranscriptStrict(waitingParent.SessionID, session.TranscriptEntry{
		ID: "parent-answer", Role: "assistant", Content: "parent result", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict(parent answer): %v", err)
	}
	lastChild := launchRunningChild(t, al, waitingParent.SessionID, "call-child")
	parked := launchRunningChild(t, al, waitingParent.SessionID, "call-parked")
	parked.State = session.LifecycleNeedsInput
	parked.NeedsInput = &session.NeedsInput{CorrelationID: "q-1", Reconstructable: true}
	if err := al.GetSessionLifecycleStore().Persist(parked); err != nil {
		t.Fatalf("Persist(parked): %v", err)
	}
	var question generated.SessionMessage
	if err := question.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		Kind: generated.SessionMessageQuestionKindQuestion, MessageId: "q-1", SessionId: parked.SessionID,
		SenderIdentity: parked.AgentID, CreatedAt: time.Now().UTC(), Depth: 1, Text: "Which region?",
	}); err != nil {
		t.Fatalf("encode question: %v", err)
	}
	if _, err := al.GetMessageInboxStore().Append(waitingParent.SessionID, question); err != nil {
		t.Fatalf("Append(question): %v", err)
	}

	al.completeSteeredTurn(context.Background(), lastChild, turnResult{finalContent: "child result"}, nil)

	got, err := al.GetSessionLifecycleStore().Load(waitingParent.SessionID)
	if err != nil {
		t.Fatalf("Load(waiting parent): %v", err)
	}
	if got.State != session.LifecycleCompleted {
		t.Fatalf("waiting parent state = %q, want completed", got.State)
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(rootID, waitingParent.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(root): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("root messages = %d, want 1", len(msgs))
	}
	handback, err := msgs[0].AsSessionMessageHandback()
	if err != nil {
		t.Fatalf("handback: %v", err)
	}
	if handback.ResultSoFar != "parent result" {
		t.Errorf("result_so_far = %q, want parent result", handback.ResultSoFar)
	}
	if len(handback.OpenQuestions) != 1 || handback.OpenQuestions[0] != "Which region?" {
		t.Errorf("open_questions = %#v, want [Which region?]", handback.OpenQuestions)
	}
}

func TestSubagentLifecycleFrames_StartQueuedRunningTerminalEndOrder(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	rec := launchRunningChild(t, al, parentID, "call-frame-order")
	al.deliverSubagentState(parentID, rec, string(session.LifecycleRunning))
	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: "done"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}

	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript(parent): %v", err)
	}
	var got []string
	for _, entry := range entries {
		switch entry.SystemSubtype {
		case session.SystemSubtypeSubagentStart:
			got = append(got, "start")
		case session.SystemSubtypeSubagentState:
			got = append(got, entry.SubagentState.State)
		case session.SystemSubtypeSubagentEnd:
			got = append(got, "end")
		}
	}
	want := []string{"start", "queued", "running", "completed", "end"}
	if len(got) != len(want) {
		t.Fatalf("frame order = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame order = %#v, want %#v", got, want)
		}
	}
}

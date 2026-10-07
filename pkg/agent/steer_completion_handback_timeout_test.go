package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Issue #1215 (founder decision 2026-10-07, option 1): a final handback
// delivered in THIS execution makes a later timeout end the helper as
// completed with that result — never a second timeout stop/notice.

func admitExecution(t *testing.T, al *AgentLoop, id, runID string) *session.LifecycleRecord {
	t.Helper()
	if err := al.GetSessionLifecycleStore().Mutate(id, func(r *session.LifecycleRecord) error {
		r.ExecutionID = &session.ExecutionIdentity{RunID: runID, BootSeq: al.bootEpochFor()}
		r.State = session.LifecycleRunning
		return nil
	}); err != nil {
		t.Fatalf("admit execution %s: %v", runID, err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return rec
}

func sendFinalHandback(t *testing.T, al *AgentLoop, childID, result string) {
	t.Helper()
	tool := tools.NewMessageParentTool(al.getUpwardDeliverer(), al.GetSessionLifecycleStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	res := tool.Execute(ctx, map[string]any{"kind": "handback", "mode": "final", "result_so_far": result})
	if res.IsError {
		t.Fatalf("message_parent handback: %s", res.ForLLM)
	}
}

// parentNoticeTexts returns every child-authored message text in the parent's inbox.
func parentNoticeTexts(t *testing.T, al *AgentLoop, parent, child string) []string {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(parent)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	var texts []string
	for _, e := range entries {
		if e.Kind != session.InboxEntryMessage || e.Message == nil {
			continue
		}
		raw, _ := e.Message.MarshalJSON()
		var f map[string]any
		if json.Unmarshal(raw, &f) != nil || f["session_id"] != child {
			continue
		}
		texts = append(texts, string(raw))
	}
	return texts
}

func mustLoadRec(t *testing.T, al *AgentLoop, id string) *session.LifecycleRecord {
	t.Helper()
	rec, err := al.GetSessionLifecycleStore().Load(id)
	if err != nil {
		t.Fatalf("Load(%s): %v", id, err)
	}
	return rec
}

func TestTimeoutAfterFinalHandback_CompletesWithHandbackResultAndNoTimeoutNotice(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parent := newTestSteeringSession(t, al, "ws-1215-a")
	child := u1LaunchChild(t, al, parent, "t1215-a")
	wireSteerCompletionDeps(t, al)
	rec := admitExecution(t, al, child.SessionID, "run-a")

	sendFinalHandback(t, al, child.SessionID, "RESULT-MARKER-1215")
	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{}, context.DeadlineExceeded); err != nil {
		t.Fatalf("completeSteeredTurn(deadline): %v", err)
	}

	got := mustLoadRec(t, al, child.SessionID)
	if got.State != session.LifecycleCompleted {
		t.Fatalf("handed-back child after a timeout = %q (stop note %+v), want completed", got.State, got.StopNote)
	}
	for _, text := range parentNoticeTexts(t, al, parent, child.SessionID) {
		if strings.Contains(text, "stopped_child") || strings.Contains(text, "cause: timeout") || strings.Contains(text, "timeout:") {
			t.Errorf("parent received a timeout/stopped notice for a handed-back child: %s", text)
		}
	}
	if !strings.Contains(strings.Join(parentNoticeTexts(t, al, parent, child.SessionID), "\n"), "RESULT-MARKER-1215") {
		t.Errorf("the handback result never reached the parent")
	}
}

func TestTimeoutWithoutHandback_StaysStoppedTimeout(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parent := newTestSteeringSession(t, al, "ws-1215-b")
	child := u1LaunchChild(t, al, parent, "t1215-b")
	wireSteerCompletionDeps(t, al)
	rec := admitExecution(t, al, child.SessionID, "run-b")

	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{}, context.DeadlineExceeded); err != nil {
		t.Fatalf("completeSteeredTurn(deadline): %v", err)
	}
	assertU1StoppedChildNotice(t, al, parent, rec, string(session.StopCauseTimeout), "")
}

func TestResumeAfterTimeoutThenHandback_CompletesAndOldHandbackDoesNotLeak(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parent := newTestSteeringSession(t, al, "ws-1215-c")
	child := u1LaunchChild(t, al, parent, "t1215-c")
	wireSteerCompletionDeps(t, al)

	// Execution 1 hands back, then is stopped by something other than the
	// deadline path (a plain cancel), so its mark must NOT cover execution 2.
	rec1 := admitExecution(t, al, child.SessionID, "run-c1")
	sendFinalHandback(t, al, child.SessionID, "FIRST-RUN-RESULT")
	if err := al.completeSteeredTurn(context.Background(), rec1, turnResult{}, context.Canceled); err != nil {
		t.Fatalf("completeSteeredTurn(cancel): %v", err)
	}
	if st := mustLoadRec(t, al, child.SessionID).State; st != session.LifecycleStopped {
		t.Fatalf("precondition: cancelled execution = %q, want stopped", st)
	}
	if _, err := al.steerCanceller().Revive(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
		t.Fatalf("Revive: %v", err)
	}

	// Execution 2 times out WITHOUT its own handback: stopped(timeout).
	rec2 := admitExecution(t, al, child.SessionID, "run-c2")
	if err := al.completeSteeredTurn(context.Background(), rec2, turnResult{}, context.DeadlineExceeded); err != nil {
		t.Fatalf("completeSteeredTurn(deadline, no handback in this execution): %v", err)
	}
	if got := mustLoadRec(t, al, child.SessionID); got.State != session.LifecycleStopped || got.StopNote == nil || got.StopNote.Cause != session.StopCauseTimeout {
		t.Fatalf("execution 2 without its own handback = %q %+v, want stopped(timeout): an earlier execution's handback must not cover it", got.State, got.StopNote)
	}

	// Resume after the timeout, hand back in the new execution, time out again.
	if _, err := al.steerCanceller().Revive(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}); err != nil {
		t.Fatalf("Revive after timeout: %v", err)
	}
	rec3 := admitExecution(t, al, child.SessionID, "run-c3")
	sendFinalHandback(t, al, child.SessionID, "RESUMED-RESULT")
	if err := al.completeSteeredTurn(context.Background(), rec3, turnResult{}, context.DeadlineExceeded); err != nil {
		t.Fatalf("completeSteeredTurn(deadline after handback): %v", err)
	}
	if got := mustLoadRec(t, al, child.SessionID); got.State != session.LifecycleCompleted {
		t.Fatalf("resumed child that handed back then timed out = %q, want completed", got.State)
	}
}

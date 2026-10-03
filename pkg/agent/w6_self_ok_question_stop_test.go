// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6 RED — ADR-20260928 D1.7 / F0929-R2-Q2=A, self_ok half.
//
// Stopping a child that is waiting on a self_ok question lands it stopped
// and keeps that question: the original correlation and the original
// 24-hour TTLDeadline stay durable across a fresh store open. NeedsInput
// itself is cleared outside needs_input (persistLocked). An authorized
// parent respond then delivers that exact answer once, through the existing
// respond path, and resumes only the child — even when the parent is also
// stopped.
//
// Oracles are the ADR, not the current stop writer. The deadline check
// reads the fresh lifecycle tail (Load, then its JSON), not an older
// history line and not a made-up question type.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	w6SelfOKCorrelation = "w6-self-ok-staging"
	w6SelfOKAnswer      = "yes, use the staging key"
)

// w6ResumeRecorder is the turn-start edge only. The answer text is written
// to the real session store before Dispatch; this records that the child
// was resumed and does not start a model turn.
type w6ResumeRecorder struct {
	calls []w6ResumeCall
}

type w6ResumeCall struct {
	sessionID  string
	generation int
}

func (r *w6ResumeRecorder) Launch(context.Context, steer.LaunchRequest) (steer.LaunchResult, error) {
	return steer.LaunchResult{}, fmt.Errorf("respond must resume the parked child, not launch another session")
}

func (r *w6ResumeRecorder) Dispatch(_ context.Context, sessionID string, generation int) (steer.DispatchResult, error) {
	r.calls = append(r.calls, w6ResumeCall{sessionID: sessionID, generation: generation})
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: generation}, nil
}

// TestW6SelfOKQuestion_SurvivesStop_RespondResumesChildWhileParentStaysStopped
// parks a self_ok question through message_parent, stops the parent and the
// child with SteerCanceller.StopTurns, and requires the question to survive
// a fresh store open so the parent's respond can resume only the child.
func TestW6SelfOKQuestion_SurvivesStop_RespondResumesChildWhileParentStaysStopped(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	root := newTestSteeringSession(t, al, "ws-w6-self-ok")
	parent := u1LaunchChild(t, al, root, "w6-self-ok-parent")
	child := u1LaunchChild(t, al, parent.SessionID, "w6-self-ok-child")
	corr, deadline, generation := w6ParkSelfOKQuestion(t, al, child.SessionID)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopTurns(t, al, parent.SessionID, by)
	w6StopTurns(t, al, child.SessionID, by)

	freshLC, freshInbox := w6ReopenMessagingStores(t, al)
	stoppedChild := w6MustLoad(t, freshLC, child.SessionID)
	stoppedParent := w6MustLoad(t, freshLC, parent.SessionID)
	questionKept := w6StoppedRecordKeepsQuestion(t, stoppedChild, corr, deadline)

	delegate, resumes := w6ParentRespondTool(t, al, freshLC, freshInbox)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)
	first := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6SelfOKAnswer))
	want := w6SelfOKDelivery(corr, w6SelfOKAnswer)
	delivered := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, want)
	afterChild := w6MustLoad(t, freshLC, child.SessionID)
	afterParent := w6MustLoad(t, freshLC, parent.SessionID)
	resumedOnce := w6ChildResumedOnce(first, resumes, child.SessionID, generation, delivered, afterChild, afterParent, stoppedParent.Generation)

	if stoppedChild.State != session.LifecycleStopped || stoppedParent.State != session.LifecycleStopped ||
		stoppedChild.Generation != generation || !questionKept || !resumedOnce {
		t.Fatalf("self_ok question lost across Stop, or the authorized respond did not resume only the child (ADR D1.7): "+
			"child_state=%s parent_state=%s child_generation=%d want_generation=%d question_kept=%v "+
			"needs_input=%v respond_error=%v respond=%q deliveries=%d dispatches=%v "+
			"after_child=%s after_parent=%s want_deadline=%s",
			stoppedChild.State, stoppedParent.State, stoppedChild.Generation, generation, questionKept,
			stoppedChild.NeedsInput, first.IsError, first.ForLLM, delivered, resumes.calls,
			afterChild.State, afterParent.State, deadline.Format(time.RFC3339Nano))
	}

	second := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6SelfOKAnswer))
	if !second.IsError || w6CountTranscript(t, al.GetSessionStore(), child.SessionID, want) != 1 || len(resumes.calls) != 1 {
		t.Fatalf("self_ok answer must be delivered once: second respond error=%v text=%q deliveries=%d dispatches=%d",
			second.IsError, second.ForLLM, w6CountTranscript(t, al.GetSessionStore(), child.SessionID, want), len(resumes.calls))
	}
}

func w6ParkSelfOKQuestion(t *testing.T, al *AgentLoop, childID string) (string, time.Time, int) {
	t.Helper()
	tool := tools.NewMessageParentTool(al.getUpwardDeliverer(), al.GetSessionLifecycleStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	before := time.Now()
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	result := tool.Execute(ctx, map[string]any{
		"kind": "question", "text": "should I use the staging key?", "wait": true,
		"authority": "self_ok", "correlation_id": w6SelfOKCorrelation,
	})
	after := time.Now()
	if result.IsError {
		t.Fatalf("message_parent self_ok question: %s", result.ForLLM)
	}
	var resp generated.MessageParentResponse
	if err := json.Unmarshal([]byte(result.ForLLM), &resp); err != nil {
		t.Fatalf("message_parent response %q: %v", result.ForLLM, err)
	}
	if resp.CorrelationId == nil || *resp.CorrelationId != w6SelfOKCorrelation {
		t.Fatalf("message_parent correlation = %v, want %q", resp.CorrelationId, w6SelfOKCorrelation)
	}
	rec := w6MustLoad(t, al.GetSessionLifecycleStore(), childID)
	if rec.State != session.LifecycleNeedsInput || rec.NeedsInput == nil {
		t.Fatalf("park did not record needs_input: state=%s needs_input=%v", rec.State, rec.NeedsInput)
	}
	if rec.NeedsInput.CorrelationID != w6SelfOKCorrelation {
		t.Fatalf("parked correlation = %q, want %q", rec.NeedsInput.CorrelationID, w6SelfOKCorrelation)
	}
	earliest := before.Add(session.DefaultNeedsInputTTL)
	latest := after.Add(session.DefaultNeedsInputTTL)
	if rec.NeedsInput.TTLDeadline.Before(earliest) || rec.NeedsInput.TTLDeadline.After(latest) {
		t.Fatalf("parked TTLDeadline = %s, want the original 24h deadline in [%s, %s]",
			rec.NeedsInput.TTLDeadline.Format(time.RFC3339Nano), earliest.Format(time.RFC3339Nano), latest.Format(time.RFC3339Nano))
	}
	return rec.NeedsInput.CorrelationID, rec.NeedsInput.TTLDeadline, rec.Generation
}

func w6StopTurns(t *testing.T, al *AgentLoop, sessionID string, by steer.Principal) {
	t.Helper()
	canceller := NewSteerCanceller(al.GetSessionLifecycleStore())
	if _, err := canceller.StopTurns(context.Background(), sessionID, by, false, al.SteerGenerationCancel); err != nil {
		t.Fatalf("StopTurns(%s): %v", sessionID, err)
	}
}

func w6ReopenMessagingStores(t *testing.T, al *AgentLoop) (*session.LifecycleStore, *session.MessageInboxStore) {
	t.Helper()
	home := al.GetConfig().Agents.Defaults.Home
	return session.NewLifecycleStore(al.GetSessionLifecycleStore().Dir()),
		session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
}

func w6MustLoad(t *testing.T, lc *session.LifecycleStore, sessionID string) *session.LifecycleRecord {
	t.Helper()
	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load(%s): %v", sessionID, err)
	}
	if rec == nil {
		t.Fatalf("Load(%s): nil record", sessionID)
	}
	return rec
}

// w6StoppedRecordKeepsQuestion reports whether a fresh load of a stopped
// child still carries the original correlation and TTL instant. The check
// uses the loaded tail only, so an older needs_input history line cannot
// pass it. D1.7 keeps that payload even though NeedsInput is cleared.
func w6StoppedRecordKeepsQuestion(t *testing.T, rec *session.LifecycleRecord, corr string, deadline time.Time) bool {
	t.Helper()
	if rec == nil || rec.State != session.LifecycleStopped {
		return false
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal stopped record: %v", err)
	}
	encodedDeadline, err := json.Marshal(deadline)
	if err != nil {
		t.Fatalf("marshal original deadline: %v", err)
	}
	return bytes.Contains(raw, []byte(corr)) && bytes.Contains(raw, encodedDeadline)
}

func w6ParentRespondTool(t *testing.T, al *AgentLoop, lc *session.LifecycleStore, inbox *session.MessageInboxStore) (*tools.DelegateTool, *w6ResumeRecorder) {
	t.Helper()
	tool := tools.NewDelegateTool("", 0, 0)
	tool.SetLifecycleStore(lc)
	tool.SetMessageInbox(inbox)
	tool.SetSessionStore(al.GetSessionStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	resumes := &w6ResumeRecorder{}
	tool.SetSessionLauncher(resumes)
	return tool, resumes
}

func w6RespondArgs(sessionID, corr, answer string) map[string]any {
	return map[string]any{
		"action": "respond", "session_id": sessionID, "correlation_id": corr, "text": answer,
	}
}

// w6SelfOKDelivery is the existing respond path's instruction. D1.7 sends a
// self_ok answer down that path, so the child transcript must receive this
// exact text once.
func w6SelfOKDelivery(corr, answer string) string {
	return fmt.Sprintf("Answer to your question (correlation_id=%s): %s", corr, answer)
}

func w6CountTranscript(t *testing.T, store *session.UnifiedStore, sessionID, want string) int {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessionID, err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == want {
			count++
		}
	}
	return count
}

func w6ChildResumedOnce(result *tools.ToolResult, resumes *w6ResumeRecorder, childID string, generation, delivered int, child, parent *session.LifecycleRecord, parentGeneration int) bool {
	if result == nil || result.IsError || delivered != 1 || len(resumes.calls) != 1 {
		return false
	}
	call := resumes.calls[0]
	return call.sessionID == childID && call.generation == generation &&
		child.State == session.LifecycleRunning && child.Generation == generation &&
		parent.State == session.LifecycleStopped && parent.Generation == parentGeneration
}

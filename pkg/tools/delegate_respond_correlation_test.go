package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// seedOpenQuestion appends a question the child raised to its parent's inbox,
// the shape message_parent(kind=question) stores.
func seedOpenQuestion(t *testing.T, inbox *session.MessageInboxStore, parentKey, childID, correlationID string) {
	t.Helper()
	var msg generated.SessionMessage
	parent := parentKey
	if err := msg.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId: "q-" + childID + "-" + correlationID, SessionId: childID, ParentSessionId: &parent,
		CreatedAt: time.Now().UTC(), Depth: 1, UntrustedOrigin: true, SenderIdentity: "worker",
		Text: "may I proceed?", CorrelationId: correlationID,
	}); err != nil {
		t.Fatalf("encode question: %v", err)
	}
	if _, err := inbox.Append(parentKey, msg); err != nil {
		t.Fatalf("append question: %v", err)
	}
}

func seedRunningChild(t *testing.T, lc *session.LifecycleStore, id string) {
	t.Helper()
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: id, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}
}

func respondArgs(child, corr, text string) map[string]any {
	return map[string]any{"action": "respond", "session_id": child, "correlation_id": corr, "text": text}
}

// Founder decision 2026-10-07 (#1213): respond refuses an unknown correlation
// id and says how to correct it.
func TestDelegateRespond_UnknownCorrelationIDIsRefusedWithOpenIDs(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	seedRunningChild(t, lc, "child-u")
	seedOpenQuestion(t, inbox, "parent-1", "child-u", "corr-real")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	result := tool.Execute(ctx, respondArgs("child-u", "bogus-corr-id-xyz", "go"))
	if !result.IsError {
		t.Fatalf("respond with a never-issued correlation_id succeeded: %s", result.ForLLM)
	}
	for _, want := range []string{"unknown_correlation_id", "bogus-corr-id-xyz", "corr-real", `action="steer"`} {
		if !strings.Contains(result.ForLLM, want) {
			t.Errorf("refusal %q must contain %q so the caller can correct it", result.ForLLM, want)
		}
	}
	if msg, _ := steer.last(); msg.Content != "" {
		t.Errorf("a refused respond delivered %q to the child", msg.Content)
	}
}

func TestDelegateRespond_UnknownCorrelationIDWithNoOpenQuestionsSaysSo(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	seedRunningChild(t, lc, "child-n")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	result := tool.Execute(ctx, respondArgs("child-n", "corr-x", "go"))
	if !result.IsError || !strings.Contains(result.ForLLM, "unknown_correlation_id") ||
		!strings.Contains(result.ForLLM, "no open questions") || !strings.Contains(result.ForLLM, `action="steer"`) {
		t.Fatalf("want unknown_correlation_id, 'no open questions' and a steer hint, got: %s", result.ForLLM)
	}
}

func TestDelegateRespond_SecondAnswerIsAlreadyAnswered(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	seedRunningChild(t, lc, "child-a")
	seedOpenQuestion(t, inbox, "parent-1", "child-a", "corr-1")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	first := tool.Execute(ctx, respondArgs("child-a", "corr-1", "yes"))
	if first.IsError || !strings.Contains(first.ForLLM, `"acknowledged":true`) {
		t.Fatalf("a valid answer must still be acknowledged: %s", first.ForLLM)
	}
	if msg, _ := steer.last(); msg.Content != "yes" {
		t.Fatalf("valid answer content = %q, want %q", msg.Content, "yes")
	}
	second := tool.Execute(ctx, respondArgs("child-a", "corr-1", "again"))
	if !second.IsError {
		t.Fatalf("second answer to the same question succeeded: %s", second.ForLLM)
	}
	for _, want := range []string{"already_answered", "corr-1", `action="steer"`} {
		if !strings.Contains(second.ForLLM, want) {
			t.Errorf("refusal %q must contain %q", second.ForLLM, want)
		}
	}
	if msg, _ := steer.last(); msg.Content != "yes" {
		t.Errorf("the refused second answer reached the child: %q", msg.Content)
	}
}

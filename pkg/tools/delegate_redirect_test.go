// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// delegate_redirect_test.go — action="redirect" (ADR-20260928 D2 as amended by
// ADR-20261004): redirect REPLACES the target helper's current turn with the
// new instruction — it is not a bare stop, and it never cascades to
// descendants. One test per recipient disposition from executeRedirect's own
// state table (oracle: the state-table doc comment on delegate_redirect.go,
// not observed output):
//
//	working  → the redirecter is invoked with the named session and the exact
//	           replacement instruction (the agent side owns stop-then-deliver);
//	stopped  → the redirect is the resume: the reviver revives the SAME
//	           conversation and generation with the instruction as newest input;
//	done/failed → non-error, nothing started, pointing at action="resume";
//	3P       → named not_steerable, pointing at stop_all/resume.
package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// fakeRedirecterSink satisfies DelegateSteeringSink, steerRedirecter and
// steerReviver, recording which capability the redirect actually took — the
// outside view that tells "replaced the live turn" from "revived a stopped
// one" from "queued an ordinary message".
type fakeRedirecterSink struct {
	mu                sync.Mutex
	redirectCalled    bool
	redirectSessionID string
	redirectText      string
	reviveCalled      bool
	reviveSessionID   string
	reviveInstruction string
	queued            []providers.Message
}

func (f *fakeRedirecterSink) EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued = append(f.queued, msg)
	return "corr_test", nil
}

func (f *fakeRedirecterSink) RedirectSteeredSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redirectCalled = true
	f.redirectSessionID = sessionID
	f.redirectText = instruction
	return nil
}

func (f *fakeRedirecterSink) ReviveStoppedSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviveCalled = true
	f.reviveSessionID = sessionID
	f.reviveInstruction = instruction
	return true, nil
}

func seedRedirectChild(t *testing.T, lc *session.LifecycleStore, sessionID string, state session.LifecycleState, is3P bool) {
	t.Helper()
	seed := &session.LifecycleRecord{
		SessionID: sessionID, Generation: 2, State: state,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1", AgentID: "worker",
		Is3P: is3P,
	}
	if state == session.LifecycleStopped {
		seed.StopNote = &session.StopNote{
			At: time.Now().UTC(), By: "human:dan", Seq: 2, Cause: session.StopCauseStop,
		}
	}
	if err := lc.Persist(seed); err != nil {
		t.Fatalf("seed %s child: %v", state, err)
	}
}

// TestDelegateTool_Redirect_WorkingChild_ReplacesTheCurrentTurn pins the core
// rule: a redirect of a WORKING helper hands the named session and the exact
// replacement instruction to the redirecter — the current turn is being
// stopped AND replaced, never merely stopped, and nothing is queued as an
// ordinary steering message.
func TestDelegateTool_Redirect_WorkingChild_ReplacesTheCurrentTurn(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	sink := &fakeRedirecterSink{}
	tool.SetSteeringSink(sink)
	seedRedirectChild(t, lc, "child-redirect-working", session.LifecycleRunning, false)

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "redirect", "session_id": "child-redirect-working", "text": "drop that; summarise the release notes instead",
	})
	if result.IsError {
		t.Fatalf("redirect failed: %s", result.ForLLM)
	}
	sink.mu.Lock()
	called, gotSession, gotText, queued := sink.redirectCalled, sink.redirectSessionID, sink.redirectText, len(sink.queued)
	sink.mu.Unlock()
	if !called {
		t.Fatal("redirect of a working helper must invoke RedirectSteeredSession")
	}
	if gotSession != "child-redirect-working" {
		t.Errorf("RedirectSteeredSession session = %q, want the named child only — a redirect never cascades to descendants", gotSession)
	}
	if gotText != "drop that; summarise the release notes instead" {
		t.Errorf("RedirectSteeredSession instruction = %q, want the replacement instruction carried verbatim", gotText)
	}
	if queued != 0 {
		t.Errorf("a redirect is a turn replacement, not an ordinary steering message: %d messages were queued", queued)
	}
	if !strings.Contains(result.ForLLM, "stopped and replaced") {
		t.Errorf("redirect result = %q, want it to say the current turn is being stopped and replaced with the new instruction", result.ForLLM)
	}
}

// TestDelegateTool_Redirect_StoppedChild_RevivesSameConversation pins the
// stopped row: the redirect instruction IS the resume — the reviver revives
// the same session with the instruction as the newest input, and the redirecter
// (the stop half) is skipped.
func TestDelegateTool_Redirect_StoppedChild_RevivesSameConversation(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	sink := &fakeRedirecterSink{}
	tool.SetSteeringSink(sink)
	seedRedirectChild(t, lc, "child-redirect-stopped", session.LifecycleStopped, false)

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "redirect", "session_id": "child-redirect-stopped", "text": "carry on, but only the failing test",
	})
	if result.IsError {
		t.Fatalf("redirect of a stopped helper failed: %s", result.ForLLM)
	}
	sink.mu.Lock()
	revived, reviveSession, reviveText, redirected := sink.reviveCalled, sink.reviveSessionID, sink.reviveInstruction, sink.redirectCalled
	sink.mu.Unlock()
	if redirected {
		t.Error("the redirecter (stop half) must be skipped for a stopped helper — there is no live turn to stop")
	}
	if !revived {
		t.Fatal("redirect of a stopped helper must revive it through ReviveStoppedSession")
	}
	if reviveSession != "child-redirect-stopped" {
		t.Errorf("ReviveStoppedSession session = %q, want the SAME conversation", reviveSession)
	}
	if reviveText != "carry on, but only the failing test" {
		t.Errorf("ReviveStoppedSession instruction = %q, want the redirect instruction as the newest input", reviveText)
	}
	if !strings.Contains(result.ForLLM, "same conversation and generation") {
		t.Errorf("redirect result = %q, want it to say the helper was revived on the same conversation and generation", result.ForLLM)
	}
}

// TestDelegateTool_Redirect_FinishedChild_IsNonErrorPointingAtResume pins the
// done/failed row: there is no live turn to replace — the answer is a
// NON-ERROR that says so and points at action="resume" (an error here drives
// orchestrating agents into retry loops).
func TestDelegateTool_Redirect_FinishedChild_IsNonErrorPointingAtResume(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	// Plain steering sink: the terminal branch must return before any
	// capability assertion, so the absence of a redirecter must not matter.
	seedRedirectChild(t, lc, "child-redirect-done", session.LifecycleCompleted, false)

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "redirect", "session_id": "child-redirect-done", "text": "one more thing",
	})
	if result.IsError {
		t.Fatalf("redirect of a finished helper must be a non-error no-op, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "already finished") || !strings.Contains(result.ForLLM, `action="resume"`) {
		t.Errorf("redirect result = %q, want it to name the finished state and point at action=\"resume\"", result.ForLLM)
	}
}

// TestDelegateTool_Redirect_3PChild_NotSteerable pins the 3P row: an external
// command-line child has no steerable live turn to replace — a named
// not_steerable error pointing at stop_all/resume, with nothing delivered.
func TestDelegateTool_Redirect_3PChild_NotSteerable(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	sink := &fakeRedirecterSink{}
	tool.SetSteeringSink(sink)
	seedRedirectChild(t, lc, "child-redirect-3p", session.LifecycleRunning, true)

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "redirect", "session_id": "child-redirect-3p", "text": "focus",
	})
	if !result.IsError {
		t.Fatalf("redirect of an external-CLI child must be refused, got success: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "not_steerable") {
		t.Errorf("the refusal must be the named not_steerable result, got: %s", result.ForLLM)
	}
	for _, verb := range []string{"stop_all", "resume"} {
		if !strings.Contains(result.ForLLM, verb) {
			t.Errorf("the not_steerable refusal must point at %q, got: %s", verb, result.ForLLM)
		}
	}
	sink.mu.Lock()
	called, queued := sink.redirectCalled, len(sink.queued)
	sink.mu.Unlock()
	if called || queued != 0 {
		t.Errorf("a 3P redirect must deliver nothing (redirecter=%v, queued=%d)", called, queued)
	}
}

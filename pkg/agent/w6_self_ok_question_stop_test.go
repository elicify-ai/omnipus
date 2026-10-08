// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// W6, rewritten for ADR-20261004 ("Steering commands: no person question",
// locked decisions 5-7). The self_ok question park this file used to pin is
// gone: a helper question to its parent is an ORDINARY message — it parks
// nothing (no wait, no authority, no NeedsInput, no TTL) and the child keeps
// working. What survives — and what this file now pins, behaviorally, over
// REAL stores — is the durability and the answer path:
//
//   - the question message is durable in the parent's inbox across a Stop of
//     both helper and parent and a fresh store reopen;
//   - the parent's answer (delegate respond) to the STOPPED helper revives
//     the SAME conversation — through the real wired steering sink's
//     ReviveStoppedSession — writing the answer to the transcript the revived
//     turn reads, exactly once;
//   - only the child resumes; the parent stays stopped;
//   - a second respond to the now-working helper is another ordinary
//     message into its steering queue: no second revive, no second
//     transcript copy.
//
// The behavioral oracle is the real revive after a fresh store open: the
// exact answer lands once and only the child resumes.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
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

// w6ResumeRecorder is the launch edge only — and a negative control: respond
// must RESUME a stopped helper through the steering sink, never LAUNCH a new
// session, so Launch always fails and Dispatch (which nothing in the respond
// path may reach for a native child) records.
type w6ResumeRecorder struct {
	calls []w6ResumeCall
}

type w6ResumeCall struct {
	sessionID  string
	generation int
}

func (r *w6ResumeRecorder) Launch(context.Context, steer.LaunchRequest) (steer.LaunchResult, error) {
	return steer.LaunchResult{}, fmt.Errorf("respond must resume the stopped helper through the steering sink, not launch another session")
}

func (r *w6ResumeRecorder) Dispatch(_ context.Context, sessionID string, generation int) (steer.DispatchResult, error) {
	r.calls = append(r.calls, w6ResumeCall{sessionID: sessionID, generation: generation})
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: generation}, nil
}

// TestW6Question_OrdinaryMessage_AnswerRevivesStoppedChildWhileParentStaysStopped
// asks an ordinary question through message_parent, stops the parent and the
// child with SteerCanceller.StopTurns, and requires the question to survive a
// fresh store open so the parent's respond can revive only the child.
func TestW6Question_OrdinaryMessage_AnswerRevivesStoppedChildWhileParentStaysStopped(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)

	provider, _ := installParkedProvider(t, al)
	root := newTestSteeringSession(t, al, "ws-w6-self-ok")
	parent := g2HeldMessagingChild(t, al, provider, root, "w6-self-ok-parent")
	child := g2HeldMessagingChild(t, al, provider, parent.SessionID, "w6-self-ok-child")
	corr, askedGen := w6AskOrdinaryQuestion(t, al, child.SessionID)

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}
	w6StopTurns(t, al, parent.SessionID, by)
	w6StopTurns(t, al, child.SessionID, by)

	freshLC, freshInbox := w6ReopenMessagingStores(t, al)
	stoppedChild := w6MustLoad(t, freshLC, child.SessionID)
	stoppedParent := w6MustLoad(t, freshLC, parent.SessionID)
	// Durability: the ordinary question is still in the parent's inbox after
	// the stops and the fresh reopen.
	msgs, _, _, err := freshInbox.Drain(parent.SessionID, child.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain parent inbox after reopen: %v", err)
	}
	foundQuestion := false
	for _, msg := range msgs {
		if kind, kindErr := msg.Discriminator(); kindErr == nil && kind == "question" {
			if q, qerr := msg.AsSessionMessageQuestion(); qerr == nil && q.CorrelationId == corr {
				foundQuestion = true
			}
		}
	}
	if !foundQuestion {
		t.Fatalf("the child's question %s did not survive the Stop + fresh store reopen in the parent's inbox (%d messages)", corr, len(msgs))
	}

	delegate, launches := w6ParentRespondTool(t, al, freshLC, freshInbox)
	parentCtx := tools.WithTranscriptSessionID(context.Background(), parent.SessionID)
	first := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6SelfOKAnswer))
	want := w6SelfOKDelivery(corr, w6SelfOKAnswer)
	afterParent := w6MustLoad(t, freshLC, parent.SessionID)

	if stoppedChild.State != session.LifecycleStopped || stoppedParent.State != session.LifecycleStopped ||
		stoppedChild.Generation != askedGen {
		t.Fatalf("precondition: the stops must land both helpers stopped at the asked generation: child=(%s, %d) parent=%s want_gen=%d",
			stoppedChild.State, stoppedChild.Generation, stoppedParent.State, askedGen)
	}
	if first.IsError {
		t.Fatalf("the parent's answer to a stopped helper must revive it, got error: %s", first.ForLLM)
	}
	// The real revive wrote the answer where the revived turn reads it — the
	// transcript — exactly once (ReviveStoppedSession appends BEFORE the
	// generation moves).
	if delivered := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, want); delivered != 1 {
		t.Fatalf("the answer must reach the child's transcript exactly once, got %d copies", delivered)
	}
	if len(launches.calls) != 0 {
		t.Fatalf("respond must revive through the steering sink, never launch/dispatch a session: %+v", launches.calls)
	}
	if afterParent.State != session.LifecycleStopped || afterParent.Generation != stoppedParent.Generation {
		t.Fatalf("only the child resumes: parent = (%s, %d), want still stopped at %d",
			afterParent.State, afterParent.Generation, stoppedParent.Generation)
	}
	// Frozen control-plane ADR D2 CRIT-001: "resumes a stopped child on the
	// same generation ... only done/failed mints a next generation".
	// ADR-20261004 C1 preserves this rule for respond's ordinary message.
	waitFor(t, 10*time.Second, func() bool {
		rec := w6MustLoad(t, freshLC, child.SessionID)
		return rec.State != session.LifecycleStopped
	})
	resumed := w6MustLoad(t, freshLC, child.SessionID)
	if resumed.Generation != askedGen {
		t.Fatalf("revived child generation = %d, want %d — D2 requires same-generation stopped resume",
			resumed.Generation, askedGen)
	}
	if resumed.ExecutionID == nil || stoppedChild.ExecutionID == nil || resumed.ExecutionID.RunID == stoppedChild.ExecutionID.RunID || resumed.ExecutionID.BootSeq != al.bootEpochFor() {
		t.Fatalf("stopped resume must admit a fresh execution in this boot: stopped=%+v resumed=%+v", stoppedChild.ExecutionID, resumed.ExecutionID)
	}

	// A second respond to the SAME question is refused (founder decision
	// 2026-10-07, #1213: a question is answered once; a follow-up is an
	// ordinary steer) — no second transcript copy, no second revive.
	second := delegate.Execute(parentCtx, w6RespondArgs(child.SessionID, corr, w6SelfOKAnswer))
	if !second.IsError || !strings.Contains(second.ForLLM, "already_answered") {
		t.Fatalf("a second respond to an answered question must be refused as already_answered, got: %s", second.ForLLM)
	}
	if got := w6CountTranscript(t, al.GetSessionStore(), child.SessionID, want); got != 1 {
		t.Fatalf("the second (ordinary) respond must not write a second transcript copy: copies=%d", got)
	}
	if len(launches.calls) != 0 {
		t.Fatalf("no respond may ever launch a session: %+v", launches.calls)
	}
}

// w6AskOrdinaryQuestion sends a helper question to its parent through the
// production message_parent tool and pins the ordinary-message rule at the
// record level: no park, no NeedsInput, no generation, correlation id echoed.
func w6AskOrdinaryQuestion(t *testing.T, al *AgentLoop, childID string) (string, int) {
	t.Helper()
	tool := tools.NewMessageParentTool(al.getUpwardDeliverer(), al.GetSessionLifecycleStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	result := tool.Execute(ctx, map[string]any{
		"kind": "question", "text": "should I use the staging key?", "correlation_id": w6SelfOKCorrelation,
	})
	if result.IsError {
		t.Fatalf("message_parent question: %s", result.ForLLM)
	}
	var resp generated.MessageParentResponse
	if err := json.Unmarshal([]byte(result.ForLLM), &resp); err != nil {
		t.Fatalf("message_parent response %q: %v", result.ForLLM, err)
	}
	if resp.CorrelationId == nil || *resp.CorrelationId != w6SelfOKCorrelation {
		t.Fatalf("message_parent correlation = %v, want %q", resp.CorrelationId, w6SelfOKCorrelation)
	}
	rec := w6MustLoad(t, al.GetSessionLifecycleStore(), childID)
	if rec.State == session.LifecycleNeedsInput || rec.NeedsInput != nil {
		t.Fatalf("an ordinary question must not park the helper: state=%s needs_input=%v", rec.State, rec.NeedsInput)
	}
	return *resp.CorrelationId, rec.Generation
}

func w6StopTurns(t *testing.T, al *AgentLoop, sessionID string, by steer.Principal) {
	t.Helper()
	before := w6MustLoad(t, al.GetSessionLifecycleStore(), sessionID)
	result, err := al.StopSession(context.Background(), StopRequest{SessionID: sessionID, By: by})
	if err != nil || result.RootErr != nil || len(result.Report.Unreachable) != 0 {
		t.Fatalf("StopSession(%s) = %+v, %v", sessionID, result, err)
	}
	awaitSteeringRepairStopped(t, al, sessionID, before.Generation)
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

// w6ParentRespondTool wires the parent's delegate tool over the FRESH stores
// (the durability surface under test) with the REAL steering sink — the
// production delegateSteeringSink adapter around the AgentLoop — so the
// answer to a stopped helper takes the production ReviveStoppedSession path.
// The recorder launcher is the negative control: nothing in the respond path
// may launch a session.
func w6ParentRespondTool(t *testing.T, al *AgentLoop, lc *session.LifecycleStore, inbox *session.MessageInboxStore) (*tools.DelegateTool, *w6ResumeRecorder) {
	t.Helper()
	tool := tools.NewDelegateTool("", 0, 0)
	tool.SetLifecycleStore(lc)
	tool.SetMessageInbox(inbox)
	tool.SetSessionStore(al.GetSessionStore())
	tool.SetSessionMessagingEnabled(func() bool { return true })
	tool.SetSteeringSink(delegateSteeringSink{AgentLoop: al})
	resumes := &w6ResumeRecorder{}
	tool.SetSessionLauncher(resumes)
	return tool, resumes
}

func w6RespondArgs(sessionID, corr, answer string) map[string]any {
	return map[string]any{
		"action": "respond", "session_id": sessionID, "correlation_id": corr, "text": answer,
	}
}

// w6SelfOKDelivery is the production respond path's instruction framing
// (pkg/tools/delegate_respond.go::respondAnswerInstruction): the answer text
// carries the correlation id it answers, so the revived child can tie it to
// its own open message.
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

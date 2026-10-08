package tools

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

// slowSteeringSink widens the window between "check the id" and "deliver" so
// an unguarded check-then-deliver would let every concurrent respond through.
type slowSteeringSink struct {
	fakeSteeringSink
	delay time.Duration
}

func (s *slowSteeringSink) EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	time.Sleep(s.delay)
	return s.fakeSteeringSink.EnqueueSteeringMessage(scope, agentID, msg, correlationID)
}

func (f *fakeSteeringSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.delivered)
}

// #1213 review MUST 1: many concurrent responds to ONE open question deliver
// exactly once; every other caller is refused as already_answered.
func TestDelegateRespond_ConcurrentAnswersDeliverExactlyOnce(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	sink := &slowSteeringSink{delay: 30 * time.Millisecond}
	tool.SetSteeringSink(sink)
	seedRunningChild(t, lc, "child-race")
	seedOpenQuestion(t, inbox, "parent-1", "child-race", "corr-race")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	const callers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]*ToolResult, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = tool.Execute(ctx, respondArgs("child-race", "corr-race", "answer-"+strconv.Itoa(i)))
		}(i)
	}
	close(start)
	wg.Wait()

	ok, refused := 0, 0
	for _, r := range results {
		switch {
		case !r.IsError:
			ok++
		case strings.Contains(r.ForLLM, "already_answered"):
			refused++
		default:
			t.Errorf("unexpected refusal: %s", r.ForLLM)
		}
	}
	if ok != 1 || refused != callers-1 {
		t.Fatalf("successes=%d refused=%d, want 1 and %d", ok, refused, callers-1)
	}
	if n := sink.count(); n != 1 {
		t.Fatalf("the child received %d answers, want exactly 1", n)
	}
}

// A failed delivery gives the slot back: the id stays open and a retry works.
func TestDelegateRespond_FailedDeliveryLeavesQuestionOpenForRetry(t *testing.T) {
	const id = "child-retry"
	tool, lc, inbox, _ := newADR053TestTool(t)
	tool.SetSteeringSink(&failingReviverSink{err: errors.New("simulated revive failure")})
	seedStoppedChild(t, lc, id)
	seedOpenQuestion(t, inbox, "parent-1", id, "corr-retry")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	first := tool.Execute(ctx, respondArgs(id, "corr-retry", "yes"))
	if !first.IsError || strings.Contains(first.ForLLM, "already_answered") {
		t.Fatalf("first respond must fail on delivery, not on correlation: %s", first.ForLLM)
	}
	good := &fakeReviverSink{}
	tool.SetSteeringSink(good)
	retry := tool.Execute(ctx, respondArgs(id, "corr-retry", "yes"))
	if retry.IsError || !strings.Contains(retry.ForLLM, `"acknowledged":true`) {
		t.Fatalf("retry after a failed delivery must succeed: %s", retry.ForLLM)
	}
	if revived, _, _, _ := good.snapshot(); !revived {
		t.Fatal("the retry did not reach the child")
	}
}

type failRecordInbox struct{ *session.MessageInboxStore }

func (failRecordInbox) RecordAnswer(string, string, string) error {
	return errors.New("simulated disk full")
}

type failReserveInbox struct{ *session.MessageInboxStore }

func (failReserveInbox) ReserveAnswer(string, string, string) (session.CorrelationLookup, error) {
	return session.CorrelationLookup{}, errors.New("simulated inbox read failure")
}

// If recording fails AFTER a successful delivery the result must say the
// answer WAS delivered (not invite a retry), and a repeat is refused.
func TestDelegateRespond_RecordFailureAfterDeliveryTellsCallerNotToResend(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	tool.SetMessageInbox(failRecordInbox{inbox})
	seedRunningChild(t, lc, "child-rec")
	seedOpenQuestion(t, inbox, "parent-1", "child-rec", "corr-rec")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	first := tool.Execute(ctx, respondArgs("child-rec", "corr-rec", "yes"))
	if first.IsError {
		t.Fatalf("a delivered answer must not be reported as an error (it invites a re-send): %s", first.ForLLM)
	}
	for _, want := range []string{"WAS delivered", "do not send it again", `action="steer"`} {
		if !strings.Contains(first.ForLLM, want) {
			t.Errorf("result %q must contain %q", first.ForLLM, want)
		}
	}
	second := tool.Execute(ctx, respondArgs("child-rec", "corr-rec", "yes again"))
	if !second.IsError || !strings.Contains(second.ForLLM, "already_answered") {
		t.Fatalf("a repeat after an unrecorded delivery must be refused: %s", second.ForLLM)
	}
	if n := steer.count(); n != 1 {
		t.Fatalf("child received %d answers, want 1", n)
	}
}

func TestDelegateRespond_NoInboxRefuses(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	tool.SetMessageInbox(nil)
	seedRunningChild(t, lc, "child-noinbox")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	r := tool.Execute(ctx, respondArgs("child-noinbox", "c", "x"))
	if !r.IsError || !strings.Contains(r.ForLLM, "no message inbox configured") || steer.count() != 0 {
		t.Fatalf("respond without an inbox must refuse and deliver nothing: %s (delivered %d)", r.ForLLM, steer.count())
	}
}

func TestDelegateRespond_ResolveErrorRefusesAndDeliversNothing(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	tool.SetMessageInbox(failReserveInbox{inbox})
	seedRunningChild(t, lc, "child-resolve")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	r := tool.Execute(ctx, respondArgs("child-resolve", "c", "x"))
	if !r.IsError || !strings.Contains(r.ForLLM, "resolve correlation_id") || steer.count() != 0 {
		t.Fatalf("a lookup error must refuse visibly and deliver nothing: %s (delivered %d)", r.ForLLM, steer.count())
	}
}

// A blocker that carries a correlation id is answerable like a question.
func TestDelegateRespond_BlockerWithCorrelationIDIsAnswerable(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	seedRunningChild(t, lc, "child-blk")
	parent := "parent-1"
	corr := "corr-blk"
	var msg generated.SessionMessage
	if err := msg.FromSessionMessageBlocker(generated.SessionMessageBlocker{
		MessageId: "b1", SessionId: "child-blk", ParentSessionId: &parent, CreatedAt: time.Now().UTC(), Depth: 1,
		UntrustedOrigin: true, SenderIdentity: "worker", Text: "stuck", Severity: generated.SessionMessageBlockerSeverity("high"),
		CorrelationId: &corr,
	}); err != nil {
		t.Fatalf("encode blocker: %v", err)
	}
	if _, err := inbox.Append("parent-1", msg); err != nil {
		t.Fatalf("append blocker: %v", err)
	}
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if r := tool.Execute(ctx, respondArgs("child-blk", corr, "use B")); r.IsError {
		t.Fatalf("a correlated blocker must be answerable: %s", r.ForLLM)
	}
	if r := tool.Execute(ctx, respondArgs("child-blk", corr, "again")); !r.IsError || !strings.Contains(r.ForLLM, "already_answered") {
		t.Fatalf("second answer to the blocker: %s", r.ForLLM)
	}
}

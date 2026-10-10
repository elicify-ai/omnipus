package tools

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// NEW-10: delegate respond must show the calling model a fixed sentence for a
// store fault, never the storage path or the OS error text. The cause stays on
// the result's Err and in the gateway log at error level.

const respondLeakPath = "/var/lib/omnipus-secret-store/lifecycle/child.jsonl"

func respondLeakErr() error {
	return &fs.PathError{Op: "open", Path: respondLeakPath, Err: fs.ErrPermission}
}

type leakReserveInbox struct{ *session.MessageInboxStore }

func (leakReserveInbox) ReserveAnswer(string, string, string) (session.CorrelationLookup, error) {
	return session.CorrelationLookup{}, respondLeakErr()
}

type leakEnqueueSink struct{ fail bool }

func (s *leakEnqueueSink) EnqueueSteeringMessage(string, string, providers.Message, string) (string, error) {
	if s.fail {
		return "", respondLeakErr()
	}
	return "corr_ok", nil
}

// captureSlog routes the default logger into a buffer for the test.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func assertCuratedStoreFault(t *testing.T, r *ToolResult, logs *bytes.Buffer) {
	t.Helper()
	if !r.IsError {
		t.Fatalf("a store fault must be an error result: %s", r.ForLLM)
	}
	for _, leak := range []string{respondLeakPath, "omnipus-secret-store", "permission denied", "open /"} {
		if strings.Contains(r.ForLLM, leak) {
			t.Errorf("model-facing result leaks %q: %s", leak, r.ForLLM)
		}
	}
	if !strings.Contains(r.ForLLM, "retry") {
		t.Errorf("result must be actionable (say to retry): %s", r.ForLLM)
	}
	if !errors.Is(r.Err, fs.ErrPermission) {
		t.Errorf("the cause must stay reachable on the result's Err, got %v", r.Err)
	}
	logged := logs.String()
	if !strings.Contains(logged, respondLeakPath) || !strings.Contains(logged, "level=ERROR") {
		t.Errorf("the cause must be logged at error level, log was: %s", logged)
	}
}

func TestDelegateRespond_StoreFaultOnCorrelationLookupIsCurated(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	tool.SetMessageInbox(leakReserveInbox{inbox})
	seedRunningChild(t, lc, "child-leak-resolve")
	logs := captureSlog(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	r := tool.Execute(ctx, respondArgs("child-leak-resolve", "c", "x"))
	assertCuratedStoreFault(t, r, logs)
	if !strings.Contains(r.ForLLM, "resolve correlation_id") || steer.count() != 0 {
		t.Fatalf("must still say which step failed and deliver nothing: %s (delivered %d)", r.ForLLM, steer.count())
	}
}

func TestDelegateRespond_StoreFaultOnEnqueueIsCuratedAndQuestionStaysOpen(t *testing.T) {
	const id = "child-leak-enqueue"
	tool, lc, inbox, _ := newADR053TestTool(t)
	sink := &leakEnqueueSink{fail: true}
	tool.SetSteeringSink(sink)
	seedRunningChild(t, lc, id)
	seedOpenQuestion(t, inbox, "parent-1", id, "corr-leak")
	logs := captureSlog(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	assertCuratedStoreFault(t, tool.Execute(ctx, respondArgs(id, "corr-leak", "yes")), logs)

	sink.fail = false
	retry := tool.Execute(ctx, respondArgs(id, "corr-leak", "yes"))
	if retry.IsError || !strings.Contains(retry.ForLLM, `"acknowledged":true`) {
		t.Fatalf("the question must stay open for a retry after a failed delivery: %s", retry.ForLLM)
	}
}

func TestDelegateRespond_StoreFaultOnReviveIsCurated(t *testing.T) {
	const id = "child-leak-revive"
	tool, lc, inbox, _ := newADR053TestTool(t)
	tool.SetSteeringSink(&failingReviverSink{err: respondLeakErr()})
	seedStoppedChild(t, lc, id)
	seedOpenQuestion(t, inbox, "parent-1", id, "corr-leak-r")
	logs := captureSlog(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	assertCuratedStoreFault(t, tool.Execute(ctx, respondArgs(id, "corr-leak-r", "yes")), logs)
}

func TestDelegateRespond_StoreFaultOnRecordAnswerDoesNotLeakButSaysDelivered(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	tool.SetMessageInbox(leakRecordInbox{inbox})
	seedRunningChild(t, lc, "child-leak-rec")
	seedOpenQuestion(t, inbox, "parent-1", "child-leak-rec", "corr-leak-rec")
	logs := captureSlog(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	r := tool.Execute(ctx, respondArgs("child-leak-rec", "corr-leak-rec", "yes"))
	if r.IsError || !strings.Contains(r.ForLLM, "WAS delivered") || steer.count() != 1 {
		t.Fatalf("a delivered answer stays a non-error that says it was delivered: %s", r.ForLLM)
	}
	for _, leak := range []string{respondLeakPath, "permission denied"} {
		if strings.Contains(r.ForLLM, leak) {
			t.Errorf("model-facing result leaks %q: %s", leak, r.ForLLM)
		}
	}
	if !strings.Contains(logs.String(), respondLeakPath) {
		t.Errorf("the cause must stay in the log: %s", logs.String())
	}
}

type leakRecordInbox struct{ *session.MessageInboxStore }

func (leakRecordInbox) RecordAnswer(string, string, string) error { return respondLeakErr() }

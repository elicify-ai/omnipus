package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A5: amended ADR-20260928 D-E and WP-B FR-B-011/FR-B-013 require
// poll OR wake, never both. The provider drives the registered status tool
// in the very parent turn started by the actual bus hand-back wake.
func TestGate1Final_WakeThenStatusDoesNotRedeliver(t *testing.T) {
	f := newQAUATDelegateFixture(t, "status")
	wake := f.completeAndRetainWake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	response, err := f.al.processSystemMessage(ctx, wake)
	if err != nil || response != "Helper status checked." {
		t.Fatalf("real wake/status turn = (%q, %v), want completed provider reply", response, err)
	}
	requests := f.provider.Requests()
	// One helper request, one parent wake request, one parent tool-result request.
	if len(requests) != 3 {
		t.Fatalf("provider requests=%d, want exactly helper + wake + status result", len(requests))
	}
	wakeInput := qaUATLastUser(requests[1])
	status := qaUATPollResult(t, requests[2])
	wakeCopies := strings.Count(wakeInput, qaUATHelperFinal)
	statusCopies := strings.Count(status.Content, qaUATHelperFinal)
	if wakeCopies != 1 {
		t.Errorf("positive delivery control: real wake contains final %d times, want exactly 1; input=%q", wakeCopies, wakeInput)
	}
	if total := wakeCopies + statusCopies; total != 1 {
		t.Errorf("A5: final effectively delivered %d times (wake=%d, real delegate status=%d), want exactly 1; status=%q", total, wakeCopies, statusCopies, status.Content)
	}
	if !strings.HasPrefix(status.Content, "completed,") || !strings.Contains(strings.ToLower(status.Content), "final already delivered") {
		t.Errorf("A5: consumed-final status=%q, want completed / final already delivered without final text", status.Content)
	}

	pending, _, more, drainErr := f.al.GetMessageInboxStore().Drain(f.parentID, f.child.SessionID, "", 10)
	if drainErr != nil || more || len(pending) != 0 {
		t.Errorf("wake-consumed final still pending: count=%d more=%v err=%v, want zero", len(pending), more, drainErr)
	}
	entries := qaReceiptReopenTranscript(t, f.al.GetSessionStore(), f.parentID)
	consumed := 0
	for _, entry := range entries {
		if entry.ID == "consumed-"+wake.Metadata["steer_message_id"] && entry.Content == "consumed "+wake.Metadata["steer_message_id"] {
			consumed++
		}
	}
	if consumed != 1 {
		t.Errorf("durable final-consumption markers=%d, want exactly 1 under the real shared final identity", consumed)
	}
	latest, latestErr := f.al.GetMessageInboxStore().Latest(f.parentID, f.child.SessionID)
	if latestErr != nil || latest == nil {
		t.Fatalf("saved result/history disappeared after consumption: latest=%+v err=%v", latest, latestErr)
	}
	handback, decodeErr := latest.AsSessionMessageHandback()
	if decodeErr != nil || handback.ResultSoFar != qaUATHelperFinal {
		t.Errorf("saved final/history=%+v err=%v, want unchanged exact final", handback, decodeErr)
	}
	beforeReplay := len(requests)
	response, err = f.al.processSystemMessage(ctx, wake)
	if err != nil || response != "" || len(f.provider.Requests()) != beforeReplay {
		t.Errorf("same wake replay=(%q, %v), requests=%d; want no additional effective delivery/turn", response, err, len(f.provider.Requests()))
	}
}

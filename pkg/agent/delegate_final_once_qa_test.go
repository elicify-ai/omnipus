package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// D5 / ADR-091 D3, WP-B US-2 + FR-B-002 + SC-B-2.
// User expectation: each helper final reaches the parent exactly once, by a
// delegate poll OR a hand-back wake. The UI must not get a redundant answer
// caused by supplying the same final through both paths.
func TestQADelegateFinal_PollAndWakeDeliverExactlyOnce(t *testing.T) {
	for _, action := range []string{"status", "inbox"} {
		t.Run(action, func(t *testing.T) {
			f := newQAUATDelegateFixture(t, action)
			wake := f.completeAndRetainWake(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			message := rootHumanMessage(f.parentID)
			message.Content = "Check the helper status once."
			response, _, err := f.al.processMessage(ctx, message)
			if err != nil || response != "Helper status checked." {
				t.Fatalf("SETUP: real parent poll turn = (%q, %v), want the provider's completed poll reply", response, err)
			}
			beforeWake := f.provider.Requests()
			if len(beforeWake) != 3 { // One helper call, then poll request and real tool-result request.
				t.Fatalf("SETUP: provider calls before wake = %d, want helper + parent poll + parent tool result", len(beforeWake))
			}
			pollResult := qaUATPollResult(t, beforeWake[2])
			if action == "status" && !strings.HasPrefix(pollResult.Content, "completed,") {
				t.Fatalf("real delegate status does not report the completed helper's lifecycle state: %q", pollResult.Content)
			}
			if action == "inbox" {
				var inbox generated.DelegateInboxResponse
				if decodeErr := json.Unmarshal([]byte(pollResult.Content), &inbox); decodeErr != nil {
					t.Fatalf("real delegate inbox response cannot be decoded as the generated inbox response: %q: %v", pollResult.Content, decodeErr)
				}
			}
			pollReceipts := strings.Count(pollResult.Content, qaUATHelperFinal)

			// This is the actual bus-published message, not a constructed wake.
			// It was held only to select poll-before-wake, as in the UAT defect.
			_, err = f.al.processSystemMessage(ctx, wake)
			if err != nil {
				t.Fatalf("real delayed hand-back wake failed instead of consuming/deduplicating: %v", err)
			}
			afterWake := f.provider.Requests()
			wakeCalls := len(afterWake) - len(beforeWake)
			if wakeCalls < 0 || wakeCalls > 1 {
				t.Fatalf("delayed wake made %d new parent provider calls, want at most one", wakeCalls)
			}
			wakeReceipts := 0
			if wakeCalls == 1 {
				wakeReceipts = strings.Count(qaUATLastUser(afterWake[len(beforeWake)]), qaUATHelperFinal)
			}
			if total := pollReceipts + wakeReceipts; total != 1 {
				t.Errorf("D5: helper final reached the parent %d times (delegate %s poll=%d, delayed hand-back wake=%d; extra parent model calls=%d); want exactly 1 — poll OR wake, never both and never neither", total, action, pollReceipts, wakeReceipts, wakeCalls)
			}

			// Delayed/replayed transport must not redeliver the final yet again.
			_, err = f.al.processSystemMessage(ctx, wake)
			if err != nil || len(f.provider.Requests()) != len(afterWake) {
				t.Errorf("replaying the same real wake added a parent turn: calls=%d after first=%d error=%v", len(f.provider.Requests()), len(afterWake), err)
			}
		})
	}
}

func qaUATPollResult(t *testing.T, request []providers.Message) providers.Message {
	t.Helper()
	var results []providers.Message
	for _, message := range request {
		if message.Role == "tool" && message.ToolCallID == qaUATPollCallID {
			results = append(results, message)
		}
	}
	if len(results) != 1 {
		t.Fatalf("SETUP: actual parent model input has %d results for its real delegate poll call, want exactly 1", len(results))
	}
	// Prove the parent saw a successful real tool result, not a denied/fake call.
	if strings.Contains(results[0].Content, "Error:") || strings.Contains(results[0].Content, "denied") {
		t.Fatalf("SETUP: parent delegate poll failed instead of exercising D5: %q", results[0].Content)
	}
	return results[0]
}

func qaUATLastUser(messages []providers.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// Positive instrument control, not a claim that D5 was already fixed:
// an unpolled hand-back MUST reach the parent. Suppressing every wake would
// manufacture a green for the negative ordering cases but fail this control.
// WP-B US-2/AS-1 and AS-6: one real consumption, zero additional on replay.
func TestQADelegateFinal_UnpolledWakeDeliversExactlyOnce(t *testing.T) {
	f := newQAUATDelegateFixture(t, "")
	wake := f.completeAndRetainWake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	response, err := f.al.processSystemMessage(ctx, wake)
	if err != nil || response != "Helper status checked." {
		t.Fatalf("unpolled real hand-back turn = (%q, %v), want the provider's completed reply", response, err)
	}
	requests := f.provider.Requests()
	if len(requests) != 2 { // Helper + exactly one parent wake, not just absence of duplication.
		t.Fatalf("unpolled hand-back provider calls = %d, want helper + exactly one parent turn", len(requests))
	}
	if got := strings.Count(qaUATLastUser(requests[1]), qaUATHelperFinal); got != 1 {
		t.Errorf("unpolled final supplied to parent %d times, want exactly 1; parent input=%q", got, qaUATLastUser(requests[1]))
	}
	pending, _, more, err := f.al.GetMessageInboxStore().Drain(f.parentID, f.child.SessionID, "", 10)
	if err != nil || more || len(pending) != 0 {
		t.Errorf("consumed final still pending: count=%d more=%v error=%v, want zero", len(pending), more, err)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(f.parentID)
	if err != nil {
		t.Fatalf("read real parent's transcript: %v", err)
	}
	consumed := 0
	for _, entry := range entries {
		if entry.Content == "consumed "+wake.Metadata["steer_message_id"] {
			consumed++
		}
	}
	if consumed != 1 {
		t.Errorf("durable consumed markers for this final = %d, want exactly 1", consumed)
	}
	response, err = f.al.processSystemMessage(ctx, wake)
	if err != nil || response != "" || len(f.provider.Requests()) != len(requests) {
		t.Errorf("same hand-back replay = (%q, %v), provider calls=%d; want empty success and unchanged %d calls", response, err, len(f.provider.Requests()), len(requests))
	}
}

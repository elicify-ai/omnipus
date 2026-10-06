package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
			// A later poll must not append a duplicate to the already-consumed
			// latest final (CHECK M08). Keep history, but one copy per status.
			qaUATAssertLaterPolls(t, f, action, wake.Metadata["steer_message_id"])
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
	// Force wake-first, then both real registered poll actions, including a
	// second already-consumed poll. The positive wake assertions above remain.
	for _, action := range []string{"status", "inbox"} {
		qaUATAssertLaterPolls(t, f, action, wake.Metadata["steer_message_id"])
	}
}

func qaUATAssertFinalConsumedOnce(t *testing.T, f *qaUATDelegateFixture, finalID string) {
	t.Helper()
	pending, _, more, err := f.al.GetMessageInboxStore().Drain(f.parentID, f.child.SessionID, "", 10)
	if err != nil || more || len(pending) != 0 {
		t.Errorf("D5: consumed final %q is still pending: count=%d more=%v error=%v", finalID, len(pending), more, err)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(f.parentID)
	if err != nil {
		t.Fatalf("read parent consumption identity: %v", err)
	}
	consumed := 0
	for _, entry := range entries {
		if entry.Content == "consumed "+finalID {
			consumed++
			if entry.ID != "consumed-"+finalID {
				t.Errorf("consumption marker has wrong identity %q for final %q", entry.ID, finalID)
			}
		}
	}
	if consumed != 1 {
		t.Errorf("D5: effective durable consumption of final %q = %d, want exactly one shared identity", finalID, consumed)
	}
}

func qaUATAssertLaterPolls(t *testing.T, f *qaUATDelegateFixture, action, finalID string) {
	t.Helper()
	qaUATAssertFinalConsumedOnce(t, f, finalID) // Positive consumption control BEFORE polling.
	ctx := tools.WithTranscriptSessionID(context.Background(), f.parentID)
	ctx = tools.WithAgentID(ctx, testDefaultAgentID)
	for poll := 1; poll <= 2; poll++ { // First already-consumed poll and an idempotence repeat.
		result := delegateToolFor(t, f.al).Execute(ctx, map[string]any{
			"action": action, "session_id": f.child.SessionID,
		})
		if result == nil || result.IsError {
			t.Fatalf("real later delegate %s poll %d failed: %+v", action, poll, result)
		}
		switch action {
		case "status":
			// D-E preserves saved history. A status may summarize that historical
			// final once; it must not append a second copy as a fresh delivery.
			if !strings.HasPrefix(result.ForLLM, "completed, "+qaUATHelperFinal+", ") || strings.Count(result.ForLLM, qaUATHelperFinal) != 1 || strings.Contains(result.ForLLM, "\n") {
				t.Errorf("D5: already-consumed status poll %d must show one historical final summary, never a duplicate: %q", poll, result.ForLLM)
			}
		case "inbox":
			var inbox generated.DelegateInboxResponse
			if err := json.Unmarshal([]byte(result.ForLLM), &inbox); err != nil {
				t.Fatalf("decode later real inbox poll %d: %v", poll, err)
			}
			if len(inbox.Messages) != 0 || inbox.HasMore {
				t.Errorf("D5: already-consumed inbox poll %d redelivered final: %+v, want no pending messages", poll, inbox)
			}
		default:
			t.Fatalf("unknown poll control %q", action)
		}
		qaUATAssertFinalConsumedOnce(t, f, finalID)
	}
}

// This barrier is ONLY at the paid-provider boundary: real tool-result
// delivery/consumption has already happened when a poll request is held here.
// No waker, ledger, acknowledgement or registered delegate path is replaced.
type qaUATParentBarrierProvider struct {
	inner       providers.LLMProvider
	order       string
	entered     chan []providers.Message
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func (p *qaUATParentBarrierProvider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, model string, opts map[string]any) (*providers.LLMResponse, error) {
	matches := p.order == "wake_first" && strings.Contains(qaUATLastUser(messages), qaUATHelperFinal)
	if p.order == "poll_first" {
		for _, message := range messages {
			if message.Role == "tool" && message.ToolCallID == qaUATPollCallID {
				matches = true
			}
		}
	}
	blocked := false
	if matches {
		p.enteredOnce.Do(func() {
			blocked = true
			p.entered <- append([]providers.Message(nil), messages...)
		})
	}
	if blocked {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release:
		}
	}
	return p.inner.Chat(ctx, messages, definitions, model, opts)
}

func (p *qaUATParentBarrierProvider) GetDefaultModel() string { return p.inner.GetDefaultModel() }
func (p *qaUATParentBarrierProvider) open()                   { p.releaseOnce.Do(func() { close(p.release) }) }

func TestQADelegateFinal_ConcurrentPollAndWakeShareConsumptionIdentity(t *testing.T) {
	for _, action := range []string{"status", "inbox"} {
		for _, order := range []string{"poll_first", "wake_first"} {
			t.Run(action+"/"+order, func(t *testing.T) {
				qaUATConcurrentFinalOrder(t, action, order)
			})
		}
	}
}

func qaUATConcurrentFinalOrder(t *testing.T, action, order string) {
	t.Helper()
	pollAction := action
	if order == "wake_first" {
		pollAction = ""
	}
	f := newQAUATDelegateFixture(t, pollAction)
	wake := f.completeAndRetainWake(t)
	barrier := &qaUATParentBarrierProvider{
		inner: f.provider, order: order, entered: make(chan []providers.Message, 1), release: make(chan struct{}),
	}
	parentAgent, ok := f.al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: real parent agent is not registered")
	}
	parentAgent.Provider = barrier
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	resultCh := make(chan rootHumanResult, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		barrier.open()
		cancel()
		select {
		case <-joined:
		case <-time.After(15 * time.Second):
			t.Error("concurrent final parent turn did not join during cleanup")
		}
	})
	go func() {
		defer close(joined)
		var response string
		var err error
		if order == "poll_first" {
			message := rootHumanMessage(f.parentID)
			message.Content = "Check the helper status once."
			response, _, err = f.al.processMessage(ctx, message)
		} else {
			response, err = f.al.processSystemMessage(ctx, wake)
		}
		resultCh <- rootHumanResult{response: response, err: err}
	}()
	var heldRequest []providers.Message
	select {
	case heldRequest = <-barrier.entered:
	case <-ctx.Done():
		t.Fatalf("SETUP: %s ordering never reached its actual parent provider barrier: %v", order, ctx.Err())
	}
	if order == "poll_first" {
		if got := strings.Count(qaUATPollResult(t, heldRequest).Content, qaUATHelperFinal); got != 1 {
			t.Errorf("positive poll-first delivery control = %d finals, want exactly one", got)
		}
		qaUATAssertFinalConsumedOnce(t, f, wake.Metadata["steer_message_id"])
		// Process the ACTUAL wake while the real poll turn is still in flight.
		before := len(f.provider.Requests())
		response, err := f.al.processSystemMessage(ctx, wake)
		if err != nil || response != "" || len(f.provider.Requests()) != before {
			t.Errorf("concurrent wake redelivered a polled final: response=%q error=%v provider calls=%d, want unchanged %d", response, err, len(f.provider.Requests()), before)
		}
	} else {
		if got := strings.Count(qaUATLastUser(heldRequest), qaUATHelperFinal); got != 1 {
			t.Errorf("positive wake-first delivery control = %d finals, want exactly one", got)
		}
		// Real registered polling now overlaps the held wake turn, after that
		// wake won the shared consumption identity but before its model reply.
		qaUATAssertLaterPolls(t, f, action, wake.Metadata["steer_message_id"])
	}
	barrier.open()
	select {
	case result := <-resultCh:
		if result.err != nil || result.response != "Helper status checked." {
			t.Fatalf("real %s parent turn = (%q, %v), want completed provider reply", order, result.response, result.err)
		}
	case <-ctx.Done():
		t.Fatalf("real %s parent turn never completed: %v", order, ctx.Err())
	}
	wantCalls := 3 // Helper + initial poll + actual tool-result model request.
	if order == "wake_first" {
		wantCalls = 2 // Helper + exactly one actual hand-back model request.
	}
	if got := len(f.provider.Requests()); got != wantCalls {
		t.Errorf("%s provider calls=%d, want exactly %d", order, got, wantCalls)
	}
	qaUATAssertLaterPolls(t, f, action, wake.Metadata["steer_message_id"])
	response, err := f.al.processSystemMessage(ctx, wake)
	if err != nil || response != "" || len(f.provider.Requests()) != wantCalls {
		t.Errorf("post-concurrency wake replay = (%q, %v), calls=%d, want empty success and unchanged %d", response, err, len(f.provider.Requests()), wantCalls)
	}
}

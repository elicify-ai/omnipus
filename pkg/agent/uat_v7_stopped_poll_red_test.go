package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Oracle: dispatch V7 (including no-poll control), validator/report-v.md::V7,
// and sub-agent control-plane D5/D6. Reading B's actual stopped notice in A's
// woken turn consumes it; queued B transport cannot create another parent turn.
// Conversely an unpolled B must still cause exactly one real parent turn.
func TestQAV7StoppedNotices_PollConsumesAndSuppressesSecondWake(t *testing.T) {
	for _, action := range []string{"status", "inbox", ""} {
		name := action
		if name == "" {
			name = "unpolled_control"
		}
		t.Run(name, func(t *testing.T) {
			f := newQA3StoppedFixture(t, action)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			qa3AssertStoppedNoticeLedger(t, f) // Exactly one notice per stopped transition before any read.
			response, err := f.al.processSystemMessage(ctx, f.wakes[0])
			if err != nil || response != qa3StoppedParentAnswer {
				t.Fatalf("SETUP: first real stop-notice parent turn = (%q, %v)", response, err)
			}
			beforeB := f.parent.Requests()
			wantFirstCalls := 1
			if action != "" {
				wantFirstCalls = 2 // Model poll request + model receives actual registered-tool results.
			}
			if len(beforeB) != wantFirstCalls || !strings.Contains(qaUATLastUser(beforeB[0]), "stopped_child:") {
				t.Fatalf("SETUP: first wake provider requests=%d, want %d with A's real stopped notice", len(beforeB), wantFirstCalls)
			}
			if action != "" {
				qa3AssertParentPolledBothStops(t, f, beforeB[1], action)
			}
			pendingB := qa3PendingStoppedMessages(t, f, f.children[1].SessionID)
			wantPendingB, wantConsumedB, wantAdditionalCalls := 0, 1, 0
			if action == "" {
				wantPendingB, wantConsumedB, wantAdditionalCalls = 1, 0, 1
			}
			if len(pendingB) != wantPendingB {
				t.Errorf("V7: after A's turn delegate %q left B's stopped notice pending=%d, want %d; a polled stop must be consumed like a final", action, len(pendingB), wantPendingB)
			}
			if got := qa3ConsumedMarkerCount(t, f, f.noticeID[1]); got != wantConsumedB {
				t.Errorf("V7: after A's turn B's durable consumed markers=%d, want %d (delegate %q)", got, wantConsumedB, action)
			}
			response, err = f.al.processSystemMessage(ctx, f.wakes[1])
			if err != nil {
				t.Fatalf("V7: B's retained REAL wake failed instead of consuming/deduplicating: %v", err)
			}
			afterB := f.parent.Requests()
			if got := len(afterB) - len(beforeB); got != wantAdditionalCalls {
				t.Errorf("V7: B's stopped notice caused %d extra parent model calls after delegate %q, want %d; poll OR wake, never both", got, action, wantAdditionalCalls)
			}
			if action == "" {
				if response != qa3StoppedParentAnswer || len(afterB) != 2 || !strings.Contains(qaUATLastUser(afterB[1]), "stopped_child:") {
					t.Errorf("V7 CONTROL: unpolled B must reach exactly one real parent turn: reply=%q requests=%d", response, len(afterB))
				}
			} else if response != "" {
				t.Errorf("V7: already-polled B wake returned %q, want empty deduplicated success", response)
			}
			for i, child := range f.children {
				if msgs := qa3PendingStoppedMessages(t, f, child.SessionID); len(msgs) != 0 {
					t.Errorf("V7: consumed helper %d notice remains pending: %+v", i, msgs)
				}
				if got := qa3ConsumedMarkerCount(t, f, f.noticeID[i]); got != 1 {
					t.Errorf("V7: helper %d notice has %d durable consumed markers, want exactly 1", i, got)
				}
				// Replayed transport, duplicate Stop and live/boot publisher retry
				// all traverse the real paths; none can duplicate the notice/work.
				if reply, err := f.al.processSystemMessage(ctx, f.wakes[i]); err != nil || reply != "" {
					t.Errorf("V7: replay of helper %d wake = (%q, %v), want empty success", i, reply, err)
				}
				stopCtx := tools.WithTranscriptSessionID(ctx, f.parentID)
				stopCtx = tools.WithAgentID(stopCtx, testDefaultAgentID)
				res := delegateToolFor(t, f.al).Execute(stopCtx, map[string]any{"action": "stop_all", "session_id": child.SessionID})
				if res == nil || res.IsError {
					t.Fatalf("V7: repeat real Stop failed: %+v", res)
				}
				if _, err := f.al.deliverLandedStopNotices(ctx, rootReopenedRecord(t, f.al, child.SessionID)); err != nil {
					t.Fatalf("V7: real landed-notice retry failed: %v", err)
				}
			}
			if got := len(f.parent.Requests()); got != len(afterB) {
				t.Errorf("V7 D6: replay added parent calls: %d, want unchanged %d", got, len(afterB))
			}
			qa3AssertStoppedNoticeLedger(t, f)
			select {
			case wake := <-f.al.bus.InboundChan():
				t.Errorf("V7 D6: acknowledged notice or duplicate Stop published another wake: %+v", wake)
			default:
			}
			f.helper.mu.Lock()
			helperCalls := f.helper.calls
			f.helper.mu.Unlock()
			if helperCalls != 2 { // Two helpers, one genuinely dispatched turn each; no implicit resume.
				t.Errorf("V7: helpers ran %d model calls, want exactly 2 original admissions", helperCalls)
			}
		})
	}
}

func qa3AssertParentPolledBothStops(t *testing.T, f *qa3StoppedFixture, request []providers.Message, action string) {
	t.Helper()
	for i, child := range f.children {
		callID := fmt.Sprintf("qa3-v7-poll-%d", i)
		var results []string
		for _, message := range request {
			if message.Role == "tool" && message.ToolCallID == callID {
				results = append(results, message.Content)
			}
		}
		if len(results) != 1 || strings.Contains(results[0], "Error:") || strings.Contains(results[0], "denied") {
			t.Fatalf("SETUP: parent's real delegate %s result for helper %d=%q; want one successful tool result", action, i, results)
		}
		if action == "status" {
			if !strings.HasPrefix(results[0], "stopped, stopped_child:") {
				t.Fatalf("SETUP: parent did not read helper %d's actual stopped state/notice: %q", i, results[0])
			}
			continue
		}
		var inbox generated.DelegateInboxResponse
		if err := json.Unmarshal([]byte(results[0]), &inbox); err != nil {
			t.Fatalf("SETUP: generated delegate inbox reply decode failed: %q: %v", results[0], err)
		}
		wantMessages := 0 // A was already consumed by its own first wake.
		if i == 1 {
			wantMessages = 1
		}
		if len(inbox.Messages) != wantMessages {
			t.Fatalf("SETUP: helper %d inbox messages=%d, want %d", i, len(inbox.Messages), wantMessages)
		}
		if i == 1 {
			notice, err := inbox.Messages[0].AsSessionMessageError()
			if err != nil || notice.MessageId != f.noticeID[i] || notice.SessionId != child.SessionID || !strings.HasPrefix(notice.Text, "stopped_child:") {
				t.Fatalf("SETUP: parent did not receive B's actual stopped notice through its registered inbox tool: %+v error=%v", notice, err)
			}
		}
	}
}

func qa3ConsumedMarkerCount(t *testing.T, f *qa3StoppedFixture, id string) int {
	t.Helper()
	entries, err := f.al.GetSessionStore().ReadTranscript(f.parentID)
	if err != nil {
		t.Fatalf("read real parent consumption transcript: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Content == "consumed "+id {
			count++
		}
	}
	return count
}

func qa3AssertStoppedNoticeLedger(t *testing.T, f *qa3StoppedFixture) {
	t.Helper()
	entries, err := f.al.GetMessageInboxStore().Entries(f.parentID)
	if err != nil {
		t.Fatalf("read real stop-notice ledger: %v", err)
	}
	var gotIDs []string
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		notice, err := entry.Message.AsSessionMessageError()
		if err != nil {
			t.Fatalf("V7: unexpected non-stop message from the two stopped helpers: %v", err)
		}
		gotIDs = append(gotIDs, notice.MessageId)
	}
	wantIDs := append([]string(nil), f.noticeID...)
	sort.Strings(gotIDs)
	sort.Strings(wantIDs)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("V7 D6: actual notice identities=%v, want exactly one per transition %v", gotIDs, wantIDs)
	}
	for _, child := range f.children {
		transitions, err := f.al.GetSessionLifecycleStore().ListStoppedTransitions(child.SessionID)
		if err != nil || len(transitions) != 1 {
			t.Errorf("V7 D6: duplicate Stop created transitions=%+v error=%v; want exactly 1", transitions, err)
		}
	}
}

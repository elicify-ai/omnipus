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
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// PW-D13 (founder 2026-10-15): neither helper Stop starts a parent turn.
// Keep V7's real registered-tool polling and no-poll control: an explicit later
// instruction may read both durable notices, but an unpolled Stop stays quiet.
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
			qa3AssertStoppedNoticeLedger(t, f) // Exactly one notice per transition before any read.
			if len(f.wakes) != 0 {
				t.Errorf("V7 PW-D13: two intentional Stops published %d wakes, want 0", len(f.wakes))
			}
			if calls := len(f.parent.Requests()); calls != 0 {
				t.Fatalf("V7 PW-D13: Stops caused %d parent model calls before an instruction, want 0", calls)
			}
			wantPending, wantConsumed, wantCalls := 1, 0, 0
			if action != "" {
				// PW-D13 permits an explicit later instruction, not a Stop wake.
				key := "agent:" + testDefaultAgentID + ":session:" + f.parentID
				response, _, err := f.al.processMessage(ctx, bus.InboundMessage{
					Channel: "webchat", ChatID: f.parentID, SessionKey: key, SessionID: f.parentID,
					Content: "Please check both stopped helpers.", UserInitiated: true,
					Metadata: map[string]string{"agent_id": testDefaultAgentID},
				})
				if err != nil || response != qa3StoppedParentAnswer {
					t.Fatalf("explicit parent polling turn = (%q, %v)", response, err)
				}
				requests := f.parent.Requests()
				wantCalls = 2 // One poll request, then the real registered-tool results.
				if len(requests) != wantCalls {
					t.Fatalf("explicit polling provider requests=%d, want %d", len(requests), wantCalls)
				}
				qa3AssertParentPolledBothStops(t, f, requests[1], action)
				wantPending, wantConsumed = 0, 1
			}
			afterB := f.parent.Requests()
			if len(afterB) != wantCalls {
				t.Errorf("V7: parent calls=%d, want only %d explicit-poll calls (PW-D13)", len(afterB), wantCalls)
			}
			for i, child := range f.children {
				if msgs := qa3PendingStoppedMessages(t, f, child.SessionID); len(msgs) != wantPending {
					t.Errorf("V7: helper %d pending notices=%d, want %d (poll=%q)", i, len(msgs), wantPending, action)
				}
				if got := qa3ConsumedMarkerCount(t, f, f.noticeID[i]); got != wantConsumed {
					t.Errorf("V7: helper %d consumed markers=%d, want %d (poll=%q)", i, got, wantConsumed, action)
				}
				// Duplicate Stop and real publisher retry cannot start a turn.
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
		// PW-D13 (founder 2026-10-15): no earlier Stop wake consumed either notice.
		if len(inbox.Messages) != 1 {
			t.Fatalf("helper %d inbox messages=%d, want exactly 1 before the explicit poll", i, len(inbox.Messages))
		}
		notice, err := inbox.Messages[0].AsSessionMessageError()
		if err != nil || notice.MessageId != f.noticeID[i] || notice.SessionId != child.SessionID || !strings.HasPrefix(notice.Text, "stopped_child:") {
			t.Fatalf("parent did not receive helper %d's actual notice through the registered inbox tool: %+v error=%v", i, notice, err)
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

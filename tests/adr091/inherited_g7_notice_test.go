package adr091_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// g7AssertDirectStopNotice checks the independent durable D6 publication,
// never treating the absence of a legacy terminal event as a delivered notice.
// Frozen D6: "Every steered child's transition into stopped ... persists one
// notice for its direct parent" with cause/actor/time and the decide offers.
// D2 CRIT-001: a Stop winner "writes no final outbox entry, appends no parent
// inbox message/frame" under the terminal final identity. The separate D6
// notice has its own (parent, child, generation, stop_seq) dedup identity.
func g7AssertDirectStopNotice(t *testing.T, inbox *session.MessageInboxStore, parentID string, stopped *session.LifecycleRecord) {
	t.Helper()
	if stopped.State != session.LifecycleStopped || stopped.StopNote == nil || stopped.Stop != nil || stopped.FinalDelivery != nil || stopped.FailedReason != "" {
		t.Fatalf("D2: Stop must be a landed resumable state, with no fence, failure or final outbox: %+v", stopped)
	}
	wantID := fmt.Sprintf("stopped-notice:%s:%s:%d:%d", parentID, stopped.SessionID, stopped.Generation, stopped.StopNote.Seq)
	finalID := fmt.Sprintf("%s:%d:final", stopped.SessionID, stopped.Generation)
	deadline := time.Now().Add(10 * time.Second) // existing E2E store-wait bound, not a latency oracle
	var notices []generated.SessionMessageError
	for {
		entries, err := inbox.Entries(parentID)
		if err != nil {
			t.Fatalf("Entries(direct parent's Stop notice): %v", err)
		}
		notices = nil
		for _, entry := range entries {
			if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
				continue
			}
			raw, err := entry.Message.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON(parent inbox message): %v", err)
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("decode parent inbox envelope: %v", err)
			}
			var messageID string
			if err := json.Unmarshal(envelope["message_id"], &messageID); err != nil {
				t.Fatalf("decode parent inbox message identity: %v", err)
			}
			if messageID == finalID {
				t.Fatalf("Stop consumed its generation's terminal final identity %q", finalID)
			}
			if messageID != wantID {
				continue
			}
			message, err := entry.Message.AsSessionMessageError()
			if err != nil {
				t.Fatalf("D6 notice %q must decode as the generated nonfatal message: %v", wantID, err)
			}
			notices = append(notices, message)
		}
		if len(notices) != 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(notices) != 1 {
		t.Fatalf("direct-parent notices for %q = %d, want exactly one durable D6 notice", wantID, len(notices))
	}
	got := notices[0]
	if got.MessageId != wantID || got.SessionId != stopped.SessionID || got.ParentSessionId == nil || *got.ParentSessionId != parentID || got.Generation == nil || *got.Generation != stopped.Generation || got.Fatal || !got.CreatedAt.Equal(stopped.StopNote.At) {
		t.Fatalf("D6 notice has the wrong identity, routing, generation, fatal flag or timestamp: %+v; want id=%q parent=%q at=%s", got, wantID, parentID, stopped.StopNote.At)
	}
	for _, want := range []string{string(stopped.StopNote.Cause), stopped.StopNote.By, stopped.StopNote.At.UTC().Format(time.RFC3339Nano), "resume", "redirect", "do the work", "report", "clear"} {
		if !strings.Contains(strings.ToLower(got.Text), strings.ToLower(want)) {
			t.Errorf("D6 notice %q does not identify %q", got.Text, want)
		}
	}
}

func g7SameStopNote(before, after *session.StopNote) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return before.At.Equal(after.At) && before.By == after.By && before.Seq == after.Seq && before.Cause == after.Cause && before.BootSeq == after.BootSeq
}

// g7AwaitOwnerSettled rejects a raw in-flight fence as a resume fixture.
// The 10s bound is the restart test's existing pre-Revive store-wait bound.
func g7AwaitOwnerSettled(t *testing.T, store *session.LifecycleStore, id string) *session.LifecycleRecord {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec, err := store.Load(id)
		if err != nil {
			t.Fatalf("Load(selected Stop owner): %v", err)
		}
		if rec.Terminal() || rec.State == session.LifecycleStopped {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("selected Stop owner did not settle before resume: state=%q fence=%+v", rec.State, rec.Stop)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// g7AssertSettledStopPublication preserves the genuine completion-winner
// oracle. Only stopped's retired interrupted-final oracle is superseded:
// frozen Vocabulary says "stopped ... alive and resumable"; D2/T11 exclude
// terminal finals and D6 requires the independently stored parent notice.
func g7AssertSettledStopPublication(t *testing.T, h *e2eHarness, rec *session.LifecycleRecord, when string) {
	t.Helper()
	if rec.State != session.LifecycleStopped {
		assertStopOutcomeMatchesState(t, h.upward, rec.SessionID, rec.State, when)
		return
	}
	if delivered := h.upward.outcomesFor(rec.SessionID); len(delivered) != 0 {
		t.Fatalf("%s: stopped child published legacy terminal upward outcomes %v; D2/T11 require none", when, delivered)
	}
	g7AssertDirectStopNotice(t, h.inbox, rec.SteeringSessionID(), rec)
}

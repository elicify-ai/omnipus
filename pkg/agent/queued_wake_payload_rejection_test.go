package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Lane A repair brief 1, frozen ADR D2 collision consistency and D4 #1000:
// an ID is an immutable accepted input, not permission to replace its payload.
// The exact conflict text is specified by the dispatch, not observed output.
// Proof-of-failability and independent CHECK are deferred to a fresh qa-lead.
func TestQueuedWakeSameID_ConflictingPayloadRefusedAndOriginalEntryPreserved(t *testing.T) {
	f := newQueuedWakeNegativeFixture(t)
	const (
		messageID = "queued-negative-same-id-payload"
		accepted  = "Accepted wake: keep the original evidence"
		conflict  = "Conflicting wake: discard the original evidence"
	)
	f.accept(t, messageID, accepted, f.generation)
	f.requirePending(t, f.owner, messageID, accepted)
	beforeRecord := rootReopenedRecord(t, f.al, f.childID)
	beforeQueue := queuedWakeNegativeQueue(f.al)
	beforeJournal := queuedWakeNegativeJournal(t, f)
	beforeTranscript, err := f.al.GetSessionStore().ReadTranscript(f.childID)
	if err != nil {
		t.Fatalf("read transcript before conflict: %v", err)
	}

	reply, conflictErr := f.al.processSteeredSystemWake(context.Background(), queuedWakeNegativeMessage(f.childID, f.generation, messageID, conflict))
	wantError := fmt.Sprintf("steer: queued wake %q conflicts with its accepted payload", messageID)
	if conflictErr == nil || conflictErr.Error() != wantError {
		t.Errorf("same-ID conflicting payload: error=%v, want exactly %q", conflictErr, wantError)
	}
	if reply != "" {
		t.Errorf("conflicting wake reply=%q, want no turn output", reply)
	}
	if after := rootReopenedRecord(t, f.al, f.childID); !reflect.DeepEqual(after, beforeRecord) {
		t.Errorf("conflicting retry changed the original durable admission: before=%+v after=%+v", beforeRecord, after)
	}
	if after := queuedWakeNegativeQueue(f.al); !queuedWakeNegativeQueuesEqual(after, beforeQueue) {
		t.Errorf("conflicting retry changed the accepted queue entry: before=%+v after=%+v", beforeQueue, after)
	}
	if after := queuedWakeNegativeJournal(t, f); !bytes.Equal(after, beforeJournal) {
		t.Error("conflicting retry appended or rewrote the original lifecycle journal")
	}
	afterTranscript, err := f.al.GetSessionStore().ReadTranscript(f.childID)
	if err != nil {
		t.Fatalf("read transcript after conflict: %v", err)
	}
	if !reflect.DeepEqual(afterTranscript, beforeTranscript) {
		t.Errorf("conflicting retry consumed or injected text: before=%+v after=%+v", beforeTranscript, afterTranscript)
	}

	// An original-payload retry after the refusal still coalesces: refusal must
	// not poison the accepted identity or turn the conflict into success later.
	f.accept(t, messageID, accepted, f.generation)
	f.requirePending(t, f.owner, messageID, accepted)
	f.requirePromotedOnce(t, f.owner, accepted, conflict)
}

// Positive instrument control: repair brief 3 and frozen ADR D4 explicitly
// preserve #1000's identical same-ID retry coalescing. Exact queue, input and
// real provider-call counts rule out unconditional refusal or payload loss.
func TestQueuedWakeSameID_IdenticalRetryCoalescesToOne(t *testing.T) {
	f := newQueuedWakeNegativeFixture(t)
	const (
		messageID = "queued-negative-identical-retry"
		content   = "Matching retry: inspect this evidence exactly once"
	)
	f.accept(t, messageID, content, f.generation)
	f.requirePending(t, f.owner, messageID, content)
	beforeRecord := rootReopenedRecord(t, f.al, f.childID)
	beforeQueue := queuedWakeNegativeQueue(f.al)
	f.accept(t, messageID, content, f.generation)
	f.requirePending(t, f.owner, messageID, content)
	afterRecord := rootReopenedRecord(t, f.al, f.childID)
	// D4 constrains identity and payload coalescing, not the write timestamp.
	// Compare every other field exactly; omit only UpdatedAt on both copies.
	beforeComparable, afterComparable := *beforeRecord, *afterRecord
	beforeComparable.UpdatedAt = time.Time{}
	afterComparable.UpdatedAt = time.Time{}
	if !reflect.DeepEqual(afterComparable, beforeComparable) {
		t.Errorf("matching retry changed substantive accepted state: before=%+v after=%+v", beforeRecord, afterRecord)
	}
	if after := queuedWakeNegativeQueue(f.al); !queuedWakeNegativeQueuesEqual(after, beforeQueue) {
		t.Errorf("matching retry added or changed an admission/input: before=%+v after=%+v", beforeQueue, after)
	}
	f.requirePromotedOnce(t, f.owner, content, "")
}

func queuedWakeNegativeJournal(t *testing.T, f *queuedWakeNegativeFixture) []byte {
	t.Helper()
	journal, err := os.ReadFile(filepath.Join(f.al.GetSessionLifecycleStore().Dir(), f.childID+".jsonl"))
	if err != nil {
		t.Fatalf("read recipient lifecycle journal: %v", err)
	}
	return journal
}

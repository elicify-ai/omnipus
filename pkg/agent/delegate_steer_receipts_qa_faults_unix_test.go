//go:build linux || darwin

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// B2/D4 negative ordering control: an actual failed transcript append cannot
// advance delivered or release the queue. Repair and redrive the real drain.
func TestQADelegateSteerReceipt_TranscriptFailureKeepsQueuedAndReturnsError(t *testing.T) {
	f := newQAReceiptFixture(t)
	const text = "Do not report me delivered without a durable transcript append."
	line := qaReceiptEnqueue(t, f, text, "qa-transcript-failure")
	path := filepath.Join(f.store.BaseDir(), f.child.SessionID, "transcript.jsonl")
	restore := qaReceiptDenyAppend(t, path)
	_, items, messages, ids, err := f.al.dequeueSteeringItemsForScopeWithFallbackResult(f.child.SessionID)
	var pathErr *os.PathError
	if !errors.Is(err, os.ErrPermission) || !errors.As(err, &pathErr) || pathErr.Path != path {
		t.Errorf("D4 transcript refusal = %v, want visible real permission/path error for %s", err, path)
	}
	if len(items) != 0 || len(messages) != 0 || len(ids) != 0 {
		t.Errorf("failed durable injection handed text to turn: items=%+v messages=%+v ids=%q, want empty prefix", items, messages, ids)
	}
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))[line.ControlID]
	if latest.State != "queued" || f.al.pendingSteeringCountForScope(f.child.SessionID) != 1 {
		t.Errorf("D4 failed injection released its receipt/queue: state=%q depth=%d, want queued/1", latest.State, f.al.pendingSteeringCountForScope(f.child.SessionID))
	}
	for _, entry := range qaReceiptReopenTranscript(t, f.store, f.child.SessionID) {
		if entry.Content == text {
			t.Errorf("denied injection nevertheless appeared durable: %+v", entry)
		}
	}
	restore()
	_, _, retryMessages, retryIDs, err := f.al.dequeueSteeringItemsForScopeWithFallbackResult(f.child.SessionID)
	if err != nil || !reflect.DeepEqual(retryMessages, []providers.Message{{Role: "user", Content: text}}) || !reflect.DeepEqual(retryIDs, []string{"qa-transcript-failure"}) {
		t.Errorf("repaired real drain lost accepted instruction: messages=%+v ids=%q err=%v", retryMessages, retryIDs, err)
	}
	qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{line.ControlID: "delivered"})
	matches := 0
	for _, entry := range qaReceiptReopenTranscript(t, f.store, f.child.SessionID) {
		if entry.Content == text {
			matches++
		}
	}
	if matches != 1 {
		t.Errorf("repair must durably inject exactly once: matching entries=%d", matches)
	}
}

// B3/D4 no orphan: a real ledger append fault during abandonment must keep
// the accepted item retryable, not log an error and delete its only queue copy.
func TestQADelegateSteerReceipt_AbandonReceiptFailureCannotOrphanAcceptance(t *testing.T) {
	f := newQAReceiptFixture(t)
	line := qaReceiptEnqueue(t, f, "Keep a durable receipt when abandonment persistence fails.", "qa-abandon-fault")
	restore := qaReceiptDenyAppend(t, qaReceiptLedgerPath(f.al, f.child.SessionID))
	f.al.abandonSteeredQueuedSteering(nil, f.child.SessionID, errors.New("fixture pre-turn failure"), 3)
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))[line.ControlID]
	if latest.State != "queued" || f.al.pendingSteeringCountForScope(f.child.SessionID) != 1 {
		t.Errorf("D4 orphan after refused receipt append: state=%q queue=%d, want queued receipt WITH retained item (queued/1), not log-only loss", latest.State, f.al.pendingSteeringCountForScope(f.child.SessionID))
	}
	restore()
	f.al.abandonSteeredQueuedSteering(nil, f.child.SessionID, errors.New("fixture repaired abandonment"), 3)
	qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{line.ControlID: "superseded"})
	if depth := f.al.pendingSteeringCountForScope(f.child.SessionID); depth != 0 {
		t.Errorf("repaired abandonment still has pending input: %d", depth)
	}
}

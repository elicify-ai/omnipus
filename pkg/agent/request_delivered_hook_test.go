package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// R2-S2-02 (combined review round 2): a HELD request that is finally delivered
// must move its sender's row on, once. ORACLE: the F1 ruling (the row shows
// "waiting" only while the request is held) and the MessageStatusFrame contract
// (a later state replaces held). A request that was never held, or that has no
// sender message id, reports nothing.

type deliveredRecorder struct {
	mu   sync.Mutex
	seen [][2]string
}

func (r *deliveredRecorder) hook(sessionID, messageID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, [2]string{sessionID, messageID})
}

func (r *deliveredRecorder) snapshot() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.seen...)
}

func admitWithSenderID(t *testing.T, f *addrFixture, asker, senderMessageID string) string {
	t.Helper()
	id, _, _, err := f.al.admitRequest(context.Background(), RequestAdmission{
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, Content: "are you there",
		Sender: addressing.Sender{Principal: "p"}, SenderLabel: "a person",
		Source:          addressing.Source{Kind: addressing.SourceConversation, SessionID: asker},
		SenderMessageID: senderMessageID,
	})
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	return id
}

func TestHeldRequestDelivery_MovesTheSendersRowOnOnce(t *testing.T) {
	f, recv, asker := heldSetup(t)
	rec := &deliveredRecorder{}
	f.al.SetRequestDeliveredHook(rec.hook)

	id := admitWithSenderID(t, f, asker, "cm-held-1")
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("nothing is reported while the request is held: %v", got)
	}

	f.al.markRequestDelivered(recv, id)
	f.al.markRequestDelivered(recv, id) // a second mark must not repeat it

	want := [][2]string{{asker, "cm-held-1"}}
	if got := rec.snapshot(); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("delivery must report the sender's row exactly once: got %v want %v", got, want)
	}
}

func TestRequestDelivery_NotHeldOrNoSenderIDReportsNothing(t *testing.T) {
	// Not held: a runnable receiver.
	f := newAddrFixture(t)
	wireLifecycle(t, f)
	rec := &deliveredRecorder{}
	f.al.SetRequestDeliveredHook(rec.hook)
	id := admitWithSenderID(t, f, "src-session", "cm-live-1")
	recvMain, _ := session.MainSessionID(addrWS, addrReceiver)
	f.al.markRequestDelivered(recvMain, id)
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("a request that was never held reports nothing: %v", got)
	}

	// Held but the sender has no message id (an agent sender).
	f2, recv2, asker2 := heldSetup(t)
	rec2 := &deliveredRecorder{}
	f2.al.SetRequestDeliveredHook(rec2.hook)
	id2 := admitWithSenderID(t, f2, asker2, "")
	f2.al.markRequestDelivered(recv2, id2)
	if got := rec2.snapshot(); len(got) != 0 {
		t.Fatalf("no sender message id, nothing to move on: %v", got)
	}
}

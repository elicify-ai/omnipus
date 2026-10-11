package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// S1 final-gate M2: Stop discards a person's queued message. If the "discarded"
// label cannot be written, the reply authority of an admitted request must STILL
// be revoked and the caller must STILL be told the message was discarded - the
// message left the queue either way.
func TestDiscardUndeliveredHumanInput_RevokesReplyAuthorityEvenWhenTheLabelWriteFails(t *testing.T) {
	f := newAddrFixture(t)
	sid := putConnectorCapture(t, f, "q1")
	// Make the label sidecar unwritable: a directory sits where the file goes.
	if err := os.MkdirAll(filepath.Join(f.al.GetSessionStore().BaseDir(), sid, "input_dispositions.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.al.enqueueHumanSteeringMessage(sid, addrReceiver, providers.Message{Role: "user", Content: "hello"}, "q1"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	out, err := f.al.discardUndeliveredHumanInput(sid)
	if err == nil {
		t.Fatal("the failed label write must be reported")
	}
	if len(out) != 1 || out[0].MessageID != "q1" {
		t.Fatalf("the discarded message must still be reported to the caller, got %+v", out)
	}
	c, ok, gerr := f.al.RequestLedger().Get(sid, "q1")
	if gerr != nil || !ok || !c.Discarded {
		t.Fatalf("reply authority of the discarded request must be revoked despite the label failure: capture=%+v ok=%v err=%v", c, ok, gerr)
	}
}

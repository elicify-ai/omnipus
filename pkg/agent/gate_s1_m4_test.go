package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
)

// S1 final-gate M4: an answer is saved and published even when the asker's wake
// cannot be sent, but the answerer must be told the asker was not woken (the old
// code returned a plain success and, for unwired deps, logged nothing at all).
func mainOwnerReply(t *testing.T, f *addrFixture) (string, error) {
	t.Helper()
	recv, _ := conversationSetup(t, f, addressing.Pair{})
	ownerMain, err := f.al.GetSessionStore().GetOrCreateMainSession(addrWS, "ann")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.al.RequestLedger().Resolve(recv, addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, "q7")
	if err != nil {
		t.Fatal(err)
	}
	c.Source.SessionID = ownerMain.ID
	c.RequestID = "q9"
	if err := f.al.RequestLedger().Put(c); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.r.Reply(context.Background(), f.replyReq(recv, "q9"))
	return receipt.Destination, err
}

func TestReply_OwnerWakeEligibilityUnavailable_AnswerSavedAndAnswererTold(t *testing.T) {
	f := newAddrFixture(t)
	f.deps.err = errors.New("membership store unavailable")
	before := wakeFailures.Load()
	dest, err := mainOwnerReply(t, f)
	if err != nil {
		t.Fatalf("the answer was saved, so the reply must still succeed: %v", err)
	}
	if !strings.Contains(dest, "could not be woken") {
		t.Fatalf("the answerer must be told the asker was not woken, got %q", dest)
	}
	if wakeFailures.Load() != before+1 {
		t.Fatal("the failed wake must be counted")
	}
	if in, woke := addrDrainInbound(t, f.bus); woke {
		t.Fatalf("no wake may be published: %+v", in)
	}
}

func TestReply_OwnerWakeEligible_ReportsPlainDelivery(t *testing.T) {
	f := newAddrFixture(t)
	f.deps.eligible[addrWS+"/ann"] = true
	dest, err := mainOwnerReply(t, f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dest, "could not be woken") {
		t.Fatalf("a successful wake must not be reported as failed: %q", dest)
	}
	if _, woke := addrDrainInbound(t, f.bus); !woke {
		t.Fatal("the eligible owner must be woken")
	}
}

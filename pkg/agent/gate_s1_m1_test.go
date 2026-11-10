package agent

import (
	"context"
	"testing"
)

// S1 final-gate M1: when a connector turn's session type cannot be read, the
// reply_to-only rule must still apply (fail closed), never fall back to sending
// the plain reply to the chat that started the turn.
func TestMainConnectorTurn_FailsClosedWhenTheSessionCannotBeRead(t *testing.T) {
	f := boundConnFixture(t)
	const unreadable = "session_01NOSUCHSESSION00000000000"
	if !f.al.mainConnectorTurn("telegram", unreadable) {
		t.Fatal("an unreadable session on a connector channel must be treated as a main (reply_to-only)")
	}
	ag, _ := f.al.GetRegistry().GetAgent(addrReceiver)
	f.al.publishResponseIfNeeded(context.Background(), ag, "telegram", "chat-1", "plain answer", unreadable)
	if out, sent := addrDrainOutbound(f.bus); sent {
		t.Fatalf("a plain reply went to a connector although the session type was unreadable: %+v", out)
	}
	// Controls: a non-connector channel and an empty session id are unaffected.
	if f.al.mainConnectorTurn("webchat", unreadable) || f.al.mainConnectorTurn("telegram", "") {
		t.Fatal("the rule must stay limited to connector channels with a session id")
	}
}

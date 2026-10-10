package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// F4: a Stop that discards an admitted-but-undelivered U8 request must also
// invalidate its reply authority, so the request can no longer be answered.
func TestStopDiscardsAdmittedRequestAndRevokesItsReplyAuthority(t *testing.T) {
	al, rec, _, humanDone := newStopRedirectRoot(t)
	sessionID := rec.SessionID
	const requestID = "req-f4-undelivered"
	pair := addressing.Pair{WorkspaceID: "ws-1", AgentID: testDefaultAgentID}
	if err := al.RequestLedger().Put(addressing.Capture{
		RequestID: requestID, ReceiverSessionID: sessionID, Receiver: pair,
		Sender: addressing.Sender{Principal: "alice"},
		Source: addressing.Source{Kind: addressing.SourceConversation, Owner: addressing.Pair{WorkspaceID: "ws-1", AgentID: "ann"}, SessionID: "src-chat"},
		AdmittedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SETUP capture: %v", err)
	}
	// The request waits in the receiver's queue exactly as AdmitRequest's
	// inbound would: a human-class item carrying the request id as its archive entry.
	if _, _, err := al.enqueueHumanSteeringMessage(sessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: "request text"}, requestID); err != nil {
		t.Fatalf("SETUP enqueue: %v", err)
	}
	// Positive control: before Stop the request is answerable.
	if _, err := al.RequestLedger().Resolve(sessionID, pair, requestID); err != nil {
		t.Fatalf("instrument check: the request must resolve before Stop: %v", err)
	}

	if _, err := al.StopSession(context.Background(), StopRequest{
		SessionID: sessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "f4-owner"}, Channel: "webchat",
	}); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	select {
	case <-humanDone:
	case <-time.After(5 * time.Second):
		t.Fatal("parked turn did not join")
	}

	if _, err := al.RequestLedger().Resolve(sessionID, pair, requestID); err == nil {
		t.Fatal("a request discarded by Stop must no longer be answerable")
	}
	_, err := al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{
		ReplyTo: requestID, Content: "late answer",
		Author: tools.SendOrigin{WorkspaceID: "ws-1", AgentID: testDefaultAgentID}, ResponderSessionID: sessionID,
	})
	if err == nil {
		t.Fatal("a late reply to a discarded request must refuse")
	}
}

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// R2-S1-01: the whole production path. A peer asks a STOPPED agent through the
// router; the admission wake is processed by a real session worker
// (processTurn), which drops it because the agent is stopped; later a person
// resumes the agent. The request must still be undelivered after the dropped
// wake, and after the resume the SCRIPTED PROVIDER must receive the request
// text. (Earlier tests seeded the ledger by hand, so the worker's own
// pre-admission "delivered" mark was invisible to them.)
func TestHeldRequest_RealWorkerDropsTheWake_ResumeThenDeliversTheTextToTheModel(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &r1CompletionProvider{
		answers: []string{"resumed", "request answered"},
		entered: make(chan int, 4),
		release: []chan struct{}{make(chan struct{}), make(chan struct{})},
	}
	provider.openAll()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("registered agent missing")
	}
	inst.Provider = provider

	deps := &fakeAddressDeps{eligible: map[string]bool{addrWS + "/" + addrReceiver: true}}
	al.SetAddressDeps(deps)
	setSendMessagePolicy(t, al, addrReceiver, config.ToolPolicyAllow)
	router := al.NewAddressRouter()

	store := al.GetSessionStore()
	recv, err := store.GetOrCreateMainSession(addrWS, addrReceiver)
	if err != nil {
		t.Fatal(err)
	}
	asker, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	if err != nil {
		t.Fatal(err)
	}
	ws := addrWS
	if err := store.SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatal(err)
	}
	lc := al.GetSessionLifecycleStore()
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: recv.ID, Generation: 1, State: session.LifecycleStopped, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: addrWS, AgentID: addrReceiver,
		StopNote: &session.StopNote{Cause: session.StopCauseStop},
	}); err != nil {
		t.Fatal(err)
	}

	const body = "please review X for the Q3 report"
	got, err := router.SendPeer(context.Background(), tools.PeerRequest{
		RecipientWorkspaceID: addrWS, RecipientAgentID: addrReceiver, Content: body,
		Sender: tools.SendOrigin{WorkspaceID: addrWS, AgentID: "ray"}, SenderSessionID: asker.ID,
	})
	if err != nil {
		t.Fatalf("SendPeer: %v", err)
	}
	if !got.Held {
		t.Fatal("SETUP: the request to a stopped agent was not reported held")
	}

	// The admission wake goes through a REAL worker turn and is dropped.
	wake := <-al.bus.InboundChan()
	scope, _, routed := al.resolveSteeringTarget(wake)
	if !routed {
		t.Fatalf("SETUP: wake %+v is not routable", wake)
	}
	w := newSessionWorker(scope, al, func() {})
	t.Cleanup(w.cancel)
	w.processTurn(context.Background(), wake)

	held, err := al.RequestLedger().Undelivered(recv.ID)
	if err != nil || len(held) != 1 || held[0].RequestID != got.RequestID {
		t.Fatalf("after the dropped wake the request must still be held; Undelivered = %+v err=%v", held, err)
	}

	// A person resumes the agent: that admitted turn delivers the held request.
	human := bus.InboundMessage{
		Channel: "webchat", ChatID: "chat-" + recv.ID, SessionID: recv.ID, Content: "carry on",
		SessionKey: "agent:" + testDefaultAgentID + ":session:" + recv.ID,
		Sender:     bus.SenderInfo{CanonicalID: "webchat:tester"}, GatewayUserID: "webchat:tester", UserInitiated: true,
		Metadata: map[string]string{"agent_id": addrReceiver, "workspace_id": addrWS},
	}
	if _, _, perr := al.processMessage(context.Background(), human); perr != nil {
		t.Fatalf("resume turn: %v", perr)
	}
	select {
	case redo := <-al.bus.InboundChan():
		w2 := newSessionWorker(scope, al, func() {})
		t.Cleanup(w2.cancel)
		w2.processTurn(context.Background(), redo)
	case <-time.After(5 * time.Second):
		t.Fatal("the resume did not republish the held request")
	}

	seen := false
	for _, req := range provider.Requests() {
		for _, m := range req {
			if strings.Contains(m.Content, body) {
				seen = true
			}
		}
	}
	if !seen {
		t.Fatalf("the model never received the held request's text; provider calls=%d", len(provider.Requests()))
	}
	if c, _, _ := al.RequestLedger().Get(recv.ID, got.RequestID); !c.Delivered {
		t.Fatalf("request should be delivered once the model has it: %+v", c)
	}
	_ = addressing.Capture{}
}

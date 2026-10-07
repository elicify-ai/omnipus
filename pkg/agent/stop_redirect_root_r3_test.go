package agent

// Reviewer round 3 for /stop-redirect on an ordinary chat: the branches the
// round-2 pack left unpinned.
//
//	R1: a channel chat with no recorded chat id, redirected from the web frame,
//	    is refused visibly and leaves the running turn alone.
//	R2: when the instruction cannot be routed, the chat gets the visible
//	    not-applied notice quoting the instruction.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

func TestStopRedirectRoot_R1_ChannelChatWithoutChatIDIsRefusedAndTurnKeepsRunning(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &r1CompletionProvider{
		answers: []string{"first draft"}, entered: make(chan int, 1), release: []chan struct{}{make(chan struct{})},
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered agent missing")
	}
	inst.Provider = provider
	t.Cleanup(provider.openAll)
	// A channel chat whose session recorded no chat id (empty PeerID).
	meta, err := al.GetSessionStore().NewChannelSession("telegram", "telegram", "", testDefaultAgentID, "Alice")
	if err != nil {
		t.Fatalf("SETUP: channel session: %v", err)
	}
	first := bus.InboundMessage{
		Channel: "telegram", ChatID: r2Chat, SessionID: meta.ID, Content: "Draft the full rollout plan.",
		Sender: bus.SenderInfo{CanonicalID: r2Owner}, GatewayUserID: r2Owner, UserInitiated: true,
		Metadata: map[string]string{"agent_id": testDefaultAgentID},
	}
	turnDone := make(chan error, 1)
	go func() {
		_, _, perr := al.processMessage(context.Background(), first)
		turnDone <- perr
	}()
	r1AwaitProvider(t, provider, 0)
	if al.activeTurnForCancel(meta.ID, CancelScope{SessionID: meta.ID, TurnOnly: true}) == nil {
		t.Fatal("SETUP: the chat has no live turn")
	}

	err = al.RedirectSessionTurn(context.Background(), meta.ID, r2Instruction, r2Owner, "web")
	if err == nil || !strings.Contains(err.Error(), "no recorded chat id") {
		t.Fatalf("redirect = %v, want the visible \"no recorded chat id\" refusal", err)
	}
	if al.activeTurnForCancel(meta.ID, CancelScope{SessionID: meta.ID, TurnOnly: true}) == nil {
		t.Error("the refused redirect stopped the chat's turn; it must keep running")
	}
	if rec, lerr := al.GetSessionLifecycleStore().Load(meta.ID); lerr == nil && rec.Stop != nil {
		t.Errorf("the refused redirect recorded a Stop: %+v", rec.Stop)
	}
	provider.open(0)
	select {
	case perr := <-turnDone:
		if perr != nil {
			t.Errorf("the untouched turn ended with %v, want a normal finish", perr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the untouched turn did not finish")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !al.WaitForActiveRequestsContext(ctx) {
		t.Fatal("active requests did not drain")
	}
	if n := len(provider.Requests()); n != 1 {
		t.Errorf("provider calls = %d, want 1: a refused redirect starts no second turn", n)
	}
	for {
		select {
		case out := <-al.bus.OutboundChan():
			if out.ChatID == meta.ID {
				t.Errorf("a message went to the fabricated chat id %q (the session id): %q", out.ChatID, out.Content)
			}
			continue
		default:
		}
		break
	}
}

func TestStopRedirectRoot_R2_NoticeQuotesInstruction_RouteError(t *testing.T) {
	al, sessionID, provider := r2ChannelChat(t, "unused")
	msg, err := al.ordinaryRedirectMessage(sessionID, r2Instruction, r2Owner, "telegram", r2Chat)
	if err != nil {
		t.Fatalf("build the redirect message: %v", err)
	}
	// The chat's agent is gone from the registry: the instruction cannot be routed.
	msg.Metadata = map[string]string{"agent_id": "agent-that-is-not-registered"}
	al.continueOrdinaryAfterStop(context.Background(), msg, time.Minute)
	out := r2AwaitOutbound(t, al, "route-error notice", r2NoticeFor(sessionID))
	r2AssertNotice(t, out, "telegram", r2Chat, sessionID)
	if !strings.Contains(out.Content, "route the instruction") {
		t.Errorf("notice %q does not say the instruction could not be routed", out.Content)
	}
	if n := len(provider.Requests()); n != 0 {
		t.Errorf("provider calls = %d, want 0: an unroutable redirect runs nothing", n)
	}
}

// R3: only a web chat's redirected message is saved by the redirect itself; a
// channel chat's turn records its own user message. So a transcript that cannot
// be written is NOT a redirect failure for a channel chat: the instruction still
// reaches the model exactly once, no "not applied" notice is sent, and the reply
// goes to the chat's real channel and chat id.
func TestStopRedirectRoot_R3_ChannelChatTranscriptWriteFailureDoesNotDropTheInstruction(t *testing.T) {
	al, sessionID, provider := r2ChannelChat(t, "summary reply")
	path := filepath.Join(al.GetSessionStore().BaseDir(), sessionID, "transcript.jsonl")
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("SETUP: remove transcript: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("SETUP: block the transcript path: %v", err)
	}
	msg, err := al.ordinaryRedirectMessage(sessionID, r2Instruction, r2Owner, "web", "")
	if err != nil {
		t.Fatalf("build the redirect message: %v", err)
	}
	if msg.Channel != "telegram" || msg.ChatID != r2Chat {
		t.Fatalf("SETUP: message routed to %s/%s, want the chat's real telegram/%s", msg.Channel, msg.ChatID, r2Chat)
	}
	go al.continueOrdinaryAfterStop(context.Background(), msg, time.Minute)
	r1AwaitProvider(t, provider, 0)
	requests := provider.Requests()
	if got := stopRedirectUserCopies(requests[0], r2Instruction); got != 1 {
		t.Errorf("model input carries the instruction %d times, want exactly 1", got)
	}
	provider.open(0)
	var notice *bus.OutboundMessage
	out := r2AwaitOutbound(t, al, "the continued turn's reply", func(o bus.OutboundMessage) bool {
		if r2NoticeFor(sessionID)(o) {
			notice = &o
			return true
		}
		return o.Content == "summary reply"
	})
	if notice != nil {
		t.Fatalf("a not-applied notice was sent for a channel chat whose instruction was applied: %q", notice.Content)
	}
	if out.Channel != "telegram" || out.ChatID != r2Chat {
		t.Errorf("reply routed to %s/%s, want the chat's real telegram/%s", out.Channel, out.ChatID, r2Chat)
	}
}

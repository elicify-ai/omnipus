package agent

// Reviewer round 2 for /stop-redirect on an ordinary chat (founder rule
// 2026-10-06: it stops THIS chat's own turn and continues THIS chat with the
// text, on web, CLI and channels).
//
//	G1: a redirect that arrives from the web frame for a channel chat
//	    continues on that chat's real channel AND chat id.
//	G2: when the stop succeeded but the continuation cannot run, the chat gets a
//	    visible notice that quotes the instruction the person sent.
//	G3: the real channel-control boundary stops a root channel chat and
//	    continues it with the instruction exactly once.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const (
	r2Instruction = "Drop that and give me the two-line summary instead."
	r2Owner       = "root-owner"
	r2Chat        = "chat-77"
)

// r2ChannelChat builds an idle ordinary Telegram chat the way a channel
// message creates it: a channel session indexed by (channel, chat id).
func r2ChannelChat(t *testing.T, answers ...string) (*AgentLoop, string, *r1CompletionProvider) {
	t.Helper()
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &r1CompletionProvider{answers: answers, entered: make(chan int, len(answers))}
	for range answers {
		provider.release = append(provider.release, make(chan struct{}))
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered agent missing")
	}
	inst.Provider = provider
	t.Cleanup(provider.openAll)
	meta, err := al.GetSessionStore().NewChannelSession("telegram", "telegram", r2Chat, testDefaultAgentID, "Alice")
	if err != nil {
		t.Fatalf("SETUP: channel session: %v", err)
	}
	al.channelSessionIdx.Store("telegram/"+r2Chat, meta.ID)
	return al, meta.ID, provider
}

// r2AwaitOutbound returns the first outbound message matching want.
func r2AwaitOutbound(t *testing.T, al *AgentLoop, what string, want func(bus.OutboundMessage) bool) bus.OutboundMessage {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case out := <-al.bus.OutboundChan():
			if want(out) {
				return out
			}
		case <-timeout:
			t.Fatalf("no outbound message arrived: %s", what)
		}
	}
}

func r2NoticeFor(sessionID string) func(bus.OutboundMessage) bool {
	return func(out bus.OutboundMessage) bool {
		return out.SessionID == sessionID && strings.Contains(out.Content, "Redirect was not applied")
	}
}

// r2HoldStopFence leaves the chat with a Stop fence that never lands, so the
// wait for the stopped turn can only end by deadline or shutdown.
func r2HoldStopFence(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	rec := adr093Record(sessionID, 1, session.LifecycleRunning)
	rec.Stop = &session.Stop{At: time.Now(), Generation: 1, By: session.Principal{Kind: session.PrincipalKindHuman, ID: r2Owner}}
	adr093Persist(t, al, rec)
}

func r2AssertNotice(t *testing.T, out bus.OutboundMessage, channel, chatID, sessionID string) {
	t.Helper()
	if out.Channel != channel || out.ChatID != chatID {
		t.Errorf("notice routed to %s/%s, want the chat's own %s/%s", out.Channel, out.ChatID, channel, chatID)
	}
	if out.SessionID != sessionID {
		t.Errorf("notice session = %q, want %q", out.SessionID, sessionID)
	}
	if !strings.Contains(out.Content, r2Instruction) {
		t.Errorf("notice %q does not quote the instruction %q the person sent", out.Content, r2Instruction)
	}
}

func TestStopRedirectRoot_G1_WebFrameRedirectOfChannelChatContinuesOnRealChannelAndChat(t *testing.T) {
	al, sessionID, provider := r2ChannelChat(t, "summary reply")
	// RedirectSessionTurn is what the web frame calls: channel "web", no chat id.
	if err := al.RedirectSessionTurn(context.Background(), sessionID, r2Instruction, r2Owner, "web"); err != nil {
		t.Fatalf("redirect of a channel chat from the web frame must be accepted, got %v", err)
	}
	r1AwaitProvider(t, provider, 0)
	provider.open(0)
	out := r2AwaitOutbound(t, al, "the continued turn's reply", func(o bus.OutboundMessage) bool { return o.Content == "summary reply" })
	if out.Channel != "telegram" || out.ChatID != r2Chat {
		t.Errorf("continued turn's reply routed to %s/%s, want the chat's real telegram/%s", out.Channel, out.ChatID, r2Chat)
	}
}

func TestStopRedirectRoot_G1_UndeliveredNoticeGoesToTheChatsRealChannelAndChat(t *testing.T) {
	al, sessionID, _ := r2ChannelChat(t, "unused")
	r2HoldStopFence(t, al, sessionID)
	msg, err := al.ordinaryRedirectMessage(sessionID, r2Instruction, r2Owner, "web", "")
	if err != nil {
		t.Fatalf("build the redirect message: %v", err)
	}
	al.continueOrdinaryAfterStop(context.Background(), msg, 300*time.Millisecond)
	r2AssertNotice(t, r2AwaitOutbound(t, al, "undelivered notice", r2NoticeFor(sessionID)), "telegram", r2Chat, sessionID)
}

func TestStopRedirectRoot_G2_NoticeQuotesInstruction_WaitDeadline(t *testing.T) {
	al, sessionID, provider := r2ChannelChat(t, "unused")
	r2HoldStopFence(t, al, sessionID)
	msg, err := al.ordinaryRedirectMessage(sessionID, r2Instruction, r2Owner, "telegram", r2Chat)
	if err != nil {
		t.Fatalf("build the redirect message: %v", err)
	}
	al.continueOrdinaryAfterStop(context.Background(), msg, 300*time.Millisecond)
	r2AssertNotice(t, r2AwaitOutbound(t, al, "deadline notice", r2NoticeFor(sessionID)), "telegram", r2Chat, sessionID)
	if n := len(provider.Requests()); n != 0 {
		t.Errorf("provider calls = %d, want 0: an undelivered redirect must not run a turn", n)
	}
}

func TestStopRedirectRoot_G2_NoticeQuotesInstruction_ShutdownDuringWait(t *testing.T) {
	al, sessionID, _ := r2ChannelChat(t, "unused")
	r2HoldStopFence(t, al, sessionID)
	msg, err := al.ordinaryRedirectMessage(sessionID, r2Instruction, r2Owner, "telegram", r2Chat)
	if err != nil {
		t.Fatalf("build the redirect message: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	al.continueOrdinaryAfterStop(ctx, msg, time.Minute)
	r2AssertNotice(t, r2AwaitOutbound(t, al, "shutdown-during-wait notice", r2NoticeFor(sessionID)), "telegram", r2Chat, sessionID)
}

func TestStopRedirectRoot_G2_NoticeQuotesInstruction_AdmissionRefusedAfterShutdown(t *testing.T) {
	al, sessionID, provider := r2ChannelChat(t, "unused")
	msg, err := al.ordinaryRedirectMessage(sessionID, r2Instruction, r2Owner, "telegram", r2Chat)
	if err != nil {
		t.Fatalf("build the redirect message: %v", err)
	}
	// The chat is idle (the wait ends at once) but shutdown has closed intake.
	al.stopActiveRequestIntake()
	al.continueOrdinaryAfterStop(context.Background(), msg, time.Minute)
	r2AssertNotice(t, r2AwaitOutbound(t, al, "refused-admission notice", r2NoticeFor(sessionID)), "telegram", r2Chat, sessionID)
	if n := len(provider.Requests()); n != 0 {
		t.Errorf("provider calls = %d, want 0: a refused admission runs nothing", n)
	}
}

func TestStopRedirectRoot_G2_NoticeQuotesInstruction_TranscriptSaveFailure(t *testing.T) {
	al, _ := newSteerAL(t)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	if err != nil {
		t.Fatalf("SETUP: web session: %v", err)
	}
	// A directory where the transcript file belongs makes the save fail.
	path := filepath.Join(al.GetSessionStore().BaseDir(), meta.ID, "transcript.jsonl")
	if err = os.RemoveAll(path); err != nil {
		t.Fatalf("SETUP: remove transcript: %v", err)
	}
	if err = os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("SETUP: block the transcript path: %v", err)
	}
	msg, err := al.ordinaryRedirectMessage(meta.ID, r2Instruction, r2Owner, "web", "")
	if err != nil {
		t.Fatalf("build the redirect message: %v", err)
	}
	if msg.Channel != "webchat" {
		t.Fatalf("SETUP: message channel = %q, want webchat (the only channel whose message is saved here)", msg.Channel)
	}
	al.continueOrdinaryAfterStop(context.Background(), msg, time.Minute)
	r2AssertNotice(t, r2AwaitOutbound(t, al, "save-failure notice", r2NoticeFor(meta.ID)), "webchat", meta.ID, meta.ID)
}

func TestStopRedirectRoot_G3_ChannelControlBoundaryStopsRootChatAndContinuesOnce(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	provider := &r1CompletionProvider{
		answers: []string{"first draft", "summary reply"}, entered: make(chan int, 2),
		release: []chan struct{}{make(chan struct{}), make(chan struct{})},
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: registered agent missing")
	}
	inst.Provider = provider
	t.Cleanup(provider.openAll)

	// A real inbound channel message creates the chat's session and holds its turn.
	first := bus.InboundMessage{
		Channel: "telegram", ChatID: r2Chat, Content: "Draft the full rollout plan.",
		Sender: bus.SenderInfo{CanonicalID: r2Owner}, UserInitiated: true,
		Metadata: map[string]string{"agent_id": testDefaultAgentID},
	}
	turnDone := make(chan error, 1)
	go func() {
		_, _, perr := al.processMessage(context.Background(), first)
		turnDone <- perr
	}()
	r1AwaitProvider(t, provider, 0)

	if err := al.RequestRedirectByChannelChat(context.Background(), "telegram", r2Chat, r2Owner, r2Instruction); err != nil {
		t.Fatalf("channel /stop-redirect on a root chat must be accepted, got %v", err)
	}
	select {
	case <-turnDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the chat's live turn did not end after the redirect")
	}
	r1AwaitProvider(t, provider, 1)
	requests := provider.Requests()
	if got := stopRedirectUserCopies(requests[1], r2Instruction); got != 1 {
		t.Errorf("continued turn's model input carries the instruction %d times, want exactly 1", got)
	}
	if last := requests[1][len(requests[1])-1]; last.Role != "user" || last.Content != r2Instruction {
		t.Errorf("continued turn's latest input = %s %q, want the exact instruction", last.Role, last.Content)
	}
	provider.open(1)
	out := r2AwaitOutbound(t, al, "the continued turn's reply", func(o bus.OutboundMessage) bool { return o.Content == "summary reply" })
	if out.Channel != "telegram" || out.ChatID != r2Chat {
		t.Errorf("continued turn's reply routed to %s/%s, want telegram/%s", out.Channel, out.ChatID, r2Chat)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !al.WaitForActiveRequestsContext(ctx) {
		t.Fatal("the continued turn did not finish")
	}
	if calls := len(provider.Requests()); calls != 2 {
		t.Errorf("provider calls = %d, want the stopped turn plus exactly one continued turn", calls)
	}
}

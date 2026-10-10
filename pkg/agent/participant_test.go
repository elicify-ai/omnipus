package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Session-core F15/F17: participant labels are display records the server
// stamps from authenticated facts or the server-held capture. Oracles: the
// architect's D1/D2 (kind/source/agent rules), founder F17 (the web user's own
// messages carry no label; an empty principal is never invented into a human),
// and "display_name is plain text, control characters stripped".

func TestParticipantFromSender(t *testing.T) {
	name := func(p addressing.Pair) string {
		if p.AgentID == "ray" {
			return "Ray the Researcher"
		}
		return ""
	}
	t.Run("agent uses the configured name and carries the pair", func(t *testing.T) {
		p := participantFromSender(addressing.Sender{Agent: addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}}, name)
		if p == nil || p.Kind != "agent" || p.DisplayName != "Ray the Researcher" || p.Source != nil ||
			p.Agent == nil || p.Agent.AgentId != "ray" || p.Agent.WorkspaceId != "ws-1" {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("agent without a name falls back to its id", func(t *testing.T) {
		p := participantFromSender(addressing.Sender{Agent: addressing.Pair{WorkspaceID: "ws-1", AgentID: "zed"}}, name)
		if p == nil || p.DisplayName != "zed" {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("connector human shows name and platform only", func(t *testing.T) {
		p := participantFromSender(addressing.Sender{Platform: "Google-Chat", PlatformID: "42", CanonicalID: "google-chat:42", DisplayName: "Alice"}, name)
		if p == nil || p.Kind != "human" || p.DisplayName != "Alice" || p.Source == nil || *p.Source != "google-chat" || p.Agent != nil {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("web principal is a web human", func(t *testing.T) {
		p := participantFromSender(addressing.Sender{Principal: "dan"}, name)
		if p == nil || p.Kind != "human" || p.DisplayName != "dan" || p.Source == nil || *p.Source != "web" {
			t.Fatalf("got %+v", p)
		}
	})
	t.Run("no authenticated fact gives no label", func(t *testing.T) {
		for _, s := range []addressing.Sender{{}, {Principal: "  "}, {Platform: "telegram"}, {Platform: "telegram", DisplayName: "\u0007​"}} {
			if p := participantFromSender(s, name); p != nil {
				t.Errorf("sender %+v produced %+v; want nil (never invent a human)", s, p)
			}
		}
	})
	t.Run("display name is plain text within bounds", func(t *testing.T) {
		long := strings.Repeat("é", 300)
		p := participantFromSender(addressing.Sender{Platform: "slack", DisplayName: "A\u0000li‮ce\n" + long}, name)
		if p == nil {
			t.Fatal("nil")
		}
		if strings.ContainsAny(p.DisplayName, "\u0000\n‮") {
			t.Fatalf("control/format characters survived: %q", p.DisplayName)
		}
		if n := len([]rune(p.DisplayName)); n == 0 || n > 128 {
			t.Fatalf("display name is %d runes, want 1..128", n)
		}
	})
	t.Run("source is normalized to the contract pattern", func(t *testing.T) {
		for in, want := range map[string]string{"Telegram": "telegram", " slack ": "slack", "9bad": "bad", "!!!": "other", "wa tsapp": "watsapp"} {
			if got := participantSource(in); got != want {
				t.Errorf("participantSource(%q) = %q, want %q", in, got, want)
			}
		}
	})
}

type publishingDeps struct {
	fakeAddressDeps
	pmu       sync.Mutex
	published []session.TranscriptEntry
}

func (d *publishingDeps) PublishUserEntry(_ string, e session.TranscriptEntry) {
	d.pmu.Lock()
	d.published = append(d.published, e)
	d.pmu.Unlock()
}

func TestAdmitRequest_StampsLabelAndShowsTheRequestLive(t *testing.T) {
	f := newAddrFixture(t)
	deps := &publishingDeps{fakeAddressDeps: fakeAddressDeps{eligible: f.deps.eligible}}
	f.al.SetAddressDeps(deps)

	// A peer-agent request: left-side label is the sending agent.
	req := f.peerReq()
	got, err := f.r.SendPeer(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(got.SessionID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("transcript = %v, %v", entries, err)
	}
	p := entries[0].Participant
	if p == nil || p.Kind != "agent" || p.Agent == nil || p.Agent.AgentId != "ray" {
		t.Fatalf("request entry participant = %+v, want the sending agent", p)
	}
	if len(deps.published) != 1 || deps.published[0].ID != got.RequestID || deps.published[0].Participant == nil {
		t.Fatalf("the request must be shown live with its label; published = %+v", deps.published)
	}

	// A web-human request admitted through AdmitRequest directly.
	id2, sid2, err := f.al.AdmitRequest(context.Background(), RequestAdmission{
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, Content: "hi", SenderLabel: "user dan",
		Sender: addressing.Sender{Principal: "dan"},
		Source: addressing.Source{Kind: addressing.SourceConversation, SessionID: "s-web"},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ = f.al.GetSessionStore().ReadTranscript(sid2)
	var web *session.TranscriptEntry
	for i := range entries {
		if entries[i].ID == id2 {
			web = &entries[i]
		}
	}
	if web == nil || web.Participant == nil || web.Participant.Kind != "human" || *web.Participant.Source != "web" || web.Participant.DisplayName != "dan" {
		t.Fatalf("web request participant = %+v", web)
	}
}

func TestReply_Conversation_ParticipantComesFromTheCaptureNotTheContent(t *testing.T) {
	f := newAddrFixture(t)
	recv, src := conversationSetup(t, f, addressing.Pair{})
	// conversationSetup's capture has Sender{Principal:"alice"}; the answer text
	// names somebody else entirely.
	req := f.replyReq(recv, "q7")
	req.Content = "Bob, here is your answer"
	if _, err := f.r.Reply(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	entries, err := f.al.GetSessionStore().ReadTranscript(src)
	if err != nil || len(entries) != 1 {
		t.Fatalf("source transcript = %v, %v", entries, err)
	}
	rp := entries[0].ReplyToParticipant
	if rp == nil || rp.Kind != "human" || rp.DisplayName != "alice" || *rp.Source != "web" {
		t.Fatalf("reply_to_participant = %+v, want the captured sender alice (web)", rp)
	}
}

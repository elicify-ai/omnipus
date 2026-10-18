package agent

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Participant labels (session-core F15/F17). A ChatParticipant is a DISPLAY
// record the server stamps on a transcript entry from authenticated facts: who
// wrote a left-side user-role entry (Participant) or who a reply went to
// (ReplyToParticipant). It carries a name, a kind and a source label — never an
// id, instance, chat or route (those stay in the addressing capture).
//
// Trust decisions (security review):
//
//	P1  The only inputs are addressing.Sender (authenticated principal,
//	    bus.SenderInfo from channel code, or the router-built agent pair) —
//	    never message content, tool arguments or model output.
//	P2  ReplyToParticipant is built from the server-held CAPTURE, so a model
//	    cannot choose who a reply "went to".
//	P3  display_name is sanitized (control characters stripped, 128 runes) and
//	    is plain text; the SPA must not render it as markup.
//	P4  The web user's own messages carry no label (F17); an empty principal is
//	    never turned into a human.

const maxParticipantNameRunes = 128

// participantFromSender maps a sender to its display record, or nil when no
// authenticated fact names one. agentName resolves an agent's configured
// display name (empty falls back to its id).
func participantFromSender(s addressing.Sender, agentName func(addressing.Pair) string) *generated.ChatParticipant {
	switch {
	case !s.Agent.IsZero():
		name := ""
		if agentName != nil {
			name = agentName(s.Agent)
		}
		if strings.TrimSpace(name) == "" {
			name = s.Agent.AgentID
		}
		name = sanitizeParticipantName(name)
		if name == "" {
			return nil
		}
		return &generated.ChatParticipant{
			Kind: "agent", DisplayName: name,
			Agent: &struct {
				AgentId     string `json:"agent_id"`
				WorkspaceId string `json:"workspace_id"`
			}{AgentId: s.Agent.AgentID, WorkspaceId: s.Agent.WorkspaceID},
		}
	case strings.TrimSpace(s.Platform) != "":
		name := sanitizeParticipantName(s.DisplayName)
		if name == "" {
			return nil
		}
		src := participantSource(s.Platform)
		return &generated.ChatParticipant{Kind: "human", DisplayName: name, Source: &src}
	case strings.TrimSpace(s.Principal) != "":
		name := sanitizeParticipantName(s.Principal)
		if name == "" {
			return nil
		}
		src := "web"
		return &generated.ChatParticipant{Kind: "human", DisplayName: name, Source: &src}
	}
	return nil
}

// participantSource normalizes a platform string to the contract's source
// pattern (^[a-z][a-z0-9_-]{0,31}$): lower-case, other runes dropped.
func participantSource(platform string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(platform)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			if b.Len() == 0 && (r < 'a' || r > 'z') {
				continue
			}
			b.WriteRune(r)
			if b.Len() == 32 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "other"
	}
	return b.String()
}

// sanitizeParticipantName strips control and format characters, collapses the
// result to the contract bound and trims it. It never returns more than 128 runes.
func sanitizeParticipantName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if utf8.RuneCountInString(out) > maxParticipantNameRunes {
		out = string([]rune(out)[:maxParticipantNameRunes])
	}
	return out
}

// agentDisplayName resolves a pair's configured agent name from the live
// registry; the workspace is not consulted (names are per agent).
func (al *AgentLoop) agentDisplayName(p addressing.Pair) string {
	reg := al.GetRegistry()
	if reg == nil {
		return ""
	}
	name, ok := reg.GetAgentName(p.AgentID)
	if !ok {
		return ""
	}
	return name
}

// userEntryPublisher is the optional gateway capability that shows a server-
// written left-side user entry to every tab bound to its session. It is an
// optional interface so AddressDeps fakes need not implement it.
type userEntryPublisher interface {
	PublishUserEntry(sessionID string, entry session.TranscriptEntry)
}

// publishUserEntry delivers a persisted left-side entry live when the gateway
// can; without a publisher the entry is still durable and appears on replay.
func (al *AgentLoop) publishUserEntry(sessionID string, entry session.TranscriptEntry) {
	if deps := al.loadAddressDeps(); deps != nil {
		if p, ok := deps.(userEntryPublisher); ok {
			p.PublishUserEntry(sessionID, entry)
		}
	}
}

// approvalOrigin is a turn's server-derived approval origin (spec §6.18,
// K7): the card label and, for a group-chat room request, the room
// reference. The zero value is a plain web turn.
type approvalOrigin struct {
	Label   string
	RoomRef *generated.RoomRef
	// Class ranks the origin for the "inputs consumed" rule below.
	Class originClass
}

// originClass ranks a turn's origins. Spec §6.18: a turn's origin is the set
// of inputs it consumed, and any consumed channel-person input makes it
// channel-triggered. Chief ruling 2026-10-18 (gate round 1, Q1 A / Q2 yes):
// precedence channel > room > peer > web; the label never moves down once a
// higher class was consumed, and within a class the FIRST consumed input
// keeps the label. Approvals already pending keep the label they were raised
// with (amendment R6: a card is immutable once raised); only approvals
// raised after the input joined carry the new label.
type originClass int

const (
	originWeb originClass = iota
	originPeer
	originRoom
	originChannel
)

// mergeApprovalOrigin returns the origin a turn carries after it consumed
// next, under the precedence rule above.
func mergeApprovalOrigin(current, next approvalOrigin) approvalOrigin {
	if next.Label == "" || next.Class <= current.Class {
		return current
	}
	return next
}

// roomRefCopy returns a copy of the room reference (nil when none), so an
// approval request never shares the turn's value.
func (o approvalOrigin) roomRefCopy() *generated.RoomRef {
	if o.RoomRef == nil {
		return nil
	}
	ref := *o.RoomRef
	return &ref
}

// turnApprovalOrigin derives the origin every approval card of a turn
// carries (spec §6.18, K7) from the input that started it:
//   - a connector (channel) message: "while answering <person> on
//     <connection>" — the person is the adapter-stamped sender display name,
//     the connection is the channel instance the message arrived on
//     (BDD-16.33 "while answering Anna on tg-1");
//   - the wake of a group-chat room request (room_request.go
//     roomWakeMessage): "for group <name>" and the room reference {room,
//     name, request id = delivery id, role request} (FR-169, FR-251, K7);
//   - the wake of a request a peer agent sent (address_router.go
//     publishRequestWake): "asked by <Agent>".
//
// Room and peer requests are both read from the request ledger — the
// server-recorded capture, never the request text. Any other turn (the web
// user, an automatic wake) gets no origin; so does a request capture that
// cannot be read (logged).
func (al *AgentLoop) turnApprovalOrigin(msg bus.InboundMessage, sessionID string) approvalOrigin {
	if isConnectorChannel(msg.Channel) {
		person := strings.TrimSpace(msg.Sender.DisplayName)
		if person == "" {
			person = strings.TrimSpace(msg.Sender.Username)
		}
		if person == "" {
			person = strings.TrimSpace(msg.Sender.CanonicalID)
		}
		if person == "" {
			person = "a person"
		}
		return approvalOrigin{Label: "while answering " + person + " on " + inboundInstanceID(msg), Class: originChannel}
	}
	requestID := msg.TranscriptEntryID
	if sessionID == "" || requestID == "" || msg.ChatID != "request:"+requestID {
		return approvalOrigin{}
	}
	ledger := al.RequestLedger()
	if ledger == nil {
		return approvalOrigin{}
	}
	capture, found, err := ledger.Get(sessionID, requestID)
	if err != nil {
		logger.WarnCF("agent", "approval origin: request capture unreadable; approvals of this turn carry no origin label",
			map[string]any{"session_id": sessionID, "request_id": requestID, "error": err.Error()})
		return approvalOrigin{}
	}
	if !found {
		return approvalOrigin{}
	}
	if msg.Metadata[roomIDMetadataKey] != "" {
		if capture.Source.RoomID == "" || capture.Source.RoomName == "" {
			return approvalOrigin{}
		}
		return approvalOrigin{
			Class: originRoom,
			Label: "for group " + capture.Source.RoomName,
			RoomRef: &generated.RoomRef{
				RoomId: capture.Source.RoomID, RoomName: capture.Source.RoomName,
				RequestId: requestID, Role: "request",
			},
		}
	}
	if capture.Sender.Agent.AgentID == "" {
		return approvalOrigin{}
	}
	name := al.agentDisplayName(capture.Sender.Agent)
	if name == "" {
		name = capture.Sender.Agent.AgentID
	}
	return approvalOrigin{Label: "asked by " + name, Class: originPeer}
}

package agent

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
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

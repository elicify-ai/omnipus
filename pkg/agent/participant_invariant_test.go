package agent

import (
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/addressing"
)

// ChatParticipant is one flat schema (founder ruling, final gate item 5): the
// wire type cannot forbid an illegal source/agent combination, so the server
// does. Oracle: contracts/components/schemas/ChatParticipant.yaml — kind=human
// carries source and never agent; kind=agent carries agent and never source; a
// sender naming an agent is kind=agent even when it also carries a platform or
// principal; source matches ^[a-z][a-z0-9_-]{0,31}$; display_name is 1..128.
func TestParticipantFromSender_NeverEmitsAnIllegalCombination(t *testing.T) {
	agent := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	senders := map[string]addressing.Sender{
		"agent only":                 {Agent: agent},
		"agent and platform":         {Agent: agent, Platform: "telegram", DisplayName: "Alice"},
		"agent and principal":        {Agent: agent, Principal: "dan"},
		"agent, platform, principal": {Agent: agent, Platform: "Slack", Principal: "dan", DisplayName: "Bob"},
		"connector human":            {Platform: "Google-Chat", DisplayName: "Alice"},
		"web human":                  {Principal: "dan"},
		"platform and principal":     {Platform: "telegram", Principal: "dan", DisplayName: "Alice"},
		"odd platform":               {Platform: "  Té+lé gram!! ", DisplayName: "Alice"},
		"overlong name":              {Principal: string(make([]rune, 300))},
	}
	emitted := 0
	for label, s := range senders {
		p := participantFromSender(s, nil)
		if p == nil {
			continue
		}
		emitted++
		switch p.Kind {
		case "human":
			if p.Source == nil || p.Agent != nil {
				t.Errorf("%s: kind=human must carry source and no agent, got source=%v agent=%v", label, p.Source, p.Agent)
			}
			if p.Source != nil && !participantSourceOK(*p.Source) {
				t.Errorf("%s: source %q breaks the contract pattern", label, *p.Source)
			}
		case "agent":
			if p.Agent == nil || p.Source != nil {
				t.Errorf("%s: kind=agent must carry agent and no source, got source=%v agent=%v", label, p.Source, p.Agent)
			}
		default:
			t.Errorf("%s: unknown kind %q", label, p.Kind)
		}
		if n := utf8.RuneCountInString(p.DisplayName); n < 1 || n > 128 {
			t.Errorf("%s: display_name length %d outside 1..128", label, n)
		}
		if s.Agent.AgentID != "" && p.Kind != "agent" {
			t.Errorf("%s: a sender naming an agent must be kind=agent, got %q", label, p.Kind)
		}
	}
	if emitted < 6 {
		t.Fatalf("instrument: only %d of %d senders produced a record; the invariants above barely ran", emitted, len(senders))
	}
}

// participantSourceOK is the contract pattern ^[a-z][a-z0-9_-]{0,31}$.
func participantSourceOK(s string) bool {
	if len(s) < 1 || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

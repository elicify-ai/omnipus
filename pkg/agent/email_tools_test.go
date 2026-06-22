package agent

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

func newEmailTestAgent() *AgentInstance {
	return &AgentInstance{ID: "mia", Name: "Mia", Tools: tools.NewToolRegistry()}
}

func emailToolNames() []string {
	return []string{"read_inbox", "search_email", "read_message", "send_email", "reply"}
}

func assertEmailToolsRegistered(t *testing.T, ag *AgentInstance, want bool) {
	t.Helper()
	for _, name := range emailToolNames() {
		_, ok := ag.Tools.Get(name)
		if ok != want {
			t.Errorf("tool %q registered=%v, want %v", name, ok, want)
		}
	}
}

func TestRegisterEmailTools_NoMailbox_NoTools(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{} // no mailboxes
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, false)
}

func TestRegisterEmailTools_DisabledMailbox_NoTools(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: map[string]config.MailboxConfig{
		"mia": {Enabled: false, PasswordRef: "REF", IMAPHost: "i", SMTPHost: "s", Username: "u"},
	}}
	t.Setenv("REF", "secret")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, false)
}

func TestRegisterEmailTools_EnabledButPasswordUnresolved_NoTools(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: map[string]config.MailboxConfig{
		"mia": {Enabled: true, PasswordRef: "MISSING_REF", IMAPHost: "i", SMTPHost: "s", Username: "u"},
	}}
	// MISSING_REF is not set in env → password does not resolve.
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, false)
}

func TestRegisterEmailTools_EnabledResolvable_RegistersFive(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: map[string]config.MailboxConfig{
		"mia": {
			Enabled: true, PasswordRef: "MIA_MAIL_PW", WorkspaceID: "ws_my",
			IMAPHost: "imap.x.com", SMTPHost: "smtp.x.com", Username: "me@x.com",
		},
	}}
	t.Setenv("MIA_MAIL_PW", "app-pass")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, true)
}

func TestRegisterEmailTools_OnlyOwningAgentGetsTools(t *testing.T) {
	cfg := &config.Config{Mailboxes: map[string]config.MailboxConfig{
		"mia": {
			Enabled: true, PasswordRef: "MIA_MAIL_PW", WorkspaceID: "ws_my",
			IMAPHost: "imap.x.com", SMTPHost: "smtp.x.com", Username: "me@x.com",
		},
	}}
	t.Setenv("MIA_MAIL_PW", "app-pass")

	owner := newEmailTestAgent() // ID "mia" — owns the mailbox
	registerEmailToolsForAgent(cfg, "mia", owner)
	assertEmailToolsRegistered(t, owner, true)

	other := &AgentInstance{ID: "jim", Name: "Jim", Tools: tools.NewToolRegistry()}
	registerEmailToolsForAgent(cfg, "jim", other) // jim has no mailbox
	assertEmailToolsRegistered(t, other, false)
}

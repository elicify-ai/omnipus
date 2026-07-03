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
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {"ws_my": {Enabled: false, PasswordRef: "REF", IMAPHost: "i", SMTPHost: "s", Username: "u"}},
	}}
	t.Setenv("REF", "secret")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, false)
}

func TestRegisterEmailTools_EnabledButPasswordUnresolved_NoTools(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {"ws_my": {Enabled: true, PasswordRef: "MISSING_REF", IMAPHost: "i", SMTPHost: "s", Username: "u"}},
	}}
	// MISSING_REF is not set in env → password does not resolve.
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, false)
}

func TestRegisterEmailTools_EnabledResolvable_RegistersFive(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {"ws_my": {
			Enabled: true, PasswordRef: "MIA_MAIL_PW", WorkspaceID: "ws_my",
			IMAPHost: "imap.x.com", SMTPHost: "smtp.x.com", Username: "me@x.com",
		}},
	}}
	t.Setenv("MIA_MAIL_PW", "app-pass")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, true)
}

func TestRegisterEmailTools_TwoWorkspacePairs_RegistersOnce(t *testing.T) {
	// An agent with mailboxes in TWO workspaces still gets exactly one set of
	// the five tools — each tool holds the full workspace→transport map and
	// resolves the pair per turn (tools.ToolWorkspaceID), so registration is
	// per agent, not per pair.
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {
			"ws_a": {
				Enabled: true, PasswordRef: "MIA_MAIL_PW_A", WorkspaceID: "ws_a",
				IMAPHost: "imap.a.com", SMTPHost: "smtp.a.com", Username: "a@x.com",
			},
			"ws_b": {
				Enabled: true, PasswordRef: "MIA_MAIL_PW_B", WorkspaceID: "ws_b",
				IMAPHost: "imap.b.com", SMTPHost: "smtp.b.com", Username: "b@x.com",
			},
		},
	}}
	t.Setenv("MIA_MAIL_PW_A", "pass-a")
	t.Setenv("MIA_MAIL_PW_B", "pass-b")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, true)
}

func TestRegisterEmailTools_OnlyUnresolvablePairSkipped(t *testing.T) {
	// One resolvable pair + one unresolvable pair → tools still register
	// (backed by the resolvable transport); the bad pair is skipped with a WARN.
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {
			"ws_ok":  {Enabled: true, PasswordRef: "MIA_OK_PW", WorkspaceID: "ws_ok", IMAPHost: "i", SMTPHost: "s", Username: "u"},
			"ws_bad": {Enabled: true, PasswordRef: "MISSING_PW_REF", WorkspaceID: "ws_bad", IMAPHost: "i", SMTPHost: "s", Username: "u"},
		},
	}}
	t.Setenv("MIA_OK_PW", "pass")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, true)
}

func TestRegisterEmailTools_OnlyOwningAgentGetsTools(t *testing.T) {
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {"ws_my": {
			Enabled: true, PasswordRef: "MIA_MAIL_PW", WorkspaceID: "ws_my",
			IMAPHost: "imap.x.com", SMTPHost: "smtp.x.com", Username: "me@x.com",
		}},
	}}
	t.Setenv("MIA_MAIL_PW", "app-pass")

	owner := newEmailTestAgent() // ID "mia" — owns the mailbox
	registerEmailToolsForAgent(cfg, "mia", owner)
	assertEmailToolsRegistered(t, owner, true)

	other := &AgentInstance{ID: "jim", Name: "Jim", Tools: tools.NewToolRegistry()}
	registerEmailToolsForAgent(cfg, "jim", other) // jim has no mailbox
	assertEmailToolsRegistered(t, other, false)
}

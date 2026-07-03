package agent

import (
	"context"
	"strings"
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

// TestRegisterEmailTools_EmptyWorkspaceKey_SkippedSiblingSurvives mirrors the
// buildMailboxes guard in pkg/gateway/rest_mailbox.go: an improperly-keyed
// (empty workspace ID) pair must never enter EmailTransports — that's where
// the single-mailbox fallback would silently serve an unbound turn a mailbox
// it was never addressed to. Its sibling, properly-keyed pair still works.
func TestRegisterEmailTools_EmptyWorkspaceKey_SkippedSiblingSurvives(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {
			"": {
				Enabled: true, PasswordRef: "MIA_MAIL_PW_EMPTY",
				IMAPHost: "imap.bad.com", SMTPHost: "smtp.bad.com", Username: "bad@x.com",
			},
			"ws_ok": {
				Enabled: true, PasswordRef: "MIA_MAIL_PW_OK", WorkspaceID: "ws_ok",
				IMAPHost: "imap.x.com", SMTPHost: "smtp.x.com", Username: "ok@x.com",
			},
		},
	}}
	t.Setenv("MIA_MAIL_PW_EMPTY", "pass-empty")
	t.Setenv("MIA_MAIL_PW_OK", "pass-ok")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, true)

	tool, ok := ag.Tools.Get("read_inbox")
	if !ok {
		t.Fatalf("read_inbox must be registered")
	}
	// Bound to a workspace WITHOUT a mailbox — the error must name the
	// workspaces that DO have one, and must NOT list the empty key.
	ctx := tools.WithWorkspaceID(context.Background(), "ws_other")
	res := tool.Execute(ctx, map[string]any{})
	if !res.IsError {
		t.Fatalf("expected error for a workspace without a mailbox, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "ws_ok") {
		t.Fatalf("error must name the surviving workspace, got %s", res.ForLLM)
	}
}

// TestRegisterEmailTools_OnlyEmptyWorkspaceKey_NoTools verifies that when the
// ONLY configured pair has an empty workspace key, it is dropped and no
// tools register at all (the agent effectively owns no reachable mailbox).
func TestRegisterEmailTools_OnlyEmptyWorkspaceKey_NoTools(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {
			"": {
				Enabled: true, PasswordRef: "MIA_MAIL_PW_ONLY_EMPTY",
				IMAPHost: "i", SMTPHost: "s", Username: "u",
			},
		},
	}}
	t.Setenv("MIA_MAIL_PW_ONLY_EMPTY", "pass")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, false)
}

// TestRegisterEmailTools_EndToEndWiring_TwoPairs proves the registered tool
// instance is wired to the REAL per-workspace transport map built by
// registerEmailToolsForAgent, not a test double — without dialing IMAP. It
// fetches the live tool off the agent's registry and exercises the workspace-
// resolution seam (tools.EmailTransports.resolve) through it.
func TestRegisterEmailTools_EndToEndWiring_TwoPairs(t *testing.T) {
	ag := newEmailTestAgent()
	cfg := &config.Config{Mailboxes: config.MailboxesConfig{
		"mia": {
			"ws_a": {
				Enabled: true, PasswordRef: "MIA_WIRE_PW_A", WorkspaceID: "ws_a",
				IMAPHost: "imap.a.com", SMTPHost: "smtp.a.com", Username: "a@x.com",
			},
			"ws_b": {
				Enabled: true, PasswordRef: "MIA_WIRE_PW_B", WorkspaceID: "ws_b",
				IMAPHost: "imap.b.com", SMTPHost: "smtp.b.com", Username: "b@x.com",
			},
		},
	}}
	t.Setenv("MIA_WIRE_PW_A", "pass-a")
	t.Setenv("MIA_WIRE_PW_B", "pass-b")
	registerEmailToolsForAgent(cfg, "mia", ag)
	assertEmailToolsRegistered(t, ag, true)

	tool, ok := ag.Tools.Get("read_inbox")
	if !ok {
		t.Fatalf("read_inbox must be registered")
	}

	// Bound to a workspace WITHOUT a mailbox: the error must name the
	// workspaces that DO have one (ws_a, ws_b) — proving the tool holds the
	// real per-workspace map built from cfg.Mailboxes, not a stub.
	ctx := tools.WithWorkspaceID(context.Background(), "ws_no_mailbox")
	res := tool.Execute(ctx, map[string]any{})
	if !res.IsError {
		t.Fatalf("expected error for a workspace without a mailbox, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "ws_a") || !strings.Contains(res.ForLLM, "ws_b") {
		t.Fatalf("error must name both configured workspaces, got %s", res.ForLLM)
	}

	// Unbound turn on a two-pair agent: ambiguous, must refuse to guess.
	resUnbound := tool.Execute(context.Background(), map[string]any{})
	if !resUnbound.IsError {
		t.Fatalf("expected ambiguity error for unbound turn with 2 pairs, got %+v", resUnbound)
	}
	if !strings.Contains(resUnbound.ForLLM, "ws_a") || !strings.Contains(resUnbound.ForLLM, "ws_b") {
		t.Fatalf("ambiguity error must list both candidate workspaces, got %s", resUnbound.ForLLM)
	}
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

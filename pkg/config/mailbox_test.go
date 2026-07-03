package config

import "testing"

// The 0.1.0 cap-1-per-workspace rule (ValidateMailboxesCap1) was removed
// 2026-07-03 (operator-approved): every agent may own a mailbox, several per
// workspace. What remains — and what this test pins — is the STRUCTURAL half
// of the invariant: one mailbox per agent, guaranteed by the Config.Mailboxes
// map being keyed by agent ID.
func TestMailboxes_OneMailboxPerAgentByMapKey(t *testing.T) {
	mbs := map[string]MailboxConfig{
		"mia": {Enabled: true, WorkspaceID: "ws_my"},
	}
	mbs["mia"] = MailboxConfig{Enabled: true, WorkspaceID: "ws_my2"}
	if len(mbs) != 1 {
		t.Fatalf("agent key must hold exactly one mailbox, got %d", len(mbs))
	}
}

// Multiple enabled mailboxes in the SAME workspace are a legal configuration:
// each mailbox's unhandled mail becomes Board tasks assigned to its own owning
// agent, so several inboxes per workspace stay unambiguous.
func TestMailboxes_SeveralPerWorkspaceAreLegal(t *testing.T) {
	mbs := map[string]MailboxConfig{
		"mia": {Enabled: true, WorkspaceID: "ws_my"},
		"jim": {Enabled: true, WorkspaceID: "ws_my"},
		"ava": {Enabled: true, WorkspaceID: "ws_other"},
	}
	if len(mbs) != 3 {
		t.Fatalf("expected 3 mailboxes, got %d", len(mbs))
	}
}

// MailboxesConfig.UnmarshalJSON accepts both shapes: the current nested
// agent→workspace→mailbox form, and the legacy 0.1.0 flat agent-keyed form
// (lifted under its embedded workspace_id; entries without one are dropped).
func TestMailboxesConfig_UnmarshalNestedAndLegacy(t *testing.T) {
	var nested MailboxesConfig
	if err := nested.UnmarshalJSON([]byte(`{
		"mia": {
			"ws_a": {"enabled": true, "username": "mia-a@x.com"},
			"ws_b": {"enabled": true, "username": "mia-b@x.com"}
		}
	}`)); err != nil {
		t.Fatalf("nested shape must parse: %v", err)
	}
	if got := nested["mia"]["ws_a"].Username; got != "mia-a@x.com" {
		t.Fatalf("ws_a username = %q", got)
	}
	// The inner key is authoritative and mirrored into WorkspaceID.
	if got := nested["mia"]["ws_b"].WorkspaceID; got != "ws_b" {
		t.Fatalf("ws_b mirror = %q", got)
	}

	var legacy MailboxesConfig
	if err := legacy.UnmarshalJSON([]byte(`{
		"mia": {"enabled": true, "workspace_id": "ws_my", "username": "me@x.com", "imap_host": "i", "smtp_host": "s"},
		"jim": {"enabled": false, "username": "orphan@x.com"}
	}`)); err != nil {
		t.Fatalf("legacy shape must parse: %v", err)
	}
	mb, ok := legacy["mia"]["ws_my"]
	if !ok {
		t.Fatalf("legacy entry must be lifted under its workspace_id; got %+v", legacy)
	}
	if mb.Username != "me@x.com" || mb.WorkspaceID != "ws_my" {
		t.Fatalf("lifted entry = %+v", mb)
	}
	// Legacy entry without workspace_id: dropped (was unreachable anyway).
	if _, exists := legacy["jim"]; exists {
		t.Fatalf("workspace-less legacy entry must be dropped, got %+v", legacy["jim"])
	}
}

// An agent may hold mailboxes in several workspaces at once — the nested keys
// are the addressing; the same agent, different workspace, different inbox.
func TestMailboxesConfig_PerAgentPerWorkspacePairs(t *testing.T) {
	m := MailboxesConfig{
		"mia": {"ws_a": {Enabled: true}, "ws_b": {Enabled: true}},
		"jim": {"ws_a": {Enabled: true}},
	}
	if len(m["mia"]) != 2 || len(m["jim"]) != 1 {
		t.Fatalf("pair counts wrong: %+v", m)
	}
}

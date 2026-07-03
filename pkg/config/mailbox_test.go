package config

import "testing"

// MailboxesConfig.UnmarshalJSON accepts a SINGLE document that mixes one
// legacy-flat agent entry with one nested-shape agent entry — the realistic
// post-upgrade config, where an operator has configured a new (nested) agent
// while an old (legacy-flat) entry is still on disk. Both must parse
// correctly in the same pass.
func TestMailboxesConfig_UnmarshalMixedDocument_LegacyAndNestedAgents(t *testing.T) {
	var m MailboxesConfig
	if err := m.UnmarshalJSON([]byte(`{
		"mia": {"enabled": true, "workspace_id": "ws_legacy", "username": "legacy@x.com"},
		"jim": {
			"ws_a": {"enabled": true, "username": "jim-a@x.com"},
			"ws_b": {"enabled": true, "username": "jim-b@x.com"}
		}
	}`)); err != nil {
		t.Fatalf("mixed legacy+nested document must parse: %v", err)
	}
	mia, ok := m["mia"]["ws_legacy"]
	if !ok || mia.Username != "legacy@x.com" {
		t.Fatalf("legacy agent entry not lifted correctly: %+v", m["mia"])
	}
	if len(m["jim"]) != 2 || m["jim"]["ws_a"].Username != "jim-a@x.com" || m["jim"]["ws_b"].Username != "jim-b@x.com" {
		t.Fatalf("nested agent entry not parsed correctly: %+v", m["jim"])
	}
}

// A single agent entry that mixes legacy (scalar) and nested (object) inner
// values is malformed and must hard-error rather than being silently
// misclassified as legacy-flat (which would drop the nested keys' data).
func TestMailboxesConfig_UnmarshalMixedShapeSingleEntry_Errors(t *testing.T) {
	var m MailboxesConfig
	err := m.UnmarshalJSON([]byte(`{
		"mia": {
			"enabled": true,
			"ws_a": {"enabled": true, "username": "a@x.com"}
		}
	}`))
	if err == nil {
		t.Fatalf("mixed legacy/nested shape within one agent entry must error, got nil")
	}
	wantSubstr := `mailboxes: agent "mia" entry is malformed (mixed legacy/nested shape)`
	if err.Error() != wantSubstr {
		t.Fatalf("error = %q, want %q", err.Error(), wantSubstr)
	}
}

// A nested entry with an empty-string workspace key is dropped (with a WARN)
// while its sibling entry survives intact — mirrors the legacy branch's
// empty-workspace_id guard.
func TestMailboxesConfig_UnmarshalNestedEmptyWorkspaceKey_DroppedSiblingSurvives(t *testing.T) {
	var m MailboxesConfig
	if err := m.UnmarshalJSON([]byte(`{
		"mia": {
			"": {"enabled": true, "username": "orphan@x.com"},
			"ws_ok": {"enabled": true, "username": "ok@x.com"}
		}
	}`)); err != nil {
		t.Fatalf("empty workspace key entry must not error: %v", err)
	}
	if _, exists := m["mia"][""]; exists {
		t.Fatalf("empty-workspace-key entry must be dropped, got %+v", m["mia"])
	}
	if got, ok := m["mia"]["ws_ok"]; !ok || got.Username != "ok@x.com" {
		t.Fatalf("sibling entry must survive intact, got %+v", m["mia"])
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

package config

import (
	"errors"
	"testing"
)

func TestValidateMailboxesCap1_OnePerWorkspaceOK(t *testing.T) {
	mbs := map[string]MailboxConfig{
		"mia": {Enabled: true, WorkspaceID: "ws_my"},
		"jim": {Enabled: true, WorkspaceID: "ws_other"},
	}
	if err := ValidateMailboxesCap1(mbs); err != nil {
		t.Fatalf("distinct workspaces must pass cap-1, got %v", err)
	}
}

func TestValidateMailboxesCap1_TwoInSameWorkspaceRejected(t *testing.T) {
	mbs := map[string]MailboxConfig{
		"mia": {Enabled: true, WorkspaceID: "ws_my"},
		"jim": {Enabled: true, WorkspaceID: "ws_my"},
	}
	err := ValidateMailboxesCap1(mbs)
	if err == nil {
		t.Fatal("two mailboxes in one workspace must violate cap-1")
	}
	if !errors.Is(err, ErrMailboxesCap1Violated) {
		t.Fatalf("expected ErrMailboxesCap1Violated, got %v", err)
	}
}

func TestValidateMailboxesCap1_DisabledIgnored(t *testing.T) {
	mbs := map[string]MailboxConfig{
		"mia": {Enabled: true, WorkspaceID: "ws_my"},
		"jim": {Enabled: false, WorkspaceID: "ws_my"}, // disabled — registers no tools
	}
	if err := ValidateMailboxesCap1(mbs); err != nil {
		t.Fatalf("a disabled mailbox must not trip cap-1, got %v", err)
	}
}

func TestValidateMailboxesCap1_EmptyWorkspaceIgnored(t *testing.T) {
	mbs := map[string]MailboxConfig{
		"mia": {Enabled: true, WorkspaceID: ""},
		"jim": {Enabled: true, WorkspaceID: ""},
	}
	if err := ValidateMailboxesCap1(mbs); err != nil {
		t.Fatalf("empty workspace ids must be ignored, got %v", err)
	}
}

func TestValidateMailboxesCap1_OneMailboxPerAgentByMapKey(t *testing.T) {
	// The map key structurally guarantees one mailbox per agent — re-assigning the
	// same agent key overwrites, so there is never a second mailbox for an agent.
	mbs := map[string]MailboxConfig{
		"mia": {Enabled: true, WorkspaceID: "ws_my"},
	}
	mbs["mia"] = MailboxConfig{Enabled: true, WorkspaceID: "ws_my2"}
	if len(mbs) != 1 {
		t.Fatalf("agent key must hold exactly one mailbox, got %d", len(mbs))
	}
	if err := ValidateMailboxesCap1(mbs); err != nil {
		t.Fatalf("single mailbox must pass, got %v", err)
	}
}

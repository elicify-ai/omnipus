package tools

// RED pack (w4 spec §9.1 row 12): the attachment tools' Auto-approve
// classification (US-3.AC-6; founder Q4=A — the ordinary classes, no
// exception, no attachment-specific mechanism).

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// list/read follow the read tools' class; save uses the SAME conditional
// class as every other workspace write (write_file/edit_file/append_file) —
// a different class here would be the forbidden attachment-specific
// mechanism.
func TestAttachmentToolAutoClassification(t *testing.T) {
	if got := autoApproveClasses["list_email_attachments"]; got != autoApproveClasses["read_inbox"] {
		t.Fatalf("list_email_attachments class = %v, want the read tools' class %v (US-3.AC-6: list/read follow the read tools)", got, autoApproveClasses["read_inbox"])
	}
	if got := autoApproveClasses["read_email_attachment"]; got != autoApproveClasses["read_message"] {
		t.Fatalf("read_email_attachment class = %v, want the read tools' class %v (US-3.AC-6)", got, autoApproveClasses["read_message"])
	}
	if got := autoApproveClasses["download_email_attachment"]; got != AutoRunsIfArgs {
		t.Fatalf("download_email_attachment class = %v, want the workspace-path conditional class (US-3.AC-6: the existing conditional class, founder Q4=A)", got)
	}
	for _, peer := range []string{"write_file", "edit_file", "append_file"} {
		if autoApproveClasses["download_email_attachment"] != autoApproveClasses[peer] {
			t.Fatalf("save class %v differs from workspace-write peer %q (%v) — an attachment-specific mechanism is forbidden (Q4=A)",
				autoApproveClasses["download_email_attachment"], peer, autoApproveClasses[peer])
		}
	}
}

// An unconfigured save tool asks — it never runs, never refuses silently.
func TestDownloadAttachmentToolUnconfiguredAsks(t *testing.T) {
	tool := NewDownloadEmailAttachmentTool(nil)
	v := tool.AutoApproveVerdict(context.Background(), nil)
	if v.Run {
		t.Fatalf("unconfigured save tool auto-ran (%v) — it must ask (the tool's own doc: an unwired tool refuses with an explicit error, never a silent no-op)", v)
	}
	if v.Class != AutoVerdictClassAsks {
		t.Fatalf("unconfigured save verdict class = %v, want asks", v.Class)
	}
}

// Strictest-wins at these tool names: an agent-level allow can never loosen
// the shipped global ask (US-3.AC-6).
func TestAgentAllowCannotLoosenGlobalAskForSave(t *testing.T) {
	cfg := &ToolPolicyCfg{
		GlobalPolicies: map[string]config.ToolPolicy{"download_email_attachment": "ask"},
		Policies:       map[string]config.ToolPolicy{"download_email_attachment": "allow"},
	}
	if got := EffectiveToolPolicy(cfg, ScopeGeneral, "general", "download_email_attachment"); got != "ask" {
		t.Fatalf("effective policy with global ask × agent allow = %q, want ask (US-3.AC-6: an agent allow never loosens the global ask)", got)
	}
}

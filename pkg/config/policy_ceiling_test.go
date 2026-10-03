package config

// RED pack (w4 spec §9.1 row 11): the shipped attachment-tool ceiling
// entries and the additive reconciliation contract (US-3.AC-5; Hard
// Constraint #6; §12.1 points 1-2). Expected values are the spec's literals:
// allow / allow / ask. The allStaticToolNames boot-panic guard lives in
// pkg/coreagent (Go packages cannot span the two trees from one file; the
// oracle is unchanged).

import "testing"

func TestShippedCeilingAttachmentTools(t *testing.T) {
	ceiling := DefaultConfig().Sandbox.ToolPolicies
	for name, want := range map[string]string{
		"list_email_attachments":    "allow",
		"read_email_attachment":     "allow",
		"download_email_attachment": "ask",
	} {
		got, ok := ceiling[name]
		if !ok {
			t.Fatalf("shipped ceiling has no entry for %q (US-3.AC-5: the ceiling holds the literal entries; Hard Constraint #6: every static tool has an explicit ceiling entry)", name)
		}
		if got != want {
			t.Fatalf("ceiling[%q] = %q, want %q (spec §12.1 point 1 literals)", name, got, want)
		}
	}
}

func TestReconcileToolPolicyCeilingSelfHealsOldInstallAdditively(t *testing.T) {
	cfg := DefaultConfig()
	// Simulate an install whose config predates the three tools.
	for _, name := range []string{"list_email_attachments", "read_email_attachment", "download_email_attachment"} {
		delete(cfg.Sandbox.ToolPolicies, name)
	}
	known := map[string]struct{}{
		"list_email_attachments":    {},
		"read_email_attachment":     {},
		"download_email_attachment": {},
	}
	added := ReconcileToolPolicyCeiling(cfg, known)
	if len(added) != 3 {
		t.Fatalf("reconcile added %v, want exactly the three attachment entries (US-3.AC-5: old config gains the literal allow/allow/ask entries additively)", added)
	}
	for name, want := range map[string]string{
		"list_email_attachments":    "allow",
		"read_email_attachment":     "allow",
		"download_email_attachment": "ask",
	} {
		if got := cfg.Sandbox.ToolPolicies[name]; got != want {
			t.Fatalf("after reconcile ceiling[%q] = %q, want %q", name, got, want)
		}
	}
}

func TestReconcileToolPolicyCeilingNeverOverwritesOperatorValues(t *testing.T) {
	cfg := DefaultConfig()
	// An operator locked the save tool down; another entry rides the shipped default.
	cfg.Sandbox.ToolPolicies["download_email_attachment"] = "deny"
	known := map[string]struct{}{
		"list_email_attachments":    {},
		"read_email_attachment":     {},
		"download_email_attachment": {},
	}
	_ = ReconcileToolPolicyCeiling(cfg, known)
	if got := cfg.Sandbox.ToolPolicies["download_email_attachment"]; got != "deny" {
		t.Fatalf("operator-set deny was overwritten to %q (US-3.AC-5: operator values survive untouched; Hard Constraint #6)", got)
	}
}

func TestReconcileToolPolicyCeilingSkipsUnknownNamesRatherThanGuessing(t *testing.T) {
	cfg := DefaultConfig()
	known := map[string]struct{}{"not_a_real_tool": {}}
	added := ReconcileToolPolicyCeiling(cfg, known)
	if len(added) != 0 {
		t.Fatalf("reconcile invented entries for an unknown tool: %v (no shipped default to reconcile from — skip rather than guess)", added)
	}
}

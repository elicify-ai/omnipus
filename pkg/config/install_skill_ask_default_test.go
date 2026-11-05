package config

import "testing"

// Founder decision 2026-11-03: install_skill ships as "ask". A persisted
// "allow" is an operator-set value and must survive Reconcile (Hard
// Constraint #6: additive only, never overwrite).
func TestInstallSkillShipsAsAsk(t *testing.T) {
	got, ok := DefaultConfig().Sandbox.ToolPolicies["install_skill"]
	if !ok {
		t.Fatal("shipped ceiling has no install_skill entry")
	}
	if got != "ask" {
		t.Fatalf("install_skill ceiling = %q, want ask", got)
	}
}

func TestInstallSkillReconcileAddsAskAndKeepsPersistedAllow(t *testing.T) {
	known := map[string]struct{}{"install_skill": {}}

	// Config missing the key: Reconcile adds the shipped default, ask.
	fresh := DefaultConfig()
	delete(fresh.Sandbox.ToolPolicies, "install_skill")
	ReconcileToolPolicyCeiling(fresh, known)
	if got := fresh.Sandbox.ToolPolicies["install_skill"]; got != "ask" {
		t.Fatalf("reconciled missing install_skill = %q, want ask", got)
	}

	// Install that persisted allow under the old default keeps it.
	old := DefaultConfig()
	old.Sandbox.ToolPolicies["install_skill"] = "allow"
	ReconcileToolPolicyCeiling(old, known)
	if got := old.Sandbox.ToolPolicies["install_skill"]; got != "allow" {
		t.Fatalf("persisted allow rewritten to %q; operator values must survive", got)
	}
}

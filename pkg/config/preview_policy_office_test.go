// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// Office-document preview — policy seeding (office-preview spec AD-13, FR-016;
// TDD rows T-20 ceiling entries and T-23 reconcile).
//
// Oracles come from the spec, never from the code:
//   - AD-13: "Ceiling `preview_document: allow` (pkg/config/defaults.go)" —
//     the shipped global ceiling must carry the entry with value "allow".
//   - AD-076/ADR-077 (Hard Constraint #6): ReconcileToolPolicyCeiling
//     self-heals an old install's ceiling forward additively with the
//     shipped default, never overwrites an operator-set value, and never
//     re-adds a retired key.
//
// The reconcile regression guards (operator value preserved, idempotence,
// retired keys) pass today and stay green; the two preview assertions are
// RED on the pre-change code because defaults.go has no preview_document
// entry yet (T-20/T-23 red reason: shipped default absent).

import (
	"testing"
)

// TestOfficePreview_CeilingShipsAllow (T-20, AD-13): the shipped global
// ceiling carries "preview_document": "allow".
func TestOfficePreview_CeilingShipsAllow(t *testing.T) {
	got, ok := DefaultConfig().Sandbox.ToolPolicies["preview_document"]
	if !ok {
		t.Fatalf("BLOCKED: ceiling key \"preview_document\" absent from sandbox.tool_policies — required by office-preview spec AD-13 (T-20)")
	}
	if got != "allow" {
		t.Fatalf("ceiling preview_document = %q, want \"allow\" (spec AD-13)", got)
	}
}

// TestOfficePreview_ReconcileAddsMissingCeilingKey (T-23): an install whose
// saved ceiling predates the feature self-heals to the shipped default on
// the next load.
func TestOfficePreview_ReconcileAddsMissingCeilingKey(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Sandbox.ToolPolicies == nil {
		t.Fatalf("precondition failed: DefaultConfig has nil ToolPolicies")
	}
	delete(cfg.Sandbox.ToolPolicies, "preview_document")

	known := map[string]struct{}{"preview_document": {}}
	added := ReconcileToolPolicyCeiling(cfg, known)

	found := false
	for _, entry := range added {
		if entry == "preview_document=allow" {
			found = true
		}
	}
	if !found {
		t.Fatalf("BLOCKED: ReconcileToolPolicyCeiling did not add preview_document=allow (added=%v) — shipped default absent from defaults.go, required by spec AD-13 + ADR-076 self-heal (T-23)", added)
	}
	if got := cfg.Sandbox.ToolPolicies["preview_document"]; got != "allow" {
		t.Fatalf("reconciled ceiling preview_document = %q, want \"allow\" (spec AD-13)", got)
	}

	// Idempotent by construction: the second call must add nothing.
	if again := ReconcileToolPolicyCeiling(cfg, known); len(again) != 0 {
		t.Fatalf("reconcile must be idempotent, second call added %v", again)
	}
}

// TestReconcile_PreviewOperatorSetValueNeverOverwritten (AD-13, Hard
// Constraint #6 regression guard): an operator-set preview_document entry
// survives reconcile untouched — including a deliberate "deny" lockdown.
func TestReconcile_PreviewOperatorSetValueNeverOverwritten(t *testing.T) {
	for _, operatorValue := range []string{"deny", "ask", "allow"} {
		cfg := DefaultConfig()
		delete(cfg.Sandbox.ToolPolicies, "preview_document")
		cfg.Sandbox.ToolPolicies["preview_document"] = operatorValue

		added := ReconcileToolPolicyCeiling(cfg, map[string]struct{}{"preview_document": {}})

		for _, entry := range added {
			if entry == "preview_document="+operatorValue {
				t.Fatalf("reconcile re-added preview_document (%q) over an operator-set value; Hard Constraint #6 forbids overwriting", operatorValue)
			}
		}
		if got := cfg.Sandbox.ToolPolicies["preview_document"]; got != operatorValue {
			t.Fatalf("operator-set preview_document = %q, reconcile changed it to %q", operatorValue, got)
		}
	}
}

// TestReconcile_PreviewKeyAbsentFromKnownToolsNotReadded (ADR-077): the
// retired-name rule — a tool not in knownTools (here: nothing registered)
// must not gain an entry by reconcile.
func TestReconcile_PreviewKeyAbsentFromKnownToolsNotReadded(t *testing.T) {
	cfg := DefaultConfig()
	delete(cfg.Sandbox.ToolPolicies, "preview_document")

	added := ReconcileToolPolicyCeiling(cfg, map[string]struct{}{})

	if len(added) != 0 {
		t.Fatalf("reconcile with empty knownTools added %v, want none", added)
	}
	if _, ok := cfg.Sandbox.ToolPolicies["preview_document"]; ok {
		t.Fatalf("reconcile re-added preview_document without a knownTools entry; ADR-077 forbids re-adding absent keys")
	}
}

// Omnipus — validateOverrideKeys is a PANIC, and the D2 seed's commit
// ordering depends on that.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This is an INTERNAL test (package coreagent) because validateOverrideKeys is
// unexported. It is a separate file from browser_d2_seed_test.go for a
// language reason, not a stylistic one: pkg/coreagent cannot import pkg/tools
// (pkg/tools -> pkg/workspace -> pkg/coreagent is a cycle), and every
// resolution assertion in that file goes through pkg/tools' real compositor.
// So the posture tests must live in the external coreagent_test package and
// this one must not.

package coreagent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestValidateOverrideKeys_PanicsOnUnknown pins the guard that fixes the
// commit ordering for the whole ADR-072 D2 seed.
//
// validateOverrideKeys PANICS — it does not return an error, and it does not
// log — on an override key that is not in allStaticToolNames. That is the
// reason the per-agent policy maps cannot land in a commit before the catalog
// literal.
//
// It is asserted rather than assumed because the failure it prevents is
// invisible: an override key that matches no catalog name leaves the tool its
// author meant to grant sitting at its `deny` default, and coverage validation
// only ever looks for MISSING keys, never unrecognised ones. If this were ever
// softened to a warning, the ordering constraint every seed comment in this
// package relies on would silently stop existing.
func TestValidateOverrideKeys_PanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("validateOverrideKeys accepted an override for a tool name that is not in " +
				"allStaticToolNames. It MUST panic: a typo'd or retired key leaves the tool the " +
				"author actually meant to override at its deny default with no signal anywhere, " +
				"and the atomic-commit ordering for the D2 browser seed rests on this being a hard, " +
				"immediate failure rather than a log line")
		}
	}()
	validateOverrideKeys(map[string]config.ToolPolicy{
		"browser_selct_option": config.ToolPolicyAllow, // deliberate typo
	})
}

// TestValidateOverrideKeys_AcceptsTheD2Names is the positive control. Without
// it the test above would pass on a build where validateOverrideKeys panicked
// on everything, which is a broken guard rather than a strict one — and it
// would pass just as happily on a build where the five D2 names had been
// dropped from the catalog again.
func TestValidateOverrideKeys_AcceptsTheD2Names(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("validateOverrideKeys rejected a D2 browser tool name: %v. Every one of these "+
				"is seeded in at least one agent's override map, so a rejection here means the "+
				"per-agent seeds panic at first use", r)
		}
	}()
	validateOverrideKeys(map[string]config.ToolPolicy{
		"browser_select_option": config.ToolPolicyAllow,
		"browser_press_key":     config.ToolPolicyAllow,
		"browser_hover":         config.ToolPolicyAllow,
		"browser_snapshot":      config.ToolPolicyAllow,
		"browser_upload_file":   config.ToolPolicyAsk,
	})
}

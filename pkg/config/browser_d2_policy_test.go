// Omnipus — catalog-sync evidence for the D2 browser seed (spec §10 order 2).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WHAT THIS TEST IS, AND — MORE IMPORTANTLY — WHAT IT IS NOT.
//
// It is catalog-sync evidence: every new browser tool name has a policy entry
// somewhere. It is NOT the gate on the seed being CORRECT, and it cannot be,
// because ValidateToolPolicyCoverage is satisfied in both failure directions:
// by the intended seed, and equally by denyAllThenOverride stamping a blanket
// `deny` on all of them. The gate is resolved posture per seeded agent through
// the real compositor — pkg/coreagent/browser_d2_seed_test.go.
//
// IT MUST NOT BOOT A GATEWAY, and that is the whole reason it lives in
// pkg/config rather than pkg/gateway. pkg/gateway's boot path runs
// config.RepairIncompleteToolPolicyCoverage BEFORE
// config.ValidateToolPolicyCoverage; the repair backfills every gap with an
// explicit `deny` first, so a boot-based version of this test reports zero
// gaps on a build with NO SEED AT ALL. Calling the validator directly, on a
// config that has not been through the repair, is what keeps the criterion
// from being empty.

package config

import (
	"strings"
	"testing"
)

// d2BrowserToolNames are the names ADR-072 D2 adds here.
// browser_upload_file is included even though FR-029 holds its registration:
// coverage is about the catalog, not the registry, and its name is seeded.
var d2BrowserToolNames = []string{
	"browser_select_option",
	"browser_press_key",
	"browser_hover",
	"browser_snapshot",
	"browser_upload_file",
	"browser_handle_dialog",
}

// TestToolPolicyCoverage_SixNewBrowserTools_NoGaps calls the validator
// DIRECTLY against DefaultConfig(), with no gateway boot and no repair pass.
//
// The known-tool universe is built here from the real catalog plus the five
// new names, rather than being handed in by a caller: pkg/config cannot import
// pkg/gateway (buildKnownBuiltinToolNames) or pkg/coreagent (import cycle), so
// the names are stated locally and the two cross-package drift tests —
// pkg/coreagent's TestCatalog_MatchesGlobalCeilingEntryForEntry and
// pkg/gateway's TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog
// — are what keep this list honest.
func TestToolPolicyCoverage_SixNewBrowserTools_NoGaps(t *testing.T) {
	cfg := DefaultConfig()

	known := make(map[string]struct{}, len(cfg.Sandbox.ToolPolicies)+len(d2BrowserToolNames))
	for name := range cfg.Sandbox.ToolPolicies {
		known[name] = struct{}{}
	}
	for _, name := range d2BrowserToolNames {
		known[name] = struct{}{}
	}

	gaps := ValidateToolPolicyCoverage(cfg, known)
	if len(gaps) != 0 {
		t.Errorf("ValidateToolPolicyCoverage reports %d gap(s) on a fresh DefaultConfig(): %v",
			len(gaps), gaps)
	}

	// The non-vacuous half. Without this, the assertion above passes on a
	// build where the five names were never added to the ceiling at all —
	// because an unknown name is not a gap, it is simply not in the universe.
	for _, name := range d2BrowserToolNames {
		policy, ok := cfg.Sandbox.ToolPolicies[name]
		if !ok {
			t.Errorf("sandbox.tool_policies has no entry for %q. Coverage would still report zero "+
				"gaps — a name nobody knows about cannot be missing — while every agent resolved "+
				"deny for it through the compositor's fail-closed branch", name)
			continue
		}
		switch policy {
		case "allow", "ask", "deny":
		default:
			t.Errorf("%q is seeded %q, which is not a legal policy value", name, policy)
		}
		if strings.ContainsAny(name, "*?") {
			t.Errorf("%q is a wildcard entry; the static builtin ceiling must be literal and "+
				"wildcard-free (CLAUDE.md hard constraint 6)", name)
		}
	}

	// The one cell of the ceiling that is not `allow`, asserted by value.
	// Attaching a host file to a page on the operator's signed-in session is
	// the only browser action that moves their data outward.
	if got := cfg.Sandbox.ToolPolicies["browser_upload_file"]; got != "ask" {
		t.Errorf("browser_upload_file is seeded %q at the global ceiling, want \"ask\" (FR-021)", got)
	}
}

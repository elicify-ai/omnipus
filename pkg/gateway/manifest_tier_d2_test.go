// Omnipus — the D2 browser tools' tier, asserted rather than assumed.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// A separate file from manifest_tier_partition_test.go because that file is
// written by more than one stream and this assertion was lost once already to a
// concurrent overwrite in a shared worktree.

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestManifestTierPartition_D2BrowserToolsAreTier3 pins ADR D1.9b ruling 3.
//
// WHY IT NEEDS ASSERTING AT ALL: Tier 3 is the RESIDUAL. ToolManifestTier
// returns lazy/search-only for anything not explicitly named in the full,
// previewed or infra sets, so "these six are Tier 3" is free to state and
// impossible to notice being violated. Promoting one of them to previewed is a
// one-line edit in pkg/tools/manifest.go, and this is the only thing checking.
//
// The previewed-set count is asserted alongside because the ruling's OTHER
// half is that ADR-071's previewed set stays at seven — i.e. this change makes
// no production tiering edit at all.
func TestManifestTierPartition_D2BrowserToolsAreTier3(t *testing.T) {
	for _, name := range []string{
		"browser_select_option", "browser_press_key", "browser_hover",
		"browser_snapshot", "browser_upload_file", "browser_handle_dialog",
	} {
		if got := tools.ToolManifestTier(name); got != tools.ManifestLazy {
			t.Errorf("ToolManifestTier(%q) = %v, want ManifestLazy (Tier 3, search-only)", name, got)
		}
		if got := tools.ToolManifestVisibility(name); got != tools.ManifestSearchOnly {
			t.Errorf("ToolManifestVisibility(%q) = %v, want ManifestSearchOnly. A promotion to "+
				"previewed sends this tool's schema on every turn to every agent that holds it, "+
				"which is a context-budget change nobody argued for", name, got)
		}
	}

	if got := len(tools.PreviewedLazyToolNames()); got != 7 {
		t.Errorf("the previewed set has %d names, want 7. ADR D1.9b ruling 3 leaves ADR-071's "+
			"previewed set untouched — a change here means one of the browser tools was promoted "+
			"into it, which is the edit the ruling declined to make", got)
	}

	// The catalog half: a name that never reached buildKnownBuiltinToolNames
	// still "resolves" Tier 3 by residual, so the assertions above would pass
	// for a tool the gateway has never heard of.
	catalog := buildKnownBuiltinToolNames()
	for _, name := range []string{
		"browser_select_option", "browser_press_key", "browser_hover",
		"browser_snapshot", "browser_upload_file", "browser_handle_dialog",
	} {
		if _, ok := catalog[name]; !ok {
			t.Errorf("%q is not in buildKnownBuiltinToolNames(). Every tier assertion above would "+
				"still pass for it, because Tier 3 is the residual and answers for names that do "+
				"not exist", name)
		}
	}
}

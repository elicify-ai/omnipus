package coreagent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestSeedConfig_FreshInstall_ZeroToolPolicyCoverageGaps enforces CLAUDE.md
// hard constraint 6 end-to-end against the REAL boot composition
// (config.DefaultConfig() — which populates the global sandbox.tool_policies
// ceiling — followed by SeedConfig, exactly mirroring
// pkg/gateway/gateway.go's RunContextWithOptions boot sequence): a
// fresh-install config must have zero (agent x tool) coverage gaps across
// the full static builtin catalog. TestBuildKnownBuiltinToolNames_
// MatchesCoreagentStaticToolCatalog (pkg/gateway/tool_catalog_drift_test.go)
// separately guards that AllStaticToolNames() itself doesn't drift from the
// live tool registry; this test guards that the SEEDED policy data actually
// covers that catalog for every agent SeedConfig produces (core + the
// generic subagent tier, whose sparse per-agent policy maps rely on the
// global ceiling for full coverage — see tightenGlobalCeiling's doc
// comment). Added alongside the ADR-041 tab tools
// (browser_list_tabs/browser_switch_tab/browser_close_tab); would have
// caught a missed seed for any of the three.
func TestSeedConfig_FreshInstall_ZeroToolPolicyCoverageGaps(t *testing.T) {
	cfg := config.DefaultConfig()
	if !SeedConfig(cfg) {
		t.Fatal("SeedConfig reported no-op on a fresh DefaultConfig() — expected a fresh seed")
	}

	known := make(map[string]struct{})
	for _, name := range AllStaticToolNames() {
		known[name] = struct{}{}
	}

	gaps := config.ValidateToolPolicyCoverage(cfg, known)
	if len(gaps) != 0 {
		for _, g := range gaps {
			t.Logf("gap: %s", g.String())
		}
		t.Fatalf("expected zero tool-policy coverage gaps after SeedConfig, got %d", len(gaps))
	}
}

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// --- browserWarmUpEnabled ---------------------------------------------------

// warmUpEligibleConfig returns a config that SHOULD satisfy
// browserWarmUpEnabled, so each test below can negate exactly one input and
// attribute the result to that input alone.
//
// It must NOT be a bare &config.Config{}: warm-up is gated on
// Tools.Browser.WarmAtBoot, whose "default true" lives in
// config.DefaultConfig() (pkg/config/defaults.go), not in the Go zero value.
// A zero-valued struct therefore has WarmAtBoot == false and is NOT eligible
// — an earlier revision of these tests built one and asserted eligibility,
// which failed for a reason that had nothing to do with what each test was
// actually trying to prove. TestBrowserWarmUpEnabled_DefaultConfigEnablesIt
// below pins the real default separately, against DefaultConfig() itself.
func warmUpEligibleConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Tools.Browser.Enabled = true
	cfg.Tools.Browser.WarmAtBoot = true
	// CDPURL left empty (local managed mode) — the common case.
	return cfg
}

// --- findSharedBrowserCoordinator -------------------------------------------

func newTestBrowserManager(t *testing.T) *browser.BrowserManager {
	t.Helper()
	cfg, err := browser.DefaultConfig()
	if err != nil {
		t.Fatalf("browser.DefaultConfig: %v", err)
	}
	cfg.ProfileDir = t.TempDir()
	mgr, err := browser.NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	if err != nil {
		t.Fatalf("browser.NewBrowserManager: %v", err)
	}
	return mgr
}

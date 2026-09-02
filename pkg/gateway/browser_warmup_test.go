package gateway

// Fix Wave B, Task 2 regression coverage: the shared Chrome coordinator must
// be launched at gateway boot (not left entirely lazy until an agent's first
// browser tool call), gated on cfg.Tools.Browser.Enabled/CDPURL and
// OMNIPUS_SKIP_BROWSER_PREPROVISION=1. browserWarmUpEnabled and
// findSharedBrowserCoordinator (gateway.go) are the exact decision/lookup
// logic RunContextWithOptions' boot-time warm-up block calls — extracted so
// they are unit-testable here without booting a full gateway (which would
// additionally require either a testutil harness option this task's file
// ownership does not include, or re-implementing credential-store seeding
// from scratch). The launch mechanism itself (BrowserCoordinator.WarmUp
// actually starting a real Chrome process) is covered by
// pkg/tools/browser/warmup_test.go's
// TestBrowserCoordinator_WarmUp_LaunchesRealChrome.

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

// TestBrowserWarmUpEnabled_DefaultConfigEnablesIt pins the shipped default
// against the REAL config constructor rather than a hand-built literal: a
// fresh install must warm Chrome at boot. If someone flips the default in
// pkg/config/defaults.go, this fails — the hand-built configs in the other
// tests here would not notice.
func TestBrowserWarmUpEnabled_DefaultConfigEnablesIt(t *testing.T) {
	cfg := config.DefaultConfig()
	if !cfg.Tools.Browser.WarmAtBoot {
		t.Fatal("expected tools.browser.warm_at_boot to default TRUE in DefaultConfig()")
	}
	if !cfg.Tools.Browser.Enabled {
		t.Fatal("precondition: expected tools.browser.enabled to default true")
	}
	if !browserWarmUpEnabled(cfg) {
		t.Fatal("expected a default config to be warm-up eligible")
	}
}

func TestBrowserWarmUpEnabled_DefaultLocalConfig(t *testing.T) {
	if !browserWarmUpEnabled(warmUpEligibleConfig()) {
		t.Fatal("expected warm-up enabled for a local (non-remote-CDP), enabled browser config")
	}
}

// TestBrowserWarmUpEnabled_WarmAtBootFalseDisablesIt covers the operator
// opt-out itself — the whole point of the config field. Turning it off must
// disable EAGER warm-up while leaving browser tools available (Chrome then
// launches lazily at first use); this predicate is the eager half.
func TestBrowserWarmUpEnabled_WarmAtBootFalseDisablesIt(t *testing.T) {
	cfg := warmUpEligibleConfig()
	cfg.Tools.Browser.WarmAtBoot = false

	if browserWarmUpEnabled(cfg) {
		t.Fatal("expected warm-up DISABLED when tools.browser.warm_at_boot is false")
	}
}

func TestBrowserWarmUpEnabled_RemoteCDPURLDisablesWarmUp(t *testing.T) {
	cfg := warmUpEligibleConfig()
	cfg.Tools.Browser.CDPURL = "ws://remote-chrome:9222"

	if browserWarmUpEnabled(cfg) {
		t.Fatal(
			"expected warm-up DISABLED when tools.browser.cdp_url is set — must never launch a local Chrome in remote-CDP mode",
		)
	}
}

func TestBrowserWarmUpEnabled_ToolDisabledDisablesWarmUp(t *testing.T) {
	cfg := warmUpEligibleConfig()
	cfg.Tools.Browser.Enabled = false

	if browserWarmUpEnabled(cfg) {
		t.Fatal("expected warm-up DISABLED when tools.browser.enabled is false")
	}
}

func TestBrowserWarmUpEnabled_SkipEnvVarFullyShortCircuits(t *testing.T) {
	cfg := warmUpEligibleConfig()

	t.Setenv("OMNIPUS_SKIP_BROWSER_PREPROVISION", "1")

	if browserWarmUpEnabled(cfg) {
		t.Fatal(
			"expected OMNIPUS_SKIP_BROWSER_PREPROVISION=1 to fully disable warm-up regardless of an otherwise-eligible config",
		)
	}
}

func TestBrowserWarmUpEnabled_SkipEnvVarOnlyHonorsExactValueOne(t *testing.T) {
	cfg := warmUpEligibleConfig()

	t.Setenv("OMNIPUS_SKIP_BROWSER_PREPROVISION", "true") // not the literal "1"

	if !browserWarmUpEnabled(cfg) {
		t.Fatal("expected only the literal value \"1\" to trip the skip — got a false positive on \"true\"")
	}
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

func TestFindSharedBrowserCoordinator_EmptySlice(t *testing.T) {
	if got := findSharedBrowserCoordinator(nil); got != nil {
		t.Fatalf("expected nil for an empty manager list, got %v", got)
	}
}

func TestFindSharedBrowserCoordinator_NoManagerHasACoordinator(t *testing.T) {
	// Managers never attached via AttachSharedChrome (remote-CDP-override or
	// legacy no-coordinator mode) — Coordinator() is nil on every one.
	mgrs := []*browser.BrowserManager{newTestBrowserManager(t), newTestBrowserManager(t), nil}
	if got := findSharedBrowserCoordinator(mgrs); got != nil {
		t.Fatalf("expected nil when no manager has a coordinator attached, got %v", got)
	}
}

func TestFindSharedBrowserCoordinator_FindsTheSharedInstance(t *testing.T) {
	coordCfg, err := browser.DefaultConfig()
	if err != nil {
		t.Fatalf("browser.DefaultConfig: %v", err)
	}
	coord := browser.NewBrowserCoordinator(t.TempDir(), coordCfg)

	// Realistic shape: some agents' managers never got attached (e.g. an
	// earlier registration error), one or more share the SAME coordinator —
	// exactly loop.go's per-key AttachSharedChrome(coordinator, key)
	// wiring (every agent's manager attached to the ONE gateway-scoped
	// coordinator instance).
	unattached := newTestBrowserManager(t)
	attachedA := newTestBrowserManager(t)
	attachedA.AttachSharedChrome(coord, browserTestKey(t, "agent-a"))
	attachedB := newTestBrowserManager(t)
	attachedB.AttachSharedChrome(coord, browserTestKey(t, "agent-b"))

	got := findSharedBrowserCoordinator([]*browser.BrowserManager{unattached, attachedA, attachedB})
	if got == nil {
		t.Fatal("expected a non-nil coordinator")
	}
	if got != coord {
		t.Fatal("expected the exact SAME shared coordinator instance every attached manager points to")
	}
}

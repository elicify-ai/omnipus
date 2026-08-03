package browser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression coverage for the headless MODE the managed Chrome launches in.
//
// Bare `--headless` is still resolved by Chrome to OLD headless: a separate,
// cut-down engine that (a) is the single strongest automation fingerprint a
// bot detector can read, (b) sets navigator.webdriver NON-OVERRIDABLY, so
// neither stealthInitScript nor --disable-blink-features=AutomationControlled
// can mask it (see stealthInitScript's own "effectiveness caveat" in
// manager.go), and (c) lacks the full rendering/media stack the WebRTC capture
// path depends on.
//
// The rest of this package already ASSUMED new headless — live.go's screencast
// attach path calls it "the WebRTC-capable build ADR-047 D2 switched managed
// launches to", coordinator.go calls it "new headless" — while the launch site
// quietly asked for the old one. That mismatch is half of the operator's
// 2026-08-03 report ("captchas on google and youtube, streaming videos not
// working at all"); the other half was the runtime image shipping codec-less
// Alpine Chromium instead of full Chrome (docker/Dockerfile.heavy).
//
// A one-word regression here is invisible in review and expensive in the
// field, which is why it is pinned.

func headlessArgs(t *testing.T, headless bool) []string {
	t.Helper()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	cfg.Headless = headless
	return managedExecAllocatorOpts(cfg).Args
}

func TestManagedChrome_UsesNewHeadless(t *testing.T) {
	args := headlessArgs(t, true)

	assert.Contains(t, args, "--headless=new",
		"managed Chrome must launch in NEW headless — old headless fingerprints as automation "+
			"and sets navigator.webdriver non-overridably")

	// The bare form must not appear: Chrome reads it as OLD headless, which is
	// exactly the mode this guards against.
	for _, a := range args {
		assert.NotEqual(t, "--headless", a,
			"bare --headless means OLD headless to Chrome; use --headless=new")
	}
}

// TestManagedChrome_HeadfulOmitsHeadlessFlag — the flag is conditional, and a
// headful launch must not carry any headless form at all.
func TestManagedChrome_HeadfulOmitsHeadlessFlag(t *testing.T) {
	for _, a := range headlessArgs(t, false) {
		assert.False(t, strings.HasPrefix(a, "--headless"),
			"a headful launch must not pass any --headless flag, got %q", a)
	}
}

// TestManagedChrome_KeepsAutomationControlledDisabled pins the companion flag.
// New headless alone does not hide the automation bit; this flag plus
// stealthInitScript are what make the webdriver override land at all.
func TestManagedChrome_KeepsAutomationControlledDisabled(t *testing.T) {
	assert.Contains(t, headlessArgs(t, true), "--disable-blink-features=AutomationControlled",
		"the automation-controlled blink feature must stay disabled — without it the "+
			"webdriver override cannot take effect even on new headless")
}

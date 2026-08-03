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
	return managedExecAllocatorOpts(cfg, "151").Args
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

// --- launch-level User-Agent -------------------------------------------------
//
// Chrome's headless User-Agent literally contains the token "HeadlessChrome",
// which is the single most obvious bot signal a site can read — Google gates on
// it directly. applyStealth already rewrote it per tab, but measured live on
// UAT v46 the browser STILL reported
// "…HeadlessChrome/151.0.0.0 Safari/537.36", for two reasons:
//
//  1. coverage — applyStealth runs from createTab only, while the coordinator
//     builds each agent's FIRST window through its own CreateTarget path, so
//     the tab the user actually browses in never got the override; and
//  2. race — where it does run it lands after the target is bound, so an early
//     navigator.userAgent read still sees the headless string.
//
// The launch flag closes both. These pin it.

func TestManagedChrome_OverridesHeadlessUserAgent(t *testing.T) {
	args := headlessArgs(t, true)

	var ua string
	for _, a := range args {
		if strings.HasPrefix(a, "--user-agent=") {
			ua = strings.TrimPrefix(a, "--user-agent=")
		}
	}
	require.NotEmpty(t, ua, "headless launches must set --user-agent — Chrome's own headless UA "+
		"contains the HeadlessChrome token that gates like Google's read directly")
	assert.NotContains(t, ua, "Headless",
		"the launch User-Agent must not carry any Headless token, got %q", ua)
	assert.Contains(t, ua, "Chrome/151.", "the UA must report the real browser major version")
}

// TestManagedChrome_NoUserAgentOverrideWhenVersionUnknown — a hardcoded or
// guessed version is its own mismatch signal (a UA claiming a version the
// binary does not have). Unknown must degrade to Chrome's own UA plus the
// existing per-tab rewrite, not a fabricated one.
func TestManagedChrome_NoUserAgentOverrideWhenVersionUnknown(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	cfg.Headless = true

	for _, a := range managedExecAllocatorOpts(cfg, "").Args {
		assert.False(t, strings.HasPrefix(a, "--user-agent="),
			"an unknown Chrome version must NOT produce a guessed User-Agent, got %q", a)
	}
}

func TestManagedChrome_HeadfulSetsNoUserAgent(t *testing.T) {
	for _, a := range headlessArgs(t, false) {
		assert.False(t, strings.HasPrefix(a, "--user-agent="),
			"a headful launch already sends a normal UA and must not override it, got %q", a)
	}
}

func TestChromeMajorFromVersionOutput(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Google Chrome for Testing 151.0.7922.71", "151"},
		{"Chromium 149.0.7827.53", "149"},
		{"Google Chrome 151.0.7922.71\n", "151"},
		{"", ""},
		{"not a version line", ""},
	} {
		assert.Equal(t, tc.want, chromeMajorFromVersionOutput(tc.in), "input %q", tc.in)
	}
}

// TestDesktopUserAgent_LooksLikeRealChrome — the replacement must be a
// plausible desktop Chrome UA, not merely "not headless".
func TestDesktopUserAgent_LooksLikeRealChrome(t *testing.T) {
	ua := desktopUserAgent("151")
	assert.NotContains(t, ua, "Headless")
	for _, want := range []string{"Mozilla/5.0", "X11; Linux x86_64", "AppleWebKit/537.36", "Chrome/151.0.0.0", "Safari/537.36"} {
		assert.Contains(t, ua, want)
	}
}

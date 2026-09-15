package browser

// focus_emulation_test.go — review finding F9 (2026-08-13): focus emulation
// must be part of the treatment on EVERY path that changes which tab Chrome is
// compositing for, and must be released from the tab being left.
//
// The gap: Emulation.setFocusEmulationEnabled(true) was applied in exactly one
// place, CaptureSession.bringAgentTabToFront (capture start, and its one warm
// re-assert). The tab-switch path — BrowserManager.activateTabInChrome — did
// Page.bringToFront and nothing else, and browser_open_tab told Chrome nothing
// at all. Since a tab change fires onTabsChanged → CaptureSession.Recapture,
// the encoder re-bound to a tab under a DIFFERENT rendering regime than the
// one capture start had established, one browser_switch_tab after start. The
// tab being left, meanwhile, kept its emulation forever.
//
// What was measured, so the next reader does not over- or under-claim (this
// project's own Chrome, headless, rAF ticks under a full-viewport animation):
//
//   - Releasing the OLD tab is a real, reproducible win: a tab switched away
//     from kept running at 25–35 rAF/s while still emulated, and dropped to
//     0 rAF/s the moment emulation was cleared (4/4 paired trials). Nothing
//     captures or displays a background tab, so that is pure waste.
//   - Emulating the NEW tab was NOT measurably faster in that environment: a
//     brought-to-front tab ran at 60 rAF/s with and without it (6/6 trials).
//     So these tests pin CONSISTENCY — every foregrounded tab gets identical
//     treatment — not a claimed framerate gain on the switched-TO tab. The
//     framerate claim in capture_session.go's original comment did not
//     reproduce and must not be leaned on.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFocusTreatmentActions pins the two action sequences themselves, so a
// future edit that drops the emulation half from foregroundTabActions (the
// original defect) fails here even if every call site is still wired up.
func TestFocusTreatmentActions(t *testing.T) {
	assert.Equal(t, "foreground", focusTreatment(foregroundTabActions()),
		"foregroundTabActions must bring the tab to front AND enable focus emulation")
	assert.Equal(t, "background", focusTreatment(backgroundTabActions()),
		"backgroundTabActions must disable focus emulation and must NOT bring anything to front")
}

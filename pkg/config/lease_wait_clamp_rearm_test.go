// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"strings"
	"testing"
)

// lease_wait_clamp_rearm_test.go — the half of FR-023a's "the WARN is part of
// the requirement" that the original tests could not see.
//
// The clamp warning was throttled to once per PROCESS. There was a
// ResetLeaseWaitClampWarnForReload to re-arm it, documented as "called by the
// config-reload path", and no production code anywhere ever called it — the
// existing clamp tests call it themselves between their two halves, so they
// stayed green over a throttle that in a real gateway never re-armed at all.
//
// What that cost an operator: they set lease_wait too high, saw the warning,
// changed it to a DIFFERENT still-too-high value, saved, and got silence. The
// same silence an operator whose value was fine gets. That is the ADR-037
// "reports success and changes nothing" shape the clamp warning exists to
// prevent, reintroduced by the thing meant to prevent it.
//
// The throttle now keys on the configured (lease_wait, page_timeout) pair, so it
// re-arms itself on any edit and needs no cooperation from a reload path that
// could be — and was — left unwired.

// TestConfig_LeaseWaitClampWarnsAgainForADifferentBadValue is the discriminating
// case, and the reset call is deliberately ABSENT between the two clamps: that
// absence is the whole test. With the once-per-process throttle the second clamp
// is silent and this fails.
func TestConfig_LeaseWaitClampWarnsAgainForADifferentBadValue(t *testing.T) {
	ResetLeaseWaitClampWarnForReload()
	t.Cleanup(ResetLeaseWaitClampWarnForReload)

	first := captureWarnings(t)
	if got := (BrowserToolConfig{LeaseWaitSec: 45, PageTimeoutSec: 30}).EffectiveLeaseWaitSec(); got != 15 {
		t.Fatalf("lease_wait=45 against page_timeout=30 resolved to %d, want 15", got)
	}
	if out := first.String(); !strings.Contains(out, "45") {
		t.Fatalf("the first clamp must warn naming the configured value.\nCaptured log:\n%s", out)
	}

	// The operator reads the warning and changes lease_wait to another value
	// that is still above the ceiling. No reset here — a real gateway has none.
	second := captureWarnings(t)
	if got := (BrowserToolConfig{LeaseWaitSec: 60, PageTimeoutSec: 30}).EffectiveLeaseWaitSec(); got != 15 {
		t.Fatalf("lease_wait=60 against page_timeout=30 resolved to %d, want 15", got)
	}
	out := second.String()
	if !strings.Contains(out, "60") {
		t.Fatalf("an operator who fixed one out-of-range lease_wait and introduced another got "+
			"NO warning for the new one — the same log an operator with a valid value sees. "+
			"The clamp must warn again whenever the configured value changes.\nCaptured log:\n%s", out)
	}
}

// TestConfig_LeaseWaitClampStaysQuietForAnUnchangedValue is the other direction,
// and without it the test above is satisfied by deleting the throttle entirely.
// Every Settings save re-applies the clamp, and a warning per save for a
// condition nobody changed is how a real warning gets tuned out.
func TestConfig_LeaseWaitClampStaysQuietForAnUnchangedValue(t *testing.T) {
	ResetLeaseWaitClampWarnForReload()
	t.Cleanup(ResetLeaseWaitClampWarnForReload)

	cfg := BrowserToolConfig{LeaseWaitSec: 45, PageTimeoutSec: 30}

	first := captureWarnings(t)
	cfg.EffectiveLeaseWaitSec()
	if out := first.String(); !strings.Contains(out, "45") {
		t.Fatalf("the first clamp of a value must warn.\nCaptured log:\n%s", out)
	}

	for i := 0; i < 3; i++ {
		repeat := captureWarnings(t)
		cfg.EffectiveLeaseWaitSec()
		if out := strings.TrimSpace(repeat.String()); out != "" {
			t.Fatalf("re-applying the clamp to the SAME configured pair warned again on save %d; "+
				"a line per Settings save for an unchanged condition is noise.\nCaptured log:\n%s",
				i+2, out)
		}
	}
}

// TestConfig_LeaseWaitClampWarnsAgainWhenPageTimeoutMovesUnderIt covers the
// other half of the pair. The operator never touched lease_wait; they lowered
// page_timeout, which is what pulled the ceiling down under a value that was
// fine before. Keying the throttle on lease_wait alone would go silent here.
func TestConfig_LeaseWaitClampWarnsAgainWhenPageTimeoutMovesUnderIt(t *testing.T) {
	ResetLeaseWaitClampWarnForReload()
	t.Cleanup(ResetLeaseWaitClampWarnForReload)

	first := captureWarnings(t)
	if got := (BrowserToolConfig{LeaseWaitSec: 20, PageTimeoutSec: 30}).EffectiveLeaseWaitSec(); got != 15 {
		t.Fatalf("lease_wait=20 against page_timeout=30 resolved to %d, want 15", got)
	}
	if out := first.String(); !strings.Contains(out, "30") {
		t.Fatalf("the first clamp must warn naming page_timeout.\nCaptured log:\n%s", out)
	}

	second := captureWarnings(t)
	if got := (BrowserToolConfig{LeaseWaitSec: 20, PageTimeoutSec: 10}).EffectiveLeaseWaitSec(); got != 5 {
		t.Fatalf("lease_wait=20 against page_timeout=10 resolved to %d, want 5", got)
	}
	if out := second.String(); !strings.Contains(out, "10") {
		t.Fatalf("lowering page_timeout tightened the ceiling under an untouched lease_wait and "+
			"warned nothing.\nCaptured log:\n%s", out)
	}
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestConfig_LeaseWaitClampedAgainstPageTimeout is FR-023a: the clamp fires at
// LOAD and again on RELOAD, and it is LOUD.
//
// A silent clamp is a setting the operator believes took effect and did not —
// the ADR-037 anti-pattern this project bans. So the WARN is part of the
// requirement rather than a nicety, and it is asserted with the same weight as
// the returned number.
func TestConfig_LeaseWaitClampedAgainstPageTimeout(t *testing.T) {
	// --- At LOAD -------------------------------------------------------
	ResetLeaseWaitClampWarnForReload()
	t.Cleanup(ResetLeaseWaitClampWarnForReload)

	logs := captureWarnings(t)

	cfg := BrowserToolConfig{LeaseWaitSec: 45, PageTimeoutSec: 30}
	got := cfg.EffectiveLeaseWaitSec()
	if got != 15 {
		t.Fatalf("lease_wait=45 against page_timeout=30 resolved to %d, want 15 (half the page timeout)", got)
	}

	out := logs.String()
	if !strings.Contains(out, "lease_wait") {
		t.Fatalf("the clamp did not name tools.browser.lease_wait. A silent clamp is a setting the operator thinks took effect and did not.\nCaptured log:\n%s", out)
	}
	// BOTH keys and BOTH values. "lease_wait was lowered", without the
	// page_timeout that lowered it, sends someone to change lease_wait again
	// and watch it be lowered again.
	for _, needle := range []string{"page_timeout", "45", "30", "15"} {
		if !strings.Contains(out, needle) {
			t.Errorf("the clamp WARN does not contain %q — it must name both keys and both values, or an operator cannot tell what lowered their setting or to what.\nCaptured log:\n%s", needle, out)
		}
	}

	// --- On RELOAD -----------------------------------------------------
	//
	// The clamp is not a load-time transformation applied once; it re-applies
	// every time the config is re-read, because an operator editing lease_wait
	// in Settings must not end up with an unclamped value live in the process.
	// And the warning re-arms, so the edit they just made is the one they get
	// told about — the moment they are actually looking.
	ResetLeaseWaitClampWarnForReload()
	reloadLogs := captureWarnings(t)

	reloaded := BrowserToolConfig{LeaseWaitSec: 60, PageTimeoutSec: 20}
	if got := reloaded.EffectiveLeaseWaitSec(); got != 10 {
		t.Fatalf("on reload, lease_wait=60 against page_timeout=20 resolved to %d, want 10", got)
	}
	reloadOut := reloadLogs.String()
	if !strings.Contains(reloadOut, "60") || !strings.Contains(reloadOut, "20") {
		t.Fatalf("the reload clamp did not warn with the NEW values (60 / 20). An operator who edits the value and saves sees nothing and concludes it took effect.\nCaptured log:\n%s", reloadOut)
	}
}

// TestConfig_LeaseWaitUnderTheCeilingIsUntouchedAndSilent is the other
// direction, and without it the test above is satisfied by a function that
// clamps everything and warns always.
func TestConfig_LeaseWaitUnderTheCeilingIsUntouchedAndSilent(t *testing.T) {
	ResetLeaseWaitClampWarnForReload()
	t.Cleanup(ResetLeaseWaitClampWarnForReload)

	logs := captureWarnings(t)

	cfg := BrowserToolConfig{LeaseWaitSec: 5, PageTimeoutSec: 30}
	if got := cfg.EffectiveLeaseWaitSec(); got != 5 {
		t.Fatalf("lease_wait=5 against page_timeout=30 resolved to %d, want 5 (well under the ceiling of 15 — an in-range value must be honoured exactly)", got)
	}
	if strings.Contains(logs.String(), "lease_wait is configured above") {
		t.Fatalf("the clamp warned about a value it did not change. A warning that fires when nothing happened is how a real warning stops being read.\nCaptured log:\n%s", logs.String())
	}
}

// TestConfig_LeaseWaitUnsetPageTimeoutUsesTheBrowserDefault pins the
// open-question answer, because getting it wrong is silent and total.
//
// tools.browser.page_timeout is 0 on almost every install — unset, meaning the
// browser package's own 30s default is in force. Clamping against that
// configured 0 would make the ceiling 0 and reduce EVERY lease wait to nothing,
// turning every concurrent browser call into an instant contention refusal on
// every default installation. The clamp uses the browser default as the
// denominator instead.
func TestConfig_LeaseWaitUnsetPageTimeoutUsesTheBrowserDefault(t *testing.T) {
	ResetLeaseWaitClampWarnForReload()
	t.Cleanup(ResetLeaseWaitClampWarnForReload)

	cfg := BrowserToolConfig{LeaseWaitSec: 2, PageTimeoutSec: 0}
	got := cfg.EffectiveLeaseWaitSec()
	if got == 0 {
		t.Fatal("with page_timeout unset, the clamp reduced lease_wait to 0 — it clamped against the configured 0 rather than the browser package's own default, which turns every concurrent browser call into an instant contention refusal on every default install")
	}
	if got != 2 {
		t.Fatalf("lease_wait=2 with page_timeout unset resolved to %d, want 2 (the ceiling is half the 30s browser default, i.e. 15)", got)
	}
}

// TestConfig_LeaseWaitUnsetUsesTheDefaultNotZero: 0 means "unset", not "do not
// wait". A zero returned here would make every concurrent call an instant
// refusal.
func TestConfig_LeaseWaitUnsetUsesTheDefaultNotZero(t *testing.T) {
	ResetLeaseWaitClampWarnForReload()
	t.Cleanup(ResetLeaseWaitClampWarnForReload)

	cfg := BrowserToolConfig{}
	got := cfg.EffectiveLeaseWaitSec()
	if got <= 0 {
		t.Fatalf("an unset lease_wait resolved to %d — 0 means \"not configured\", never \"do not wait\"; returning it makes every concurrent browser call an instant contention refusal", got)
	}
	if got != defaultLeaseWaitSec {
		t.Fatalf("an unset lease_wait resolved to %d, want the default %d", got, defaultLeaseWaitSec)
	}
}

// TestBrowserToolConfig_NewKeysHaveFullyQualifiedEnvTags guards the
// TRUST_PATH_CHROME bug for the two keys added here.
//
// BrowserToolConfig embeds ToolConfig with envPrefix:"OMNIPUS_TOOLS_BROWSER_",
// so a RELATIVE env tag on a sibling field is double-prefixed into
// OMNIPUS_TOOLS_BROWSER_OMNIPUS_TOOLS_BROWSER_X. That shipped once already and
// was invisible: nothing fails, the variable simply never takes effect.
//
// The existing TestBrowserToolConfig_EnvKeys_NoDoublePrefix want-map is
// ONE-DIRECTIONAL — its loops never flag a field that is simply absent from the
// map — so a new field with a mistyped tag ships green there. This asserts on
// the struct tags themselves, which cannot be satisfied by forgetting to list
// something.
func TestBrowserToolConfig_NewKeysHaveFullyQualifiedEnvTags(t *testing.T) {
	want := map[string]string{
		"LeaseWaitSec":      "OMNIPUS_TOOLS_BROWSER_LEASE_WAIT",
		"ActionabilityGate": "OMNIPUS_TOOLS_BROWSER_ACTIONABILITY_GATE",
	}
	typ := reflect.TypeOf(BrowserToolConfig{})

	for field, wantEnv := range want {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Errorf("BrowserToolConfig has no field %s", field)
			continue
		}
		gotEnv := f.Tag.Get("env")
		if gotEnv != wantEnv {
			t.Errorf("BrowserToolConfig.%s env tag = %q, want the FULLY QUALIFIED %q. A relative tag is double-prefixed by the embedded ToolConfig's envPrefix into OMNIPUS_TOOLS_BROWSER_%s — which fails silently: nothing errors, the variable just never takes effect.",
				field, gotEnv, wantEnv, gotEnv)
		}
		if f.Tag.Get("json") == "" {
			t.Errorf("BrowserToolConfig.%s has no json tag, so the key is unreachable from config.json", field)
		}
	}
}

// TestBrowserToolConfig_ActionabilityGateDocumentsItsRemoval.
//
// tools.browser.actionability_gate is a REVERT SWITCH, not a feature. It exists
// so an operator whose site regresses under the stricter gate has something to
// turn while it is diagnosed, and it is meant to be deleted once the full gate
// has soaked. A revert switch with no removal plan recorded next to it becomes
// a permanent second code path that every future change has to keep working —
// which is how a temporary flag turns into a permanent maintenance cost.
func TestBrowserToolConfig_ActionabilityGateDocumentsItsRemoval(t *testing.T) {
	src := readRepoFile(t, "config.go")
	idx := strings.Index(src, "ActionabilityGate string")
	if idx < 0 {
		t.Fatal("ActionabilityGate's declaration was not found in config.go")
	}
	// Look back over the doc comment attached to the field.
	start := idx - 2200
	if start < 0 {
		start = 0
	}
	doc := src[start:idx]

	for _, needle := range []string{"revert switch", "REMOVED"} {
		if !strings.Contains(doc, needle) {
			t.Errorf("ActionabilityGate's doc comment does not say %q. It is a temporary revert switch; without a recorded removal intent it becomes a permanent second code path.", needle)
		}
	}
	if !strings.Contains(doc, "visible_only") || !strings.Contains(doc, `"full"`) {
		t.Error("ActionabilityGate's doc comment does not enumerate its accepted values (full / visible_only), so an operator cannot know what to set it to")
	}
}

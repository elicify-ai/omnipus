// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// FR-042 / FR-043: browser_evaluate has EXACTLY ONE switch, it is seeded ON,
// and an operator's explicit OFF survives being saved.
//
// There used to be two. sandbox.browser_evaluate_enabled was the live one;
// tools.browser.evaluate_enabled was a field with a JSON tag, an env var, and
// no production reader at all — while a live, operator-visible string in
// pkg/sysagent named it as though it were the control. An operator who found
// that name, set it, and restarted would have changed nothing, with no error
// and no warning.

// TestDefaultConfig_BrowserEvaluateEnabledSeededTrue: a fresh install has the
// tool working.
//
// Before this, browser_evaluate was registered, advertised to the model, and
// allowed by Jim's policy — and then refused at execution with a message about
// a config setting nobody had heard of. A capability that is on in every list
// and off in reality is worse than one that is absent.
func TestDefaultConfig_BrowserEvaluateEnabledSeededTrue(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Sandbox.BrowserEvaluateEnabled == nil {
		t.Fatal("DefaultConfig() leaves sandbox.browser_evaluate_enabled nil, which resolves to FALSE — a fresh install would register browser_evaluate, show it to the model, allow it by policy, and then refuse it at execution")
	}
	if !*cfg.Sandbox.BrowserEvaluateEnabled {
		t.Fatal("DefaultConfig() seeds sandbox.browser_evaluate_enabled = false, want true")
	}
	if !ResolveBool(cfg.Sandbox.BrowserEvaluateEnabled, false) {
		t.Fatal("the seeded value does not resolve to true through ResolveBool — this is the exact call the sole production consumer makes")
	}
}

// TestResolveBool_BrowserEvaluateEnabled_NilMeansFalse pins the resolution
// direction, which is the OPPOSITE of the field the pointer shape was borrowed
// from.
//
// PathGuardAuditFailClosed resolves nil -> TRUE (fail closed). Copying that
// literally here would ship arbitrary in-page JavaScript ON in every
// construction that skips DefaultConfig() — a test harness, an embedder, a
// hand-written config fragment. The default belongs in the SEED, which is data
// an operator can edit, never in the resolution.
func TestResolveBool_BrowserEvaluateEnabled_NilMeansFalse(t *testing.T) {
	var unset *bool
	if ResolveBool(unset, false) {
		t.Fatal("an unset sandbox.browser_evaluate_enabled resolves to TRUE. The seed is where the default lives; a nil that resolves true turns arbitrary in-page JavaScript on in every construction that skips DefaultConfig()")
	}

	// And the pointer shape's whole purpose: false and nil are distinguishable.
	off := false
	on := true
	if ResolveBool(&off, false) {
		t.Fatal("an explicit false resolved to true")
	}
	if !ResolveBool(&on, false) {
		t.Fatal("an explicit true resolved to false")
	}
}

// TestConfig_ExplicitBrowserEvaluateFalseSurvivesSaveRoundTrip is the
// persistence regression, and it is the reason the type is a pointer.
//
// As a plain `bool` with `omitempty`, an operator's explicit false was
// INDISTINGUISHABLE from "not set" at marshal time and was dropped on every
// SaveConfig. The kill switch silently reverted to the seeded true on the next
// config write — from Settings, from a CLI command, from anything that saved.
// The operator had turned it off, seen it stay off until something else
// happened to save, and then found it on again with nothing in any log.
func TestConfig_ExplicitBrowserEvaluateFalseSurvivesSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	off := false
	cfg.Sandbox.BrowserEvaluateEnabled = &off

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Assert on the BYTES, not only on the reloaded struct. This is where the
	// defect actually lived: omitempty dropped the key at marshal time, and a
	// reload then produced nil, which resolves to false — so a struct-only
	// assertion could pass while the operator's setting was gone from disk and
	// the next DefaultConfig-based merge would restore true.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var onDisk map[string]any
	if unmarshalErr := json.Unmarshal(raw, &onDisk); unmarshalErr != nil {
		t.Fatalf("unmarshal saved config: %v", unmarshalErr)
	}
	sandbox, _ := onDisk["sandbox"].(map[string]any)
	if sandbox == nil {
		t.Fatal("the saved config has no sandbox section at all")
	}
	got, present := sandbox["browser_evaluate_enabled"]
	if !present {
		t.Fatal("an explicitly-set browser_evaluate_enabled=false was DROPPED from the saved JSON. omitempty cannot tell false from unset on a plain bool, so the operator's kill switch silently reverted to the seeded true on the next config write.")
	}
	if got != false {
		t.Fatalf("saved browser_evaluate_enabled = %v, want false", got)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Sandbox.BrowserEvaluateEnabled == nil {
		t.Fatal("the reloaded config has a nil browser_evaluate_enabled after an explicit false was saved")
	}
	if *loaded.Sandbox.BrowserEvaluateEnabled {
		t.Fatal("the reloaded config has browser_evaluate_enabled = true after an explicit false was saved — the operator's opt-out did not survive")
	}
}

// TestBrowserToolConfig_HasNoEvaluateEnabledField is FR-043's deletion,
// asserted structurally on the TYPE.
//
// tools.browser.evaluate_enabled had a JSON tag, an env var, and zero
// production readers — while pkg/sysagent's operator-facing blocklist named it
// as though it were the control. There must be exactly one switch, or an
// operator can spend an afternoon setting the wrong one and get no signal at
// all that they have.
func TestBrowserToolConfig_HasNoEvaluateEnabledField(t *testing.T) {
	typ := reflect.TypeOf(BrowserToolConfig{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Name == "EvaluateEnabled" {
			t.Fatalf("BrowserToolConfig still has an EvaluateEnabled field (json tag %q). It had no production reader; sandbox.browser_evaluate_enabled is the one switch. Two switches for one capability means an operator can set the wrong one and get no signal.", f.Tag.Get("json"))
		}
		if strings.Contains(f.Tag.Get("json"), "evaluate_enabled") {
			t.Fatalf("BrowserToolConfig field %s still carries the evaluate_enabled JSON tag", f.Name)
		}
		if strings.Contains(f.Tag.Get("env"), "EVALUATE_ENABLED") {
			t.Fatalf("BrowserToolConfig field %s still carries the OMNIPUS_TOOLS_BROWSER_EVALUATE_ENABLED env tag", f.Name)
		}
	}
}

// TestDocs_BrowserEvaluateDefaultIsAccurate asserts the two OPERATOR-FACING
// pages agree with the code.
//
// docs/operations/sandbox-config.md carries a worked config.json an operator
// copies wholesale; with the old `false` in it, copying the example silently
// reverted the seed. docs/tools-reference.md claimed registration is skipped
// when the flag is off — which was FALSE at HEAD independently of this change,
// and is the kind of claim that sends someone hunting for a registration bug
// that does not exist.
func TestDocs_BrowserEvaluateDefaultIsAccurate(t *testing.T) {
	root := repoRoot(t)

	sandboxDoc := readRepoFile(t, filepath.Join(root, "docs", "operations", "sandbox-config.md"))
	if strings.Contains(sandboxDoc, `"browser_evaluate_enabled": false`) {
		t.Error(`docs/operations/sandbox-config.md's worked config.json still contains "browser_evaluate_enabled": false — an operator copying that example silently reverts the seeded default`)
	}
	if !strings.Contains(sandboxDoc, "hand-edit `config.json`") {
		t.Error("docs/operations/sandbox-config.md does not tell an operator that hand-editing config.json plus a restart is the ONLY way to turn this off — neither Settings nor the sandbox-config API can express this key, so an operator who looks in the UI finds nothing and concludes the switch does not exist")
	}

	toolsDoc := readRepoFile(t, filepath.Join(root, "docs", "tools-reference.md"))
	if strings.Contains(toolsDoc, "registration is skipped when the flag is off") {
		t.Error("docs/tools-reference.md still claims registration is skipped when sandbox.browser_evaluate_enabled is off. That has never been true — registration is unconditional and the gate is at EvaluateTool.Execute.")
	}
	if !strings.Contains(toolsDoc, "Registration is NOT skipped") {
		t.Error("docs/tools-reference.md does not state that registration is unconditional. The old claim was wrong for long enough to be worth contradicting explicitly rather than merely deleting.")
	}
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

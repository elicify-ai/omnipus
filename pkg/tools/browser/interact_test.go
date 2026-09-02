// Omnipus — unit tests for the four interaction verbs (D2 Stream B).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// NO BROWSER IS NEEDED FOR ANYTHING IN THIS FILE. Everything here is
// argument validation and error wording — the half of each tool that runs
// BEFORE any CDP round trip, and therefore the half a fast unit suite can
// hold to account. The real-Chrome behaviour (a change event actually firing,
// a hover not clicking) lives in interact_e2e_test.go.

package browser

import (
	"strings"
	"testing"
)

// TestPressKey_RejectsTextLocatorByName is FR-004's press_key row.
//
// `key` is the VALUE dispatched, so `text` collides with it exactly the way
// browser_type's does. The requirement is not merely "text is ignored" — it is
// that the refusal NAMES the field, because an agent whose text locator was
// silently dropped would see the key go somewhere it did not choose and have
// nothing to read that explains why.
func TestPressKey_RejectsTextLocatorByName(t *testing.T) {
	// parseLocatorArgs with textIsLocator=false is the first half: `text` is
	// not read as a locator at all.
	loc, err := parseLocatorArgs("browser_press_key", map[string]any{"text": "Submit"}, false)
	if err != nil {
		t.Fatalf("parseLocatorArgs: %v", err)
	}
	if loc.Text != "" {
		t.Errorf("browser_press_key read `text` as a locator (got %q). `key` is the value it "+
			"dispatches; a text locator on this tool is the same collision browser_type has",
			loc.Text)
	}

	// The second half is the one that matters: an agent that supplies `text`
	// anyway must be TOLD, by name.
	_, verr := validateLocator("browser_press_key", Locator{Text: "Submit"})
	if verr == nil {
		t.Fatal("validateLocator accepted a `text` locator on browser_press_key. Silently ignoring " +
			"it sends the key to whatever happened to have focus, and the agent has nothing to read " +
			"that explains why its target was not used")
	}
	if !strings.Contains(verr.Error(), "text") {
		t.Errorf("the refusal does not name the offending field: %q", verr.Error())
	}
}

// TestPressKey_UnknownKeyErrorsAndIsNeverTyped pins the closed accepted set.
//
// The dangerous failure here is not the error — it is the ABSENCE of one. A
// fall-through that typed the unrecognised name as literal text would put
// "Banana" into whatever field had focus: a plausible-looking, silent, wrong
// action that no error surface would ever mention.
func TestPressKey_UnknownKeyErrorsAndIsNeverTyped(t *testing.T) {
	for _, spec := range []string{"Banana", "Ctrl+Banana", "a", ""} {
		keys, _, err := parseKeySpec(spec)
		if err == nil {
			t.Errorf("parseKeySpec(%q) accepted an unrecognised key and would dispatch %q", spec, keys)
			continue
		}
		if keys != "" {
			t.Errorf("parseKeySpec(%q) returned a key sequence alongside its error", spec)
		}
		// The error must list what IS accepted; "not a key" alone leaves the
		// agent to guess, and it will guess by trying text.
		for _, want := range []string{"Enter", "Escape", "browser_type"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("parseKeySpec(%q) error does not mention %q: %s", spec, want, err)
			}
		}
	}
}

// TestPressKey_AcceptsEveryNamedKeyAndModifier is the positive control. Without
// it the test above passes against a build that rejects everything.
func TestPressKey_AcceptsEveryNamedKeyAndModifier(t *testing.T) {
	for name := range namedKeys {
		if _, _, err := parseKeySpec(name); err != nil {
			t.Errorf("parseKeySpec(%q) rejected a key its own accepted set names: %v", name, err)
		}
	}
	for mod := range namedModifiers {
		spec := mod + "+Enter"
		_, mods, err := parseKeySpec(spec)
		if err != nil {
			t.Errorf("parseKeySpec(%q): %v", spec, err)
			continue
		}
		if len(mods) != 1 {
			t.Errorf("parseKeySpec(%q) produced %d modifiers, want 1", spec, len(mods))
		}
	}
	// Multiple modifiers compose.
	if _, mods, err := parseKeySpec("Ctrl+Shift+Enter"); err != nil || len(mods) != 2 {
		t.Errorf("parseKeySpec(\"Ctrl+Shift+Enter\") = mods %v, err %v; want 2 modifiers and no error",
			mods, err)
	}
}

// TestPressKey_AcceptedKeyNamesIsStable pins that the accepted-set rendering
// is sorted. It is read straight into an agent-facing error and into the tool
// Description the model sees every request; Go map order is randomised per
// run, so an unsorted render would make the same build produce a different
// prompt on every boot.
func TestPressKey_AcceptedKeyNamesIsStable(t *testing.T) {
	first := acceptedKeyNames()
	for i := 0; i < 20; i++ {
		if got := acceptedKeyNames(); got != first {
			t.Fatalf("acceptedKeyNames() is not stable across calls:\n  %q\n  %q", first, got)
		}
	}
	if !strings.Contains(first, "ArrowDown, ArrowLeft") {
		t.Errorf("acceptedKeyNames() does not look sorted: %q", first)
	}
}

// TestSelectOption_ValueAndLabelConflictNamesBothFields (FR-009).
//
// Both populated is an ErrLocatorConflict-shaped error naming BOTH fields —
// the same contract the Locator matrix applies to locators. Never a
// precedence rule: an agent that supplied both and got a result cannot tell
// which one ran.
func TestSelectOption_ValueAndLabelConflictNamesBothFields(t *testing.T) {
	tool := &SelectOptionTool{}
	res := tool.Execute(t.Context(), map[string]any{
		"selector": "#country",
		"value":    "de",
		"label":    "Germany",
	})
	if res == nil || !res.IsError {
		t.Fatal("browser_select_option accepted both `value` and `label`. With a precedence rule " +
			"the agent gets a success it cannot interpret: it does not know which of its two " +
			"instructions was applied")
	}
	for _, want := range []string{"value", "label"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Errorf("the conflict error does not name %q: %s", want, res.ForLLM)
		}
	}
	// It must fail BEFORE touching the browser. This tool has a nil resolver,
	// so any attempt to resolve a turn would produce the resolver error
	// instead — its absence is the evidence.
	if strings.Contains(res.ForLLM, "resolver") {
		t.Errorf("the conflict was detected only after the turn was resolved: %s", res.ForLLM)
	}
}

// TestSelectOption_RequiresOneOfValueOrLabel is the other half of the same
// rule. Neither supplied must be an error too, or a call with no instruction
// at all silently clears the selection.
func TestSelectOption_RequiresOneOfValueOrLabel(t *testing.T) {
	tool := &SelectOptionTool{}
	res := tool.Execute(t.Context(), map[string]any{"selector": "#country"})
	if res == nil || !res.IsError {
		t.Fatal("browser_select_option accepted a call naming no option at all")
	}
	for _, want := range []string{"label", "value"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Errorf("the error does not tell the agent which parameter to supply (%q): %s",
				want, res.ForLLM)
		}
	}

	// An EMPTY array is a caller error, not "absent". Reading it as absent
	// would fall through to the message above and misdescribe what happened.
	empty := tool.Execute(t.Context(), map[string]any{"selector": "#c", "label": []any{}})
	if empty == nil || !empty.IsError {
		t.Fatal("an empty `label` array was accepted")
	}
	if !strings.Contains(empty.ForLLM, "empty") {
		t.Errorf("an empty array is reported as if the parameter were missing: %s", empty.ForLLM)
	}
}

// TestStringOrStringArray covers the shared parameter reader, including the
// present-but-empty case the two callers both depend on.
func TestStringOrStringArray(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		want    []string
		present bool
		wantErr bool
	}{
		{"absent", map[string]any{}, nil, false, false},
		{"nil", map[string]any{"v": nil}, nil, false, false},
		{"string", map[string]any{"v": "a"}, []string{"a"}, true, false},
		{"array", map[string]any{"v": []any{"a", "b"}}, []string{"a", "b"}, true, false},
		{"empty array", map[string]any{"v": []any{}}, []string{}, true, false},
		{"wrong element type", map[string]any{"v": []any{"a", 3}}, nil, true, true},
		{"wrong type", map[string]any{"v": 7}, nil, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, present, err := stringOrStringArray(tc.args, "v")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if present != tc.present {
				t.Errorf("present = %v, want %v — 'absent' and 'supplied empty' are different "+
					"caller mistakes and must not collapse into one", present, tc.present)
			}
			if !tc.wantErr && len(got) != len(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUploadFile_RequiresPath pins that a missing path is refused before any
// filesystem or CDP work.
func TestUploadFile_RequiresPath(t *testing.T) {
	tool := &UploadFileTool{}
	res := tool.Execute(t.Context(), map[string]any{"selector": "input[type=file]"})
	if res == nil || !res.IsError {
		t.Fatal("browser_upload_file accepted a call with no `path`")
	}
	if !strings.Contains(res.ForLLM, "path") {
		t.Errorf("the error does not name the missing parameter: %s", res.ForLLM)
	}
}

// TestInteractTools_LocatorMatrixMatchesTheirSchemas is the cross-check
// between the two places a tool's accepted locators are declared: the
// per-tool matrix resolveTarget enforces, and the JSON schema the model reads.
//
// They are separate declarations, so they can disagree — and the disagreement
// is silent in the direction that matters: a schema advertising `text` on a
// tool whose matrix rejects it teaches the model to make a call that always
// fails.
func TestInteractTools_LocatorMatrixMatchesTheirSchemas(t *testing.T) {
	tools := []struct {
		name   string
		schema map[string]any
	}{
		{"browser_select_option", (&SelectOptionTool{}).Parameters()},
		{"browser_press_key", (&PressKeyTool{}).Parameters()},
		{"browser_hover", (&HoverTool{}).Parameters()},
		{"browser_upload_file", (&UploadFileTool{}).Parameters()},
	}
	for _, tc := range tools {
		acc, ok := locatorMatrix[tc.name]
		if !ok {
			t.Errorf("%s has no entry in locatorMatrix, so resolveTarget applies no per-tool rule "+
				"to it at all", tc.name)
			continue
		}
		props, _ := tc.schema["properties"].(map[string]any)
		if props == nil {
			t.Errorf("%s exposes no properties", tc.name)
			continue
		}
		_, advertisesText := props["text"]
		if advertisesText != acc.text {
			t.Errorf("%s: schema advertises `text` = %v but the locator matrix accepts it = %v. "+
				"The model reads the schema and resolveTarget enforces the matrix; a mismatch "+
				"teaches the model a call that can only fail", tc.name, advertisesText, acc.text)
		}
		if _, ok := props["role"]; !ok {
			t.Errorf("%s does not advertise `role`, but its matrix row accepts a role+name "+
				"locator — the capability exists and no model will find it", tc.name)
		}
	}
}

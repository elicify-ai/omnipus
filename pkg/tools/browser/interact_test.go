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
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
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

// ---------------------------------------------------------------------------
// Order 24a — the human-control gate, which IS the write-lease membership
// ---------------------------------------------------------------------------

// TestActionTools_DeferWhenHumanControls_Table drives all four new verbs
// through Execute while a human viewer holds control.
//
// WHY THIS IS NOT JUST ANOTHER DEFERRAL TEST. Under D1 §14 rule 3 a tool takes
// the write lease IFF it is controlledResult-gated. So this table is the only
// local evidence that these four are IN the lease at all — drop the
// controlledResult call from one of them and the lease silently loses it, with
// the failure surfacing in the OTHER document's test and no explanation here.
//
// The oracle is the NON-ERROR deferral shape, not merely "it did not act": an
// agent that receives IsError on a deferral treats a temporary condition as a
// permanent one and gives up.
//
// It uses the unreachable-CDP config, so no Chrome is required: any tool that
// failed to defer would fall through to a connection error instead, which is a
// visibly different result rather than a hang.
func TestActionTools_DeferWhenHumanControls_Table(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	ctx := context.Background()

	if !mgr.Live().TakeControl(testSessionID, "human-viewer") {
		t.Fatal("test setup: taking control must succeed on an uncontrolled session")
	}

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"browser_select_option", map[string]any{"selector": "#country", "label": "Germany"}},
		{"browser_press_key", map[string]any{"key": "Enter"}},
		{"browser_hover", map[string]any{"selector": "#menu"}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result := mustGetTool(t, registry, tc.tool).Execute(ctx, tc.args)
			if result == nil {
				t.Fatalf("%s returned no result", tc.tool)
			}
			if result.IsError {
				t.Fatalf("%s reported an ERROR while deferring. A deferral is a temporary, "+
					"non-error condition — an agent that reads it as a failure gives up instead of "+
					"retrying after the human releases control: %s", tc.tool, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "human is currently controlling") {
				t.Errorf("%s did not defer. Under D1 §14 rule 3 that also removes it from the "+
					"write lease, and THAT failure surfaces only in the other document's test: %s",
					tc.tool, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, tc.tool) {
				t.Errorf("%s's deferral message does not name the deferred tool: %s",
					tc.tool, result.ForLLM)
			}
		})
	}

	// browser_upload_file is NOT in the registry (FR-029), so it is exercised
	// directly. Its gate must be wired NOW rather than at registration time,
	// or the day #659 closes it ships ungated and nobody re-checks.
	t.Run("browser_upload_file", func(t *testing.T) {
		tool := &UploadFileTool{res: newFixedResolver(mgr), agentHome: t.TempDir(), restrict: true}
		result := tool.Execute(ctx, map[string]any{"selector": "input", "path": "x.txt"})
		if result == nil || result.IsError {
			t.Fatalf("browser_upload_file did not produce a non-error deferral: %+v", result)
		}
		if !strings.Contains(result.ForLLM, "human is currently controlling") {
			t.Errorf("browser_upload_file is not control-gated: %s", result.ForLLM)
		}
	})
}

// TestSnapshot_DefersButAnExemptToolDoesNot is the counterpart the table above
// needs to not be vacuous — a table asserting only "the action tools defer"
// would pass on a build where EVERY browser tool defers.
//
// RE-POINTED BY ADR-085 D5 (was TestSnapshot_NotDeferredByViewer; the old name
// is left here so a grep for it lands on this explanation). It asserted the
// opposite of what it asserts now, on the strength of the capability spec's
// FR-038 ("browser_snapshot is read-only: no controlledResult, no write
// lease"). ADR-085 D5 retires the first half of that requirement by name:
//
//	"The read-only capture tools — browser_screenshot, browser_get_text,
//	 browser_snapshot — are deliberately ungated today ('they don't inject
//	 input'). Under D1 the agent keeps running, so it can screenshot the
//	 operator's login form while they type into it. D1 makes this worse, so D5
//	 is required, not optional."
//
// The handover spec's FR-033 states the consequence ("browser_screenshot,
// browser_get_text and browser_snapshot MUST consult the control gate and
// return the same non-error deferral while the wheel is held"), and FR-035
// moves them into the new CAPTURE class: gated, but still never write-leased
// and never per-call audited. The SECOND half of the old FR-038 — no write
// lease — therefore survives untouched, and is still asserted by
// snapshot_test.go::TestSnapshot_IsNotWriteClass.
//
// This wave's write-set (ADR-085 wave W1) named this file, but only
// tools_control_test.go was actually updated, which is why the retired
// expectation shipped red.
//
// Both halves are asserted rather than only the new one, so the test still
// discriminates: browser_snapshot (capture class) MUST defer, and
// browser_wait (exempt class) MUST NOT — it reaches the unreachable-CDP
// endpoint and fails there instead, which is a visibly different result.
func TestSnapshot_DefersButAnExemptToolDoesNot(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	if !mgr.Live().TakeControl(testSessionID, "human-viewer") {
		t.Fatal("test setup: taking control must succeed")
	}

	tool := &SnapshotTool{res: newFixedResolver(mgr)}
	result := tool.Execute(context.Background(), map[string]any{})
	if result == nil {
		t.Fatal("browser_snapshot returned no result")
	}
	if result.IsError {
		t.Errorf("browser_snapshot reported an ERROR while deferring. A deferral is a temporary, "+
			"non-error condition — an agent that reads it as a failure gives up instead of "+
			"resuming once the operator sends the next prompt: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "human is currently controlling") {
		t.Errorf("browser_snapshot did NOT defer to a human viewer. ADR-085 D5/FR-033 moved it out "+
			"of the exempt roster and into the capture class precisely because, under D1's "+
			"'the agent keeps running' turn model, an ungated snapshot describes whatever the "+
			"operator is mid-typing into: %s", result.ForLLM)
	}
	if result.Deferred == nil || result.Deferred.Gate != "browser_control" {
		t.Errorf("browser_snapshot must carry the STRUCTURAL browser_control deferral the turn "+
			"engine's ledger reads, not only the prose: %+v", result.Deferred)
	}

	// The discriminator: an EXEMPT-class tool under the same held wheel must
	// still reach the browser and fail on the unreachable endpoint. Without
	// this half, the assertions above would pass on a build where the gate
	// swallowed every browser tool.
	waitResult := mustGetTool(t, registry, "browser_wait").
		Execute(context.Background(), map[string]any{"selector": "#anything"})
	if waitResult == nil {
		t.Fatal("browser_wait returned no result")
	}
	if strings.Contains(waitResult.ForLLM, "human is currently controlling") {
		t.Errorf("browser_wait DEFERRED to a human viewer. FR-034 keeps it ungated, and if every "+
			"browser tool defers then the assertions above prove nothing about the gate "+
			"discriminating at all: %s", waitResult.ForLLM)
	}
	if !waitResult.IsError {
		t.Errorf("browser_wait must attempt real execution and fail on the unreachable CDP "+
			"endpoint; a non-error result means it was short-circuited somewhere: %s",
			waitResult.ForLLM)
	}
}

// ---------------------------------------------------------------------------
// FR-029 — held means UNREGISTERED, not unseeded
// ---------------------------------------------------------------------------

// TestUploadFile_NotRegistered is FR-029's only gate.
//
// It lives in this file rather than scope_test.go because it was silently lost
// once already when two writers overwrote that file in the same worktree, and
// register.go's own doc comment cites it by name — a comment pointing at a
// test that does not exist is worse than no comment, because it reads as
// evidence.
//
// It is deliberately a GREEN test, not a red one, and it needs BOTH halves:
// asserting only the absence would pass on a build where the tool was never
// written, and asserting only the presence of the others would pass on a build
// where upload is fully callable.
//
// There is no guard here on #659's issue state. A network call to GitHub in a
// unit suite is not acceptable, and a local constant would be this comment
// with extra steps. When #659 closes, the RegisterReplacing line lands and
// this test is DELETED in the same commit.
func TestUploadFile_NotRegistered(t *testing.T) {
	registry := tools.NewToolRegistry()
	mgr := &BrowserManager{}
	if err := RegisterTools(registry, newFixedResolver(mgr), true, t.TempDir(), true); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	if _, ok := registry.Get("browser_upload_file"); ok {
		t.Error("browser_upload_file IS registered. FR-029 holds it out of the registry until " +
			"issue #659 (AutoDenyAsk not inherited by delegated sub-turns) is closed: its seeded " +
			"policy is `ask`, so an unattended delegated turn that reaches it raises an approval " +
			"nobody can answer. Registering it early is not a small win — it is the exact hang " +
			"#659 describes, on the one browser verb that hands the operator's files outward.")
	}

	// The seeded-but-unregistered half. Absent from BOTH the registry and the
	// catalog would be a different (and also wrong) state: the catalog-drift
	// test compares BrowserBuiltinMetadata against coreagent's
	// allStaticToolNames, and the name must be in both.
	var inCatalog bool
	for _, tool := range BrowserBuiltinMetadata() {
		if tool.Name() == "browser_upload_file" {
			inCatalog = true
		}
	}
	if !inCatalog {
		t.Error("browser_upload_file is absent from BrowserBuiltinMetadata. Held means " +
			"UNREGISTERED, not unseeded — the name must stay in the catalog or the drift test " +
			"against allStaticToolNames fails and the policy seed describes a tool nothing knows about")
	}

	// The positive control, in the same test, so the absence above cannot be
	// read as evidence that registration is broken for everything.
	for _, name := range []string{
		"browser_select_option", "browser_press_key", "browser_hover", "browser_snapshot",
	} {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("%q is NOT registered, yet it is seeded and catalogued. Every agent's policy "+
				"then resolves allow for a tool the registry cannot produce", name)
		}
	}
}

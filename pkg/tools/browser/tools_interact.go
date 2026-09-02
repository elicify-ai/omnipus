// Omnipus — the four interaction verbs (ADR-072 D2, capability spec Stream B).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// browser_select_option, browser_press_key, browser_hover and
// browser_upload_file.
//
// THE CALL ORDER IS FIXED AND IT IS NOT A STYLE CHOICE (spec §3, D1 §14.2
// rule 1). Every Execute here runs:
//
//	resolveTurn -> controlledResult -> leaseWrite -> recordBrowserAction
//	  -> mgr.Session -> context.WithTimeout -> display -> resolveTarget
//	  (+ defer cleanup) -> waitActionable -> the act
//
//   - controlledResult (FR-040) is what puts these four in D1's write lease.
//     D1's membership rule is a biconditional — a tool takes the lease IFF it
//     is controlledResult-gated — so DELETING one of these four calls silently
//     removes that tool from the lease and the failure surfaces in the OTHER
//     document's test, with no local explanation.
//   - m.mu is NEVER held across resolveTarget or waitActionable: both issue CDP
//     round trips, and the ADR-038 rule the manager already documents on
//     installTargetListenerLocked applies unchanged.
//   - browser_press_key with NO locator skips waitActionable, and ONLY
//     waitActionable. It still calls controlledResult and still takes the
//     lease. That skip is the single sanctioned bypass of the actionability
//     gate in the whole design (§12 A-10): there is no element to gate on.
//
// browser_upload_file is implemented here but is NOT registered — see FR-029
// and register.go. Held means unregistered, not unseeded.

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Shared locator plumbing for the four verbs
// ---------------------------------------------------------------------------

// locatorParamSchema is the locator half of the JSON schema, shared by the
// three verbs that accept all three locator kinds. A single definition so the
// wording an agent reads cannot drift between tools that behave identically.
func locatorParamSchema(includeText bool, textDesc string) map[string]any {
	props := map[string]any{
		"selector": map[string]any{
			"type":        "string",
			"description": "CSS selector of the element, optionally ending in :has-text(\"...\") / :text-is(\"...\") to match by visible text",
		},
		"role": map[string]any{
			"type":        "string",
			"description": "ARIA/computed role of the element, e.g. \"button\", \"combobox\", \"link\". Use with `name`. Survives generated CSS class names.",
		},
		"name": map[string]any{
			"type":        "string",
			"description": "Computed accessible name of the element, e.g. \"Country\". Use with `role`.",
		},
		"index": map[string]any{
			"type":        "integer",
			"description": "0-based disambiguator when role+name matches more than one element. Omit to require a unique match. Not applicable to a CSS/text locator.",
		},
	}
	if includeText {
		props["text"] = map[string]any{"type": "string", "description": textDesc}
	}
	return props
}

// deferredIsNotAnError is the shared tail of every action tool's Description:
// the {"deferred": true} shape is a NON-error result and an agent that treats
// it as a failure retries the wrong thing.
const deferredIsNotAnError = " If a human is currently controlling the browser via the live view, this call " +
	"defers instead of acting — the result is {\"deferred\": true, \"reason\": ...}, which is not an " +
	"error; wait for them to release control and retry."

// ---------------------------------------------------------------------------
// browser_select_option (FR-009)
// ---------------------------------------------------------------------------

type SelectOptionTool struct {
	tools.BaseTool
	browserAudit
	res ManagerResolver
}

func (t *SelectOptionTool) Name() string                 { return "browser_select_option" }
func (t *SelectOptionTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *SelectOptionTool) Category() tools.ToolCategory { return tools.CategoryBrowser }

func (t *SelectOptionTool) Description() string {
	return "Choose one or more options in a <select> dropdown and fire a real change event, so " +
		"framework listeners (React, Vue, Angular) see the choice — setting the value alone fires " +
		"nothing and they do not. Name the option by `label` (its visible text, which is what you " +
		"read on the page) OR by `value` (its value attribute); supply exactly one of the two, never " +
		"both. For a multi-select, pass an array to either one — if any entry in the array does not " +
		"match an option the call fails naming the unmatched entries and changes NOTHING, so the form " +
		"is never left half-applied. " + roleNameLocatorHelp + "INTERIM: this tool acts on the workspace browser, which is shared " +
		"with the operator and carries their live logins." + deferredIsNotAnError
}

func (t *SelectOptionTool) Parameters() map[string]any {
	props := locatorParamSchema(true, "Match the <select> by its visible text (case-insensitive substring), instead of — or scoped within — selector")
	props["value"] = map[string]any{
		"description": "Option value attribute to select. A string, or an array of strings for a multi-select. Supply `value` OR `label`, not both.",
		"type":        []string{"string", "array"},
		"items":       map[string]any{"type": "string"},
	}
	props["label"] = map[string]any{
		"description": "Option visible text to select. A string, or an array of strings for a multi-select. Supply `label` OR `value`, not both.",
		"type":        []string{"string", "array"},
		"items":       map[string]any{"type": "string"},
	}
	return map[string]any{"type": "object", "properties": props}
}

// stringOrStringArray reads a parameter that may be a single string or an
// array of strings. present distinguishes "absent" from "supplied empty" — the
// second is a caller error and must not be read as the first.
func stringOrStringArray(args map[string]any, key string) (vals []string, present bool, err error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, false, nil
	}
	switch v := raw.(type) {
	case string:
		return []string{v}, true, nil
	case []string:
		return v, true, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, true, fmt.Errorf("`%s[%d]` must be a string, got %T", key, i, item)
			}
			out = append(out, s)
		}
		return out, true, nil
	default:
		return nil, true, fmt.Errorf("`%s` must be a string or an array of strings, got %T", key, raw)
	}
}

// selectOutcome is what the in-page script reports back. Every failure mode is
// a NAMED code rather than a bare false, because the three of them need three
// different agent-facing messages and a boolean cannot carry that.
type selectOutcome struct {
	// Code is "", "not_a_select", "zero_options" or "unmatched".
	Code string `json:"code"`
	// Unmatched carries the requested entries that matched no option. Only
	// meaningful for Code == "unmatched".
	Unmatched []string `json:"unmatched"`
	// Selected is the visible text of every option now selected.
	Selected []string `json:"selected"`
	// Multiple reports whether the element is a multi-select.
	Multiple bool `json:"multiple"`
	// Tag is the element's tag name, for the not_a_select message.
	Tag string `json:"tag"`
}

// selectOptionScript resolves the requested entries and applies them
// ALL-OR-NOTHING. The all-or-nothing rule lives inside the page, in one
// evaluation, deliberately: splitting "check" and "apply" into two round trips
// would leave a window in which the page mutates between them and a partial
// application becomes possible again.
//
// It dispatches `input` then `change`, both bubbling, which is the pair
// framework listeners bind to. Assigning `.selected` alone fires neither.
const selectOptionScript = `(function(sel, by, wanted) {
  const el = document.querySelector(sel);
  if (!el) { return {code: "not_a_select", tag: "", unmatched: [], selected: [], multiple: false}; }
  if (el.tagName !== "SELECT") {
    return {code: "not_a_select", tag: el.tagName.toLowerCase(), unmatched: [], selected: [], multiple: false};
  }
  const opts = Array.from(el.options);
  if (opts.length === 0) {
    return {code: "zero_options", tag: "select", unmatched: [], selected: [], multiple: el.multiple};
  }
  const norm = (s) => (s == null ? "" : String(s)).trim();
  const chosen = [];
  const unmatched = [];
  for (const w of wanted) {
    const hit = opts.find((o) => (by === "value" ? norm(o.value) === norm(w) : norm(o.textContent) === norm(w)));
    if (hit) { chosen.push(hit); } else { unmatched.push(w); }
  }
  if (unmatched.length > 0) {
    return {code: "unmatched", tag: "select", unmatched: unmatched, selected: [], multiple: el.multiple};
  }
  if (!el.multiple && chosen.length > 1) {
    return {code: "unmatched", tag: "select", unmatched: wanted.slice(1), selected: [], multiple: false};
  }
  for (const o of opts) { o.selected = false; }
  for (const o of chosen) { o.selected = true; }
  el.dispatchEvent(new Event("input", {bubbles: true}));
  el.dispatchEvent(new Event("change", {bubbles: true}));
  return {
    code: "",
    tag: "select",
    unmatched: [],
    selected: Array.from(el.selectedOptions).map((o) => norm(o.textContent)),
    multiple: el.multiple,
  };
})(%s, %s, %s)`

func (t *SelectOptionTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	values, hasValue, verr := stringOrStringArray(args, "value")
	if verr != nil {
		return tools.ErrorResult(t.Name() + ": " + verr.Error())
	}
	labels, hasLabel, lerr := stringOrStringArray(args, "label")
	if lerr != nil {
		return tools.ErrorResult(t.Name() + ": " + lerr.Error())
	}
	switch {
	case hasValue && hasLabel:
		// The same contract the Locator matrix applies to locators: name the
		// offending fields, never pick a winner.
		return tools.ErrorResult((&ErrLocatorConflict{
			Fields: []string{"value", "label"},
			Tool:   t.Name(),
			Reason: "an option is named EITHER by its value attribute OR by its visible label",
		}).Error())
	case !hasValue && !hasLabel:
		return tools.ErrorResult(t.Name() + ": supply `label` (the option's visible text) or `value` (its value attribute)")
	}
	by, wanted := "label", labels
	if hasValue {
		by, wanted = "value", values
	}
	if len(wanted) == 0 {
		return tools.ErrorResult(fmt.Sprintf("%s: `%s` was supplied but empty; name at least one option", t.Name(), by))
	}

	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	if result := controlledResult(mgr, key, owner, t.Name()); result != nil {
		return result
	}
	deferred, release := leaseWrite(ctx, mgr, key, owner, tools.ToolAgentID(ctx), t.Name())
	if deferred != nil {
		return deferred
	}
	defer release()
	t.recordBrowserAction(ctx, key, owner, t.Name(), hostOfActiveTab(mgr, sid))

	sessionCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: %s", t.Name(), err))
	}
	tabCtx, cancelTimeout := context.WithTimeout(sessionCtx, mgr.PageTimeout())
	defer cancelTimeout()

	loc, lperr := parseLocatorArgs(t.Name(), args, true)
	if lperr != nil {
		return tools.ErrorResult(lperr.Error())
	}
	display := displayLocator(loc)
	target, cleanup, rerr := resolveTarget(tabCtx, t.Name(), loc, mgr.PageTimeout())
	defer cleanup()
	if rerr != nil {
		return tools.ErrorResult(rerr.Error())
	}
	// The gate runs BEFORE the zero-option check, deliberately: a <select>
	// that is still rendering, disabled or covered is a different problem from
	// a <select> that genuinely has nothing to choose, and reporting the
	// second while the first is true would send the agent after the wrong
	// thing. Recorded because the spec leaves the ordering open.
	if aerr := waitActionable(tabCtx, t.Name(), target, display, mgr.PageTimeout()); aerr != nil {
		return tools.ErrorResult(aerr.Error())
	}

	script := fmt.Sprintf(selectOptionScript, jsString(target), jsString(by), jsStrings(wanted))
	var out selectOutcome
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(script, &out)); err != nil {
		// FR-037: a failure AFTER the gate must not surface as a bare
		// "context deadline exceeded". translatePostGateErr names `visible`
		// when that is what was lost, and passes anything else through.
		return tools.ErrorResult(fmt.Sprintf("%s: element %q could not be read: %s", t.Name(), display,
			postGateMessage(err, t.Name(), target, display)))
	}

	switch out.Code {
	case "not_a_select":
		tag := out.Tag
		if tag == "" {
			tag = "nothing"
		}
		return tools.ErrorResult(fmt.Sprintf(
			"%s: %q is a <%s>, not a <select>. This tool drives dropdowns; use browser_click for a "+
				"custom listbox built from divs.", t.Name(), display, tag))
	case "zero_options":
		return tools.ErrorResult(fmt.Sprintf(
			"%s: the <select> %q has no <option> elements, so there is nothing to choose. It is "+
				"probably still being populated — wait for an option to appear (browser_wait) and "+
				"retry.", t.Name(), display))
	case "unmatched":
		return tools.ErrorResult(fmt.Sprintf(
			"%s: no option matches %s %s in %q. NOTHING was changed — the selection is exactly as it "+
				"was before this call. Read the available options with browser_snapshot or "+
				"browser_get_text and retry with an exact match.",
			t.Name(), by, quoteAll(out.Unmatched), display))
	}

	return jsonResult(map[string]any{
		"success":  true,
		"selector": display,
		"selected": out.Selected,
		"multiple": out.Multiple,
	})
}

// ---------------------------------------------------------------------------
// browser_press_key (FR-010)
// ---------------------------------------------------------------------------

type PressKeyTool struct {
	tools.BaseTool
	browserAudit
	res ManagerResolver
}

func (t *PressKeyTool) Name() string                 { return "browser_press_key" }
func (t *PressKeyTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *PressKeyTool) Category() tools.ToolCategory { return tools.CategoryBrowser }

// namedKeys is the CLOSED accepted set. A name outside it is an ERROR listing
// the set — it is NEVER typed as literal text, which is what a naive
// fall-through would do: `browser_press_key{key:"Banana"}` typing "Banana"
// into a form is a silent, plausible-looking wrong action.
//
// Use browser_type to enter text; this tool sends discrete keys.
var namedKeys = map[string]string{
	"Enter":      kb.Enter,
	"Tab":        kb.Tab,
	"Escape":     kb.Escape,
	"Backspace":  kb.Backspace,
	"Delete":     kb.Delete,
	"ArrowUp":    kb.ArrowUp,
	"ArrowDown":  kb.ArrowDown,
	"ArrowLeft":  kb.ArrowLeft,
	"ArrowRight": kb.ArrowRight,
	"Home":       kb.Home,
	"End":        kb.End,
	"PageUp":     kb.PageUp,
	"PageDown":   kb.PageDown,
}

// namedModifiers is the accepted modifier set, with the aliases an agent will
// plausibly reach for. Meta/Cmd/Command all map to ModifierCommand, which is
// Chrome's own name for the platform meta key.
var namedModifiers = map[string]input.Modifier{
	"Ctrl":    input.ModifierCtrl,
	"Control": input.ModifierCtrl,
	"Alt":     input.ModifierAlt,
	"Option":  input.ModifierAlt,
	"Shift":   input.ModifierShift,
	"Meta":    input.ModifierCommand,
	"Cmd":     input.ModifierCommand,
	"Command": input.ModifierCommand,
}

// acceptedKeyNames renders the accepted set in a stable order for the error
// message. Sorted, not map order: an error whose wording changes between runs
// is one an agent cannot learn from and a test cannot pin.
func acceptedKeyNames() string {
	names := make([]string, 0, len(namedKeys))
	for k := range namedKeys {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// parseKeySpec splits "Ctrl+Shift+Enter" into its modifiers and its base key.
//
// THE WIRE FORMAT IS ONE COMBINED STRING, and this is a decision the spec
// leaves open (§3 shows a bare accepted set and a "Ctrl+Banana" dataset row,
// implying a prefix, but never states it). One string is chosen over a
// separate `modifiers` array because two ways to express the same thing means
// two validation paths and a well-defined "both supplied" case nobody has
// specified. The base key is still validated against the closed set, so
// "Ctrl+Banana" fails exactly like "Banana".
func parseKeySpec(spec string) (keys string, mods []input.Modifier, err error) {
	parts := strings.Split(spec, "+")
	base := strings.TrimSpace(parts[len(parts)-1])
	for _, raw := range parts[:len(parts)-1] {
		name := strings.TrimSpace(raw)
		mod, ok := namedModifiers[name]
		if !ok {
			return "", nil, fmt.Errorf(
				"%q is not a modifier. Accepted modifiers: Alt, Cmd, Command, Control, Ctrl, Meta, Option, Shift",
				name)
		}
		mods = append(mods, mod)
	}
	seq, ok := namedKeys[base]
	if !ok {
		return "", nil, fmt.Errorf(
			"%q is not a key this tool sends. Accepted keys: %s. To enter text, use browser_type — "+
				"an unrecognised name is never typed as literal characters",
			base, acceptedKeyNames())
	}
	return seq, mods, nil
}

func (t *PressKeyTool) Description() string {
	return "Send one discrete keystroke — " + acceptedKeyNames() + " — optionally with modifiers " +
		"written as a prefix, e.g. \"Ctrl+Enter\" or \"Shift+Tab\". This is for keys, NOT for text: " +
		"use browser_type to enter characters. A name outside the accepted set is rejected and is " +
		"never typed as literal text. Supply `selector` or `role`+`name` to send the key to a " +
		"specific element (it must be actionable first); with NO locator the key goes to whatever " +
		"holds focus, or to the document when nothing does — in that case the result reports " +
		"focused_element: null, which is why an Enter that you expected to submit a form did nothing. " +
		"INTERIM: this tool acts on the workspace browser, which is shared with the operator and " +
		"carries their live logins." + deferredIsNotAnError
}

func (t *PressKeyTool) Parameters() map[string]any {
	// text is DELIBERATELY absent: `key` is the value dispatched, and a `text`
	// locator collides with it exactly the way browser_type's does (FR-004).
	// resolveTarget rejects it by name if an agent sends it anyway.
	props := locatorParamSchema(false, "")
	props["key"] = map[string]any{
		"type":        "string",
		"description": "The key to send: " + acceptedKeyNames() + ". Prefix modifiers with +, e.g. \"Ctrl+Enter\".",
	}
	return map[string]any{"type": "object", "properties": props, "required": []string{"key"}}
}

// focusedElementScript reports a short description of the focused element, or
// null when focus is on the body (i.e. nothing is really focused).
const focusedElementScript = `(function() {
  const el = document.activeElement;
  if (!el || el === document.body || el === document.documentElement) { return null; }
  const id = el.id ? "#" + el.id : "";
  const name = el.getAttribute("name") ? "[name=" + el.getAttribute("name") + "]" : "";
  return el.tagName.toLowerCase() + id + name;
})()`

func (t *PressKeyTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	spec, _ := args["key"].(string)
	if strings.TrimSpace(spec) == "" {
		return tools.ErrorResult(t.Name() + ": 'key' parameter is required")
	}
	keys, mods, kerr := parseKeySpec(spec)
	if kerr != nil {
		return tools.ErrorResult(t.Name() + ": " + kerr.Error())
	}

	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	if result := controlledResult(mgr, key, owner, t.Name()); result != nil {
		return result
	}
	// The no-locator call takes the lease exactly like the located one. Only
	// the actionability gate is skipped (A-10) — a keystroke to the document
	// is still a page mutation and still contends with another writer.
	deferred, release := leaseWrite(ctx, mgr, key, owner, tools.ToolAgentID(ctx), t.Name())
	if deferred != nil {
		return deferred
	}
	defer release()
	t.recordBrowserAction(ctx, key, owner, t.Name(), hostOfActiveTab(mgr, sid))

	sessionCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: %s", t.Name(), err))
	}
	tabCtx, cancelTimeout := context.WithTimeout(sessionCtx, mgr.PageTimeout())
	defer cancelTimeout()

	// textIsLocator=false: `key` is the value dispatched, so a `text`
	// argument is NOT read as a locator here (FR-004). resolveTarget rejects
	// it BY NAME if an agent sends one anyway.
	loc, lperr := parseLocatorArgs(t.Name(), args, false)
	if lperr != nil {
		return tools.ErrorResult(lperr.Error())
	}
	located := !loc.empty()

	var opts []chromedp.KeyOption
	if len(mods) > 0 {
		opts = append(opts, chromedp.KeyModifiers(mods...))
	}

	display := "the focused element"
	if located {
		display = displayLocator(loc)
		target, cleanup, rerr := resolveTarget(tabCtx, t.Name(), loc, mgr.PageTimeout())
		defer cleanup()
		if rerr != nil {
			return tools.ErrorResult(rerr.Error())
		}
		if aerr := waitActionable(tabCtx, t.Name(), target, display, mgr.PageTimeout()); aerr != nil {
			return tools.ErrorResult(aerr.Error())
		}
		var nodes []*cdp.Node
		if err := chromedp.Run(tabCtx, chromedp.Nodes(target, &nodes, chromedp.ByQuery)); err != nil || len(nodes) == 0 {
			return tools.ErrorResult(fmt.Sprintf("%s: element %q vanished between the actionability check and the keystroke",
				t.Name(), display))
		}
		if err := chromedp.Run(tabCtx, chromedp.KeyEventNode(nodes[0], keys, opts...)); err != nil {
			return tools.ErrorResult(fmt.Sprintf("%s: sending %s to %q failed: %s", t.Name(), spec, display,
				postGateMessage(err, t.Name(), target, display)))
		}
	} else if err := chromedp.Run(tabCtx, chromedp.KeyEvent(keys, opts...)); err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: sending %s failed: %s", t.Name(), spec, err))
	}

	// Read focus AFTER the key. An agent whose Enter did nothing needs to know
	// where the key actually went, and the answer is the post-dispatch state:
	// a Tab moves focus, and reporting the pre-dispatch element would describe
	// a page that no longer exists.
	var focused *string
	if err := chromedp.Run(tabCtx, chromedp.Evaluate(focusedElementScript, &focused)); err != nil {
		// Non-fatal: the key was sent. Reporting a hard failure here would
		// make the agent retry a keystroke that already landed.
		focused = nil
	}

	result := map[string]any{"success": true, "key": spec, "target": display}
	if focused == nil {
		// Explicit null, not an omitted field (FR-010/AC3). "absent" and
		// "nothing was focused" must not look the same to the model.
		result["focused_element"] = nil
	} else {
		result["focused_element"] = *focused
	}
	return jsonResult(result)
}

// ---------------------------------------------------------------------------
// browser_hover (FR-011)
// ---------------------------------------------------------------------------

type HoverTool struct {
	tools.BaseTool
	browserAudit
	res ManagerResolver
}

func (t *HoverTool) Name() string                 { return "browser_hover" }
func (t *HoverTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *HoverTool) Category() tools.ToolCategory { return tools.CategoryBrowser }

func (t *HoverTool) Description() string {
	return "Move the mouse pointer over an element without clicking it — the way to open a hover menu, " +
		"reveal a tooltip, or trigger a mouseover-only control before acting on what appears. It " +
		"scrolls the element into view and moves the pointer to the centre of its box. It NEVER " +
		"clicks: if you want the click, call browser_click. The pointer stays where this call left " +
		"it, so a hover menu remains open for the next call; move it elsewhere by hovering something " +
		"else. " + roleNameLocatorHelp + "INTERIM: this tool acts on the workspace browser, which is shared with the operator " +
		"and carries their live logins." + deferredIsNotAnError
}

func (t *HoverTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": locatorParamSchema(true,
			"Match the element by its visible text (case-insensitive substring), instead of — or scoped within — selector"),
	}
}

func (t *HoverTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	if result := controlledResult(mgr, key, owner, t.Name()); result != nil {
		return result
	}
	deferred, release := leaseWrite(ctx, mgr, key, owner, tools.ToolAgentID(ctx), t.Name())
	if deferred != nil {
		return deferred
	}
	defer release()
	t.recordBrowserAction(ctx, key, owner, t.Name(), hostOfActiveTab(mgr, sid))

	sessionCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: %s", t.Name(), err))
	}
	tabCtx, cancelTimeout := context.WithTimeout(sessionCtx, mgr.PageTimeout())
	defer cancelTimeout()

	loc, lperr := parseLocatorArgs(t.Name(), args, true)
	if lperr != nil {
		return tools.ErrorResult(lperr.Error())
	}
	display := displayLocator(loc)
	target, cleanup, rerr := resolveTarget(tabCtx, t.Name(), loc, mgr.PageTimeout())
	defer cleanup()
	if rerr != nil {
		return tools.ErrorResult(rerr.Error())
	}
	if aerr := waitActionable(tabCtx, t.Name(), target, display, mgr.PageTimeout()); aerr != nil {
		return tools.ErrorResult(aerr.Error())
	}

	var cx, cy float64
	err = chromedp.Run(tabCtx,
		chromedp.ScrollIntoView(target, chromedp.ByQuery),
		chromedp.ActionFunc(func(c context.Context) error {
			var nodes []*cdp.Node
			if nerr := chromedp.Nodes(target, &nodes, chromedp.ByQuery).Do(c); nerr != nil {
				return nerr
			}
			if len(nodes) == 0 {
				return fmt.Errorf("element vanished after the actionability check")
			}
			box, berr := dom.GetBoxModel().WithNodeID(nodes[0].NodeID).Do(c)
			if berr != nil {
				return berr
			}
			// Content quad is x1,y1,x2,y2,x3,y3,x4,y4. The centre of the box
			// is the average of the two opposite corners — correct for a
			// rotated element too, where the axis-aligned midpoint is not.
			if len(box.Content) < 8 {
				return fmt.Errorf("element has no content box")
			}
			cx = (box.Content[0] + box.Content[4]) / 2
			cy = (box.Content[1] + box.Content[5]) / 2
			// mouseMoved ONLY. No mousePressed, no mouseReleased: a hover that
			// clicks is indistinguishable from a click the agent did not ask
			// for, and on a delete button that is unrecoverable.
			return input.DispatchMouseEvent(input.MouseMoved, cx, cy).Do(c)
		}),
	)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: could not hover %q: %s", t.Name(), display,
			postGateMessage(err, t.Name(), target, display)))
	}

	return jsonResult(map[string]any{
		"success":  true,
		"selector": display,
		"x":        cx,
		"y":        cy,
	})
}

// ---------------------------------------------------------------------------
// browser_upload_file (FR-012, FR-029, FR-031)
// ---------------------------------------------------------------------------

// UploadFileTool attaches host files to a page's <input type="file">.
//
// IT IS DELIBERATELY NOT REGISTERED (FR-029). Its name IS seeded — in
// allStaticToolNames, in the global sandbox.tool_policies ceiling, in every
// browser-capable agent's override map, and in BrowserBuiltinMetadata — while
// its RegisterReplacing line waits on issue #659. Held means unregistered, not
// unseeded: a seeded-but-unregistered name is inert, whereas a
// registered-but-unseeded tool resolves a SILENT deny on every agent.
//
// It must NOT copy EvaluateTool's executeEnabled shape (registered, then
// refused inside Execute). An operator reading the catalog would see a tool
// that is present and then discover it refuses; absent is honest.
type UploadFileTool struct {
	tools.BaseTool
	browserAudit
	res ManagerResolver
	// agentHome and restrict are threaded through exactly as ScreenshotTool's
	// are, and for the same reason: they are what ResolveTurnFSPolicy needs to
	// build the turn's filesystem policy.
	agentHome string
	restrict  bool
}

func (t *UploadFileTool) Name() string                 { return "browser_upload_file" }
func (t *UploadFileTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *UploadFileTool) Category() tools.ToolCategory { return tools.CategoryBrowser }

func (t *UploadFileTool) Description() string {
	return "Attach a file from your working directory to a file-upload input on the page. Give the " +
		"path relative to your working directory (or an absolute path inside an approved mount); a " +
		"path outside those is refused. Pass an array to attach several files to one input — if any " +
		"one of them is refused, NONE is attached. This reports that the file was ATTACHED to the " +
		"input, not that the site accepted it: submit the form and read the page to find that out. " +
		"INTERIM: this tool acts on the workspace browser, which is shared with the operator and " +
		"carries their live logins." + deferredIsNotAnError
}

func (t *UploadFileTool) Parameters() map[string]any {
	props := locatorParamSchema(true,
		"Match the file input (or its label) by visible text, instead of — or scoped within — selector")
	props["path"] = map[string]any{
		"description": "Path of the file to attach, relative to your working directory. A string, or an array of strings to attach several files to one input.",
		"type":        []string{"string", "array"},
		"items":       map[string]any{"type": "string"},
	}
	return map[string]any{"type": "object", "properties": props, "required": []string{"path"}}
}

func (t *UploadFileTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	paths, present, perr := stringOrStringArray(args, "path")
	if perr != nil {
		return tools.ErrorResult(t.Name() + ": " + perr.Error())
	}
	if !present || len(paths) == 0 {
		return tools.ErrorResult(t.Name() + ": 'path' parameter is required")
	}

	mgr, key, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	if result := controlledResult(mgr, key, owner, t.Name()); result != nil {
		return result
	}
	deferred, release := leaseWrite(ctx, mgr, key, owner, tools.ToolAgentID(ctx), t.Name())
	if deferred != nil {
		return deferred
	}
	defer release()

	pageOrigin := hostOfActiveTab(mgr, sid)
	t.recordBrowserAction(ctx, key, owner, t.Name(), pageOrigin)

	// THE SINGLE CHOKEPOINT (FR-012). ResolvePath with FSOpWrite, whose own
	// rule is "the work dir or an explicit mount only" — which is exactly the
	// set of paths it is safe to hand to a DIFFERENT process that sits outside
	// this one's confinement.
	//
	// There is deliberately NO second AllowedRoots comparison here. A
	// hand-rolled path check outside the chokepoint would be a second
	// string-comparison gate and therefore a SECOND TOCTOU window, stacked on
	// the one RealPath() already forces. One rule, one implementation.
	policy, err := tools.ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: failed to resolve filesystem policy: %s", t.Name(), err))
	}
	realPaths := make([]string, 0, len(paths))
	for _, raw := range paths {
		handle, herr := tools.ResolvePath(ctx, policy, t.Name(), "", tools.FSOpWrite, raw)
		if herr != nil {
			// All-or-nothing, the same rule FR-009 draws for a partial
			// multi-select: a call that attached two of three files leaves the
			// form in a state nobody asked for and the agent cannot tell which
			// landed. Nothing has been attached at this point.
			t.recordUploadDecision(ctx, key, owner, raw, pageOrigin, audit.DecisionDeny, "path_refused", herr.Error())
			return tools.PermissionDeniedResult(t.Name(), herr,
				fmt.Sprintf("%s: %s. Nothing was attached.", t.Name(), herr.Error()))
		}
		real, rerr := handle.RealPath()
		closeErr := handle.Close()
		if rerr != nil {
			return tools.ErrorResult(fmt.Sprintf("%s: %s", t.Name(), rerr))
		}
		_ = closeErr
		// The tool Stats the RESOLVED path itself. SetUploadFiles hands the
		// path to Chrome and Chrome opens it, so a missing path or a directory
		// surfaces as an opaque CDP failure — or, worse, as a success with an
		// empty attachment. Refusing here is the difference between "that file
		// is not there" and "the upload silently did nothing".
		info, serr := os.Stat(real)
		switch {
		case serr != nil:
			t.recordUploadDecision(ctx, key, owner, real, pageOrigin, audit.DecisionDeny, "not_found", serr.Error())
			return tools.ErrorResult(fmt.Sprintf("%s: %q does not exist (%s). Nothing was attached.",
				t.Name(), raw, serr))
		case info.IsDir():
			t.recordUploadDecision(ctx, key, owner, real, pageOrigin, audit.DecisionDeny, "is_a_directory", "")
			return tools.ErrorResult(fmt.Sprintf("%s: %q is a directory, not a file. Nothing was attached.",
				t.Name(), raw))
		}
		realPaths = append(realPaths, real)
	}

	sessionCtx, err := mgr.Session(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: %s", t.Name(), err))
	}
	tabCtx, cancelTimeout := context.WithTimeout(sessionCtx, mgr.PageTimeout())
	defer cancelTimeout()

	loc, lperr := parseLocatorArgs(t.Name(), args, true)
	if lperr != nil {
		return tools.ErrorResult(lperr.Error())
	}
	display := displayLocator(loc)
	target, cleanup, rerr := resolveTarget(tabCtx, t.Name(), loc, mgr.PageTimeout())
	defer cleanup()
	if rerr != nil {
		return tools.ErrorResult(rerr.Error())
	}
	if aerr := waitActionable(tabCtx, t.Name(), target, display, mgr.PageTimeout()); aerr != nil {
		return tools.ErrorResult(aerr.Error())
	}

	if err := chromedp.Run(tabCtx, chromedp.SetUploadFiles(target, realPaths, chromedp.ByQuery)); err != nil {
		for _, p := range realPaths {
			t.recordUploadDecision(ctx, key, owner, p, pageOrigin, audit.DecisionDeny, "attach_failed", err.Error())
		}
		return tools.ErrorResult(fmt.Sprintf("%s: could not attach to %q: %s", t.Name(), display,
			postGateMessage(err, t.Name(), target, display)))
	}
	for _, p := range realPaths {
		t.recordUploadDecision(ctx, key, owner, p, pageOrigin, audit.DecisionAllow, "", "")
	}

	return jsonResult(map[string]any{
		"success":  true,
		"selector": display,
		"files":    realPaths,
		"note": "attached to the input — the page has not necessarily accepted them. Submit the " +
			"form and read the result to find that out.",
	})
}

// recordUploadDecision writes FR-031's per-invocation audit event. EVERY
// invocation, allowed or denied — a trail that records only the successes
// cannot answer "did this agent try to exfiltrate anything?", which is the
// question the event exists for.
//
// The event name is the UNDERSCORE form. A dotted name would fail the
// AuditEntry contract's name pattern, and the operator's whole Audit Log view
// blanks on a schema-invalid row rather than skipping it.
func (t *UploadFileTool) recordUploadDecision(
	ctx context.Context,
	key BrowsingKey, owner TabOwner,
	path, pageOrigin string,
	decision string,
	reason, detail string,
) {
	log := t.auditLogger()
	if log == nil {
		return
	}
	details := map[string]any{
		"workspace_id":  key.WorkspaceID(),
		"browsing_key":  key.String(),
		"tab_owner":     owner.String(),
		"resolved_path": path,
		"page_origin":   pageOrigin,
		"fs_op":         "write",
		"fs_op_reason": "the path is handed to Chrome, a process outside this one's confinement, " +
			"so the set of paths it is safe to hand out is exactly the set FSOpWrite already bounds",
	}
	if reason != "" {
		details["reason"] = reason
	}
	if detail != "" {
		details["detail"] = detail
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventBrowserUploadFile,
		Decision:  decision,
		AgentID:   tools.ToolAgentID(ctx),
		SessionID: tools.ToolTranscriptSessionID(ctx),
		Tool:      "browser_upload_file",
		Details:   details,
	}
	if err := log.Log(entry); err != nil {
		slog.Error("browser audit: upload log write failed",
			"error", err, "tool", "browser_upload_file", "workspace_id", key.WorkspaceID())
	}
}

// ---------------------------------------------------------------------------
// small shared helpers
// ---------------------------------------------------------------------------

// postGateMessage renders a failure that happened AFTER the actionability gate
// returned, for the four interaction verbs.
//
// FR-037: such a failure must never surface as a bare "context deadline
// exceeded". The gate's guarantee holds across its two probes, not at the
// moment of dispatch, so an element can pass and then stop being visible a
// frame later — and the agent needs to be told `visible`, not handed a
// timeout it cannot act on.
//
// translatePostGateErr returns (translated, ok); when ok is false the error
// is not a lost-visibility one and is passed through, scrubbed of the internal
// data-omnipus-tsel marker so the agent sees the locator it actually wrote.
func postGateMessage(err error, toolName, target, display string) string {
	if translated, ok := translatePostGateErr(err, toolName, display); ok {
		return translated.Error()
	}
	return scrubMarkerFromError(err, target, display).Error()
}

// jsString renders a Go string as a JavaScript literal safe to embed in an
// evaluated script. Never fmt.Sprintf("%q") — Go's quoting is not JavaScript's
// and differs on exactly the inputs (lone surrogates, \x escapes) an attacker
// would reach for.
func jsString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// jsStrings renders a []string as a JavaScript array literal.
func jsStrings(v []string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// quoteAll renders a list for an error message: "Alpha", "Beta".
func quoteAll(v []string) string {
	out := make([]string, 0, len(v))
	for _, s := range v {
		out = append(out, fmt.Sprintf("%q", s))
	}
	return strings.Join(out, ", ")
}

// Compile-time interface checks — the same guard the other browser tools carry.
var (
	_ tools.Tool = (*SelectOptionTool)(nil)
	_ tools.Tool = (*PressKeyTool)(nil)
	_ tools.Tool = (*HoverTool)(nil)
	_ tools.Tool = (*UploadFileTool)(nil)
)

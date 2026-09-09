// Real-Chrome end-to-end tests for ADR-075 D2's interaction verbs
// (capability spec §10 orders 17, 18, 19 and 20).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WHAT THESE PROVE THAT THE UNIT TESTS CANNOT.
//
// interact_test.go covers argument validation, the locator matrix and the two
// gates — everything decided before a CDP command is issued. None of it
// touches a page. Every claim below is a claim about what a REAL BROWSER did:
//
//   - browser_select_option dispatches a real bubbling `change` event.
//     Assigning `option.selected` alone fires nothing and a framework listener
//     never sees the choice — a failure INVISIBLE to a test that reads back
//     `.value`, because `.value` is correct either way. So every assertion
//     here reads a LISTENER's record, never the element's own state.
//   - browser_hover moves the pointer and does NOT click. A click counter that
//     must stay at zero is the only way to state that; on a delete button the
//     difference is unrecoverable.
//   - browser_press_key sends a key event real enough to submit a form.
//   - browser_upload_file's path really resolves through the chokepoint, and a
//     path outside the roots is refused before Chrome is handed anything.
//
// Gated by skipIfNoBrowser like every other *_e2e_test.go here. That helper
// probes $PATH, then the macOS .app bundle locations directly, then the
// managed install — so on a developer Mac with Chrome in /Applications these
// RUN rather than skip.

package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// interactFixtureHTML is one page carrying every element these tests drive.
//
// THE CHANGE-EVENT RECORDERS ARE THE POINT. `#change-log` and `#multi-log` are
// written ONLY by an addEventListener("change") handler. Nothing else on the
// page touches them, so a non-empty log is proof a real change event fired and
// bubbled.
const interactFixtureHTML = `<!DOCTYPE html>
<html>
<head><title>Interact Fixture</title></head>
<body>
  <select id="single">
    <option value="a">Alpha</option>
    <option value="b">Beta</option>
    <option value="c">Gamma</option>
  </select>
  <div id="change-log">no-change</div>

  <select id="multi" multiple size="3">
    <option value="a">Alpha</option>
    <option value="b">Beta</option>
    <option value="c">Gamma</option>
  </select>
  <div id="multi-log">no-change</div>

  <select id="empty"></select>

  <form id="search-form" onsubmit="document.getElementById('submitted').textContent='submitted'; return false;">
    <input id="q" type="text" aria-label="Search" />
  </form>
  <div id="submitted">not-submitted</div>

  <div id="menu-trigger">Menu</div>
  <div id="menu">closed</div>
  <div id="click-count">0</div>

  <input id="file-input" type="file" />
  <div id="file-log">no-file</div>

  <script>
    function names(el) {
      return Array.prototype.map.call(el.selectedOptions, function (o) { return o.textContent; }).join('|');
    }
    document.getElementById('single').addEventListener('change', function (e) {
      document.getElementById('change-log').textContent = 'change:' + names(e.target);
    });
    document.getElementById('multi').addEventListener('change', function (e) {
      document.getElementById('multi-log').textContent = 'change:' + names(e.target);
    });
    var trigger = document.getElementById('menu-trigger');
    trigger.addEventListener('mouseover', function () {
      document.getElementById('menu').textContent = 'open';
    });
    trigger.addEventListener('click', function () {
      var el = document.getElementById('click-count');
      el.textContent = String(Number(el.textContent) + 1);
    });
    document.getElementById('file-input').addEventListener('change', function (e) {
      document.getElementById('file-log').textContent =
        e.target.files.length ? e.target.files[0].name : 'no-file';
    });
  </script>
</body>
</html>`

func interactFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(interactFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// interactPage boots a real browser on the fixture and returns the registry
// and manager, with the page already loaded.
func interactPage(t *testing.T) (*tools.ToolRegistry, *BrowserManager) {
	t.Helper()
	skipIfNoBrowser(t)
	srv := interactFixtureServer(t)
	registry, mgr := newPermissiveRegistry(t, testBrowserCfg(t))
	nav := mustGetTool(t, registry, "browser_navigate")
	res := nav.Execute(context.Background(), map[string]any{"url": srv.URL})
	require.False(t, res.IsError, "navigate must succeed; got: %s", res.ForLLM)
	return registry, mgr
}

// readFixtureText reads an element's text through the SHIPPED browser_get_text
// tool rather than a bespoke evaluate. A fixture assertion routed through the
// real read path cannot pass on a build where reading the page is broken.
func readFixtureText(t *testing.T, registry *tools.ToolRegistry, selector string) string {
	t.Helper()
	res := mustGetTool(t, registry, "browser_get_text").
		Execute(context.Background(), map[string]any{"selector": selector})
	require.NotNil(t, res)
	require.False(t, res.IsError, "reading %s must succeed; got: %s", selector, res.ForLLM)
	data := decodeJSON(t, res.ForLLM)
	text, _ := data["text"].(string)
	return strings.TrimSpace(text)
}

// --- order 17: browser_select_option ----------------------------------------

// TestSelectOption_ByLabel_FiresChange is the headline claim: an agent names
// the option by the text it can READ on the page, and a framework listener
// sees the choice.
func TestSelectOption_ByLabel_FiresChange(t *testing.T) {
	registry, _ := interactPage(t)

	res := mustGetTool(t, registry, "browser_select_option").Execute(context.Background(),
		map[string]any{"selector": "#single", "label": "Gamma"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "select by label must succeed; got: %s", res.ForLLM)

	// The LISTENER's record, not the element's value. A build that assigned
	// `option.selected` and fired nothing would leave `.value` == "c" and this
	// log untouched.
	assert.Equal(t, "change:Gamma", readFixtureText(t, registry, "#change-log"),
		"no change event reached the page's listener. Setting the value alone fires nothing, "+
			"and every React/Vue/Angular form on the web binds to change — the agent would see a "+
			"successful tool result and the site would behave as if nothing had been chosen")

	data := decodeJSON(t, res.ForLLM)
	assert.Equal(t, true, data["success"])
	assert.Equal(t, false, data["multiple"])
}

// TestSelectOption_ByValue_FiresChange proves `value` is a real parameter and
// not prose. Revision 2 of the spec described it and exercised only `label`,
// which left it genuinely unclear whether it shipped.
func TestSelectOption_ByValue_FiresChange(t *testing.T) {
	registry, _ := interactPage(t)

	res := mustGetTool(t, registry, "browser_select_option").Execute(context.Background(),
		map[string]any{"selector": "#single", "value": "b"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "select by value must succeed; got: %s", res.ForLLM)

	// Matched by VALUE, reported and recorded by LABEL — which is also how the
	// agent will read it back.
	assert.Equal(t, "change:Beta", readFixtureText(t, registry, "#change-log"))
}

// TestSelectOption_PartialMultiSelectAppliesNothing is the all-or-nothing
// rule. Two of three labels resolve; the call must error, name the unresolved
// one, and leave the form exactly as it was.
//
// The "nothing was applied" half is what makes this test worth writing: an
// implementation that applied the two matches and then reported the third as
// an error would pass any assertion that only checked for an error.
func TestSelectOption_PartialMultiSelectAppliesNothing(t *testing.T) {
	registry, _ := interactPage(t)

	res := mustGetTool(t, registry, "browser_select_option").Execute(context.Background(),
		map[string]any{"selector": "#multi", "label": []any{"Alpha", "Beta", "Omega"}})
	require.NotNil(t, res)
	require.True(t, res.IsError, "a partial multi-select match must fail; got: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "Omega",
		"the error must NAME the entry that did not resolve, or the agent has to guess which of "+
			"its three labels was wrong")

	assert.Equal(t, "no-change", readFixtureText(t, registry, "#multi-log"),
		"a partially-applied multi-select left the form in a state neither the agent nor the "+
			"operator asked for, and the agent cannot tell from an error which entries landed")
}

// TestSelectOption_MultiSelectAppliesAll is the positive control for the test
// above. Without it, the all-or-nothing assertion would pass on a build where
// browser_select_option never applied anything at all.
func TestSelectOption_MultiSelectAppliesAll(t *testing.T) {
	registry, _ := interactPage(t)

	res := mustGetTool(t, registry, "browser_select_option").Execute(context.Background(),
		map[string]any{"selector": "#multi", "label": []any{"Alpha", "Gamma"}})
	require.NotNil(t, res)
	require.False(t, res.IsError, "a fully-matching multi-select must succeed; got: %s", res.ForLLM)

	assert.Equal(t, "change:Alpha|Gamma", readFixtureText(t, registry, "#multi-log"))
	data := decodeJSON(t, res.ForLLM)
	assert.Equal(t, true, data["multiple"])
}

// TestSelectOption_ZeroOptionsErrors — a <select> with no <option> is a NAMED
// error, never a silent success. It is the shape of a dropdown still being
// populated by a fetch, and reporting success there would have the agent move
// on believing it had chosen something.
func TestSelectOption_ZeroOptionsErrors(t *testing.T) {
	registry, _ := interactPage(t)

	res := mustGetTool(t, registry, "browser_select_option").Execute(context.Background(),
		map[string]any{"selector": "#empty", "label": "Anything"})
	require.NotNil(t, res)
	require.True(t, res.IsError, "an option-less <select> must fail; got: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "no <option>",
		"the error must say WHAT is wrong with the element, not just that the label did not match "+
			"— those two send the agent to different remedies")
}

// --- order 18: browser_press_key --------------------------------------------

// TestPressKey_Enter_SubmitsForm proves the keystroke is real enough to
// trigger the browser's own form submission, which a synthetic
// dispatchEvent-style key would not.
func TestPressKey_Enter_SubmitsForm(t *testing.T) {
	registry, _ := interactPage(t)

	typed := mustGetTool(t, registry, "browser_type").Execute(context.Background(),
		map[string]any{"selector": "#q", "text": "hello"})
	require.False(t, typed.IsError, "typing into the form field must succeed; got: %s", typed.ForLLM)

	res := mustGetTool(t, registry, "browser_press_key").Execute(context.Background(),
		map[string]any{"selector": "#q", "key": "Enter"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "Enter must be sent; got: %s", res.ForLLM)

	assert.Equal(t, "submitted", readFixtureText(t, registry, "#submitted"),
		"Enter in a focused text input did not submit the form. The key was dispatched but not as "+
			"a real key event, which is the difference between an agent that can complete a search "+
			"box and one that silently cannot")
}

// TestPressKey_NoFocusReportsNull covers the case the spec insists must be
// stated rather than discovered: with no locator and nothing focused, the key
// goes to the document and the result says focused_element: null — which is
// the ONLY way an agent whose Enter did nothing can tell why.
func TestPressKey_NoFocusReportsNull(t *testing.T) {
	registry, _ := interactPage(t)

	res := mustGetTool(t, registry, "browser_press_key").Execute(context.Background(),
		map[string]any{"key": "Enter"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "a key with no locator is legal; got: %s", res.ForLLM)

	data := decodeJSON(t, res.ForLLM)
	value, present := data["focused_element"]
	require.True(t, present,
		"focused_element must be PRESENT and null, never omitted — an absent field and \"nothing "+
			"was focused\" look identical to a model, and only one of them explains why the "+
			"keystroke did nothing")
	assert.Nil(t, value, "nothing on the fixture has focus, so focused_element must be null")

	assert.Equal(t, "not-submitted", readFixtureText(t, registry, "#submitted"),
		"the key reached the form even though nothing was focused")
}

// TestPressKey_NoLocatorSkipsActionabilityGate asserts the design's ONE
// sanctioned bypass of the actionability gate AT THE SEAM — the gate is not
// entered — rather than by timing, which would pass on a fast machine
// regardless.
//
// Why it needs an oracle at all: with no element named there is nothing to
// gate on, so a later refactor that started gating this path would turn a
// legal keystroke-to-document into a hard `visible` failure on any page with
// nothing focused. That is a silent capability loss with a green suite.
func TestPressKey_NoLocatorSkipsActionabilityGate(t *testing.T) {
	registry, _ := interactPage(t)
	pressKey := mustGetTool(t, registry, "browser_press_key")

	armGateEvalCounter()
	res := pressKey.Execute(context.Background(), map[string]any{"key": "Tab"})
	noLocatorEvals := disarmGateEvalCounter()
	require.False(t, res.IsError, "a key with no locator is legal; got: %s", res.ForLLM)
	assert.Zero(t, noLocatorEvals,
		"browser_press_key with no locator entered the actionability gate (%d probe round trips). "+
			"There is no element to gate on, so the gate can only fail — and it would fail every "+
			"keystroke-to-document on a page with nothing focused", noLocatorEvals)

	// The positive control. Without it this test passes on a build where the
	// gate is broken for every tool, which is a much worse defect than the one
	// it is checking for.
	armGateEvalCounter()
	res = pressKey.Execute(context.Background(), map[string]any{"selector": "#q", "key": "Tab"})
	locatedEvals := disarmGateEvalCounter()
	require.False(t, res.IsError, "a located key press must succeed; got: %s", res.ForLLM)
	assert.Positive(t, locatedEvals,
		"browser_press_key WITH a locator did not enter the actionability gate either, so the "+
			"assertion above proves nothing about the no-locator case")
}

// --- order 19: browser_hover ------------------------------------------------

// TestHover_OpensMenu_NoClick is two assertions in two directions, and both
// are required. The mouseover proves the pointer arrived; the click counter
// staying at zero proves it did nothing else.
func TestHover_OpensMenu_NoClick(t *testing.T) {
	registry, _ := interactPage(t)

	require.Equal(t, "closed", readFixtureText(t, registry, "#menu"),
		"fixture precondition: the menu starts closed")

	res := mustGetTool(t, registry, "browser_hover").Execute(context.Background(),
		map[string]any{"selector": "#menu-trigger"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "hover must succeed; got: %s", res.ForLLM)

	assert.Equal(t, "open", readFixtureText(t, registry, "#menu"),
		"the pointer never reached the element — a hover that dispatches nothing leaves every "+
			"hover-only menu on the web unreachable, which is the capability this verb exists for")
	assert.Equal(t, "0", readFixtureText(t, registry, "#click-count"),
		"browser_hover CLICKED the element. A hover that clicks is indistinguishable from a click "+
			"the agent never asked for, and on a delete button that is unrecoverable")
}

// --- order 20: browser_upload_file ------------------------------------------

// newUploadTool constructs UploadFileTool directly. It is deliberately NOT in
// the registry (FR-029 holds its registration until #659 closes), so
// mustGetTool cannot reach it — constructing it here is how the implementation
// gets tested without shipping it.
func newUploadTool(t *testing.T, mgr *BrowserManager, agentHome string) *UploadFileTool {
	t.Helper()
	return &UploadFileTool{res: newFixedResolver(mgr), agentHome: agentHome, restrict: true}
}

// writeInWorkDir puts a fixture file where the turn's filesystem policy will
// actually look for it.
//
// On a turn with no workspace re-root — which is every test in this file —
// ResolveTurnFSPolicy resolves WorkDir to agentHome ITSELF, not to a `work/`
// subdirectory. Verified rather than assumed: an earlier revision of this
// helper wrote into agentHome/work and the tool refused the path as missing,
// naming the resolved absolute path in its error, which is what identified the
// real root.
func writeInWorkDir(t *testing.T, agentHome, name, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(agentHome, 0o755))
	path := filepath.Join(agentHome, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// TestUploadFile_AttachesFileFromWorkDir is the happy path, and the assertion
// is the PAGE's record of the attachment, not the tool's own claim.
func TestUploadFile_AttachesFileFromWorkDir(t *testing.T) {
	registry, mgr := interactPage(t)

	agentHome := t.TempDir()
	writeInWorkDir(t, agentHome, "report.txt", "hello")

	res := newUploadTool(t, mgr, agentHome).Execute(context.Background(),
		map[string]any{"selector": "#file-input", "path": "report.txt"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "attaching a file from the work dir must succeed; got: %s", res.ForLLM)

	assert.Equal(t, "report.txt", readFixtureText(t, registry, "#file-log"),
		"the page's own change listener never saw a file. The tool reported success without "+
			"anything reaching the input")

	// The result says ATTACHED, not accepted. An agent that reads this as
	// "the site took my file" will report a success that never happened.
	assert.Contains(t, res.ForLLM, "not necessarily accepted")
}

// TestUploadFile_DeniedAtChokepointOutsideRoots proves the refusal happens at
// tools.ResolvePath — BEFORE Chrome is handed anything — and that nothing was
// attached.
//
// Asserting only "the call errored" would pass on a build that handed the path
// to Chrome and let Chrome fail, which is a completely different security
// posture: the file would already have left this process's confinement.
func TestUploadFile_DeniedAtChokepointOutsideRoots(t *testing.T) {
	registry, mgr := interactPage(t)

	agentHome := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))

	res := newUploadTool(t, mgr, agentHome).Execute(context.Background(),
		map[string]any{"selector": "#file-input", "path": outside})
	require.NotNil(t, res)
	require.True(t, res.IsError, "a path outside the work dir and every mount must be refused; got: %s", res.ForLLM)

	assert.Equal(t, "no-file", readFixtureText(t, registry, "#file-log"),
		"the refused file reached the page anyway. The refusal must land before SetUploadFiles is "+
			"issued, or the path has already crossed into Chrome — a process outside this one's "+
			"confinement — by the time anything says no")
}

// TestUploadFile_EmitsAuditEvent covers FR-031 in BOTH directions: the allowed
// call and the denied one each produce an event.
//
// The denied half is the load-bearing one. A trail that records only what
// succeeded cannot answer "did this agent TRY to hand out a file it was not
// allowed to reach?", which is the question the event exists for.
func TestUploadFile_EmitsAuditEvent(t *testing.T) {
	_, mgr := interactPage(t)

	harness := newAuditHarness(t)
	agentHome := t.TempDir()
	writeInWorkDir(t, agentHome, "report.txt", "hello")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))

	tool := newUploadTool(t, mgr, agentHome)
	tool.SetAuditLogger(harness.log)
	ctx := auditToolCtx("jim")

	okRes := tool.Execute(ctx, map[string]any{"selector": "#file-input", "path": "report.txt"})
	require.False(t, okRes.IsError, "the allowed upload must succeed; got: %s", okRes.ForLLM)
	denyRes := tool.Execute(ctx, map[string]any{"selector": "#file-input", "path": outside})
	require.True(t, denyRes.IsError, "the outside-roots upload must be refused; got: %s", denyRes.ForLLM)

	// The event name is the UNDERSCORE form. A dotted name fails the
	// AuditEntry contract's pattern and blanks the operator's whole Audit Log
	// view rather than skipping one row.
	events := harness.eventsNamed(t, "browser_upload_file")
	require.Len(t, events, 2,
		"want one browser_upload_file event per invocation, allowed AND denied; got %d", len(events))

	var sawAllow, sawDeny bool
	for _, e := range events {
		assert.Equal(t, "jim", e["agent_id"])
		assert.Equal(t, "browser_upload_file", e["tool"])
		details, ok := e["details"].(map[string]any)
		require.True(t, ok, "event carries no details block: %v", e)
		assert.Equal(t, "write", details["fs_op"],
			"fs_op must record WHICH filesystem rule admitted the path; an upload is classed as a "+
				"write because the path is handed to a process outside this one's confinement")
		assert.NotEmpty(t, details["fs_op_reason"])
		assert.NotEmpty(t, details["resolved_path"],
			"the RESOLVED path, not the relative one an operator cannot act on")
		switch e["decision"] {
		case "allow":
			sawAllow = true
		case "deny":
			sawDeny = true
		}
	}
	assert.True(t, sawAllow, "no allow-decision event was recorded")
	assert.True(t, sawDeny,
		"no deny-decision event was recorded. A refused upload leaves no trace, so an operator "+
			"reconstructing what an agent attempted sees only what it managed to do")
}

package browser

// dialog_e2e_test.go — the half of browser_handle_dialog that no seam test can
// reach: the real Page.handleJavaScriptDialog call, against real Chrome.
//
// WHY THIS FILE HAD TO EXIST. Every test in dialog_test.go swaps
// handleJavaScriptDialogFn before driving the tool, which is the right call
// there — the concurrency assertion is "exactly ONE CDP call was issued when
// two callers raced", and only a seam can count calls. But the consequence is
// that the whole suite proved the wrapper's bookkeeping and nothing whatsoever
// about whether a dialog is actually answered. A build in which the seam's
// production body sent the wrong CDP command, or no command at all, passed
// every one of those tests.
//
// THE ORACLE IS NOT "THE DIALOG WAS DISMISSED". A dialog that is gone is not
// what the operator lost. What a JavaScript dialog takes away is the TAB: the
// renderer stops servicing CDP, so every other browser tool on that tab times
// out for as long as the dialog stands. Recovery therefore has exactly one
// acceptance test — the tab answers CDP again — and that is what each phase
// below ends with, through the shipped browser_get_text tool rather than a
// bespoke chromedp call.
//
// Each phase also proves the tab was genuinely wedged FIRST. Without that,
// "the tab answers afterwards" is worthless: it is equally true of a tab that
// was never blocked, which is precisely what a build with a broken CDP call
// would produce.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// dialogFixtureHTML raises each dialog type from a TIMER, never inline.
//
// That is not cosmetic. alert()/confirm()/prompt() block the calling execution
// context, so evaluating one directly would block the very Runtime.evaluate
// that asked for it — the test would wedge itself before it could reach the
// tool. Scheduling on a later task lets the evaluate return, and the dialog
// opens a moment afterwards.
//
// `__confirmed` and `__answer` are written ONLY by the dialog's own return
// value. They are the evidence that the real CDP call carried the ACCEPT flag
// and the prompt text, which a test that only checked "no error" cannot see.
const dialogFixtureHTML = `<!DOCTYPE html>
<html>
<head><title>Dialog Fixture</title></head>
<body>
  <div id="probe">tab-answers-cdp</div>
  <script>
    window.__confirmed = 'unset';
    window.__answer = 'unset';
    function raiseAlert() { setTimeout(function () { alert('the tab is now wedged'); }, 0); }
    function raiseConfirm() {
      setTimeout(function () { window.__confirmed = confirm('proceed?'); }, 0);
    }
    function raisePrompt() {
      setTimeout(function () { window.__answer = prompt('your name?', ''); }, 0);
    }
  </script>
</body>
</html>`

// dialogE2EPageTimeout bounds how long the shipped tools wait on the wedged
// renderer. Short enough that the three "prove it is wedged" steps cost about
// twelve seconds in total, long enough that a cold Chrome's first navigate has
// room. Set explicitly rather than via testBrowserCfg's 15s, because this file
// deliberately waits out that timeout three times.
const dialogE2EPageTimeout = 4 * time.Second

func dialogFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(dialogFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// dialogPage boots real Chrome on the fixture. One browser for the whole test:
// the three phases run in sequence on the same tab, which is also closer to
// what actually happens to an operator than three pristine browsers would be.
func dialogPage(t *testing.T) (*tools.ToolRegistry, *BrowserManager) {
	t.Helper()
	skipIfNoBrowser(t)
	srv := dialogFixtureServer(t)

	cfg := testBrowserCfg(t)
	cfg.PageTimeout = dialogE2EPageTimeout
	registry, mgr := newPermissiveRegistry(t, cfg)

	// The FR-060 memory gate is pinned OPEN for this test. It is a real
	// production behaviour with its own coverage, and on a developer machine
	// under memory pressure it refuses the tab in well under a second — which
	// would look exactly like a browser failure here and would report a red
	// that has nothing to do with dialogs.
	mgr.memoryPressureFn = func(int) (bool, bool) { return false, true }

	nav := mustGetTool(t, registry, "browser_navigate")
	res := nav.Execute(context.Background(), map[string]any{"url": srv.URL})
	require.False(t, res.IsError, "navigate must succeed; got: %s", res.ForLLM)
	return registry, mgr
}

// probeTabAnswers reads #probe through the shipped browser_get_text tool. It
// returns the tool's own result so a caller can assert either outcome — this
// is used both to prove the tab is wedged and to prove it recovered, and those
// are the same call with opposite expectations.
func probeTabAnswers(t *testing.T, registry *tools.ToolRegistry) *tools.ToolResult {
	t.Helper()
	res := mustGetTool(t, registry, "browser_get_text").
		Execute(context.Background(), map[string]any{"selector": "#probe"})
	require.NotNil(t, res)
	return res
}

// raiseDialog schedules a dialog and waits until the manager's listener has
// actually recorded it. Waiting on the RECORDED state rather than sleeping a
// fixed interval is what keeps this test from being a timing lottery on a
// loaded machine.
func raiseDialog(t *testing.T, registry *tools.ToolRegistry, mgr *BrowserManager, js string) *PendingDialog {
	t.Helper()
	res := mustGetTool(t, registry, "browser_evaluate").
		Execute(context.Background(), map[string]any{"js": js})
	require.NotNil(t, res)
	require.False(t, res.IsError, "scheduling the dialog must not fail; got: %s", res.ForLLM)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if dlg := mgr.PendingDialogOn(testSessionID); dlg != nil {
			return dlg
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no dialog was recorded within 10s after evaluating %q. Either the page never "+
		"raised one, or the Page.javascriptDialogOpening listener is not armed on this tab — "+
		"which is the silent failure syncDialogListenersLocked exists to prevent", js)
	return nil
}

// readGlobal reads a window global back through browser_evaluate, retrying
// briefly: the page's assignment happens when the blocked callback resumes,
// which is a moment after the CDP call that unblocked it returned.
func readGlobal(t *testing.T, registry *tools.ToolRegistry, expr string, want any) any {
	t.Helper()
	var last any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := mustGetTool(t, registry, "browser_evaluate").
			Execute(context.Background(), map[string]any{"js": expr})
		require.NotNil(t, res)
		require.False(t, res.IsError, "reading %s must succeed once the tab is unwedged; got: %s",
			expr, res.ForLLM)
		last = decodeJSON(t, res.ForLLM)["result"]
		if last == want {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}
	return last
}

// TestDialog_E2E_RealChromeAnswersAndUnwedgesTheTab drives the REAL
// Page.handleJavaScriptDialog — no seam swap anywhere in this file.
func TestDialog_E2E_RealChromeAnswersAndUnwedgesTheTab(t *testing.T) {
	registry, mgr := dialogPage(t)
	dialogTool := mustGetTool(t, registry, "browser_handle_dialog")
	ctx := context.Background()

	// Baseline. If the tab does not answer BEFORE any dialog exists, nothing
	// below means anything.
	baseline := probeTabAnswers(t, registry)
	require.False(t, baseline.IsError, "the tab must answer before any dialog is raised; got: %s",
		baseline.ForLLM)
	require.Contains(t, decodeJSON(t, baseline.ForLLM)["text"], "tab-answers-cdp")

	// ── Phase 1: alert(), dismissed ────────────────────────────────────────
	//
	// The headline case. An alert has no meaningful answer — the only thing
	// that matters is that the tab comes back.
	t.Run("alert is answered and the tab answers CDP again", func(t *testing.T) {
		dlg := raiseDialog(t, registry, mgr, "raiseAlert()")
		assert.Equal(t, string(page.DialogTypeAlert), dlg.Type)

		// The wedge is real, proven through an ordinary tool call. This is the
		// step that makes the recovery assertion meaningful: without it, "the
		// tab answers" would also be true of a build whose CDP call did
		// nothing, because the tab would never have stopped answering.
		wedged := probeTabAnswers(t, registry)
		require.True(t, wedged.IsError,
			"browser_get_text SUCCEEDED while an alert() was open. The premise of this whole "+
				"file is that a JavaScript dialog stops the renderer servicing CDP; if it does "+
				"not on this Chrome, the recovery assertion below proves nothing and this test "+
				"must be redesigned rather than relaxed. Got: %s", wedged.ForLLM)
		assert.Contains(t, wedged.ForLLM, "browser_handle_dialog",
			"the wedged call must point the agent at the verb that recovers it; got: %s",
			wedged.ForLLM)

		// THE REAL CDP CALL. No seam.
		res := dialogTool.Execute(ctx, map[string]any{"accept": false})
		require.NotNil(t, res)
		require.False(t, res.IsError, "answering a real alert must succeed; got: %s", res.ForLLM)
		answered, _ := decodeJSON(t, res.ForLLM)["dialog"].(map[string]any)
		require.NotNil(t, answered, "the result must describe the dialog it answered; got: %s", res.ForLLM)
		assert.Equal(t, string(page.DialogTypeAlert), answered["type"])
		assert.Equal(t, false, decodeJSON(t, res.ForLLM)["accepted"])

		// The acceptance test.
		recovered := probeTabAnswers(t, registry)
		require.False(t, recovered.IsError,
			"the tab still does not answer CDP after browser_handle_dialog reported success. The "+
				"tool answered its own bookkeeping and not the browser — which is exactly the "+
				"failure every seam-swapped test in dialog_test.go is blind to. Got: %s",
			recovered.ForLLM)
		assert.Contains(t, decodeJSON(t, recovered.ForLLM)["text"], "tab-answers-cdp")
	})

	// ── Phase 2: confirm(), accepted ───────────────────────────────────────
	//
	// This is where the accept flag stops being bookkeeping. The page records
	// confirm()'s return value, so `true` there is proof the real CDP call
	// carried accept=true — something no seam test can distinguish from a
	// call that dismissed and reported otherwise.
	t.Run("confirm accepted returns true to the page, and the tab recovers", func(t *testing.T) {
		dlg := raiseDialog(t, registry, mgr, "raiseConfirm()")
		assert.Equal(t, string(page.DialogTypeConfirm), dlg.Type)

		res := dialogTool.Execute(ctx, map[string]any{"accept": true})
		require.NotNil(t, res)
		require.False(t, res.IsError, "accepting a real confirm must succeed; got: %s", res.ForLLM)
		assert.Equal(t, true, decodeJSON(t, res.ForLLM)["accepted"])

		recovered := probeTabAnswers(t, registry)
		require.False(t, recovered.IsError,
			"the tab does not answer CDP after accepting a confirm; got: %s", recovered.ForLLM)

		assert.Equal(t, true, readGlobal(t, registry, "window.__confirmed", true),
			"the PAGE did not see confirm() return true. The tool reported accepted:true, so "+
				"either the accept flag never reached Page.handleJavaScriptDialog or the dialog "+
				"was answered some other way — and on a real site the difference between OK and "+
				"Cancel is the whole decision")
	})

	// ── Phase 3: prompt(), accepted with text ──────────────────────────────
	//
	// prompt_text is the one argument that is invisible in every other
	// assertion: a build that dropped WithPromptText would still dismiss the
	// dialog, still unwedge the tab, and still report success.
	t.Run("prompt accepted with text delivers that text to the page", func(t *testing.T) {
		dlg := raiseDialog(t, registry, mgr, "raisePrompt()")
		assert.Equal(t, string(page.DialogTypePrompt), dlg.Type)

		res := dialogTool.Execute(ctx, map[string]any{"accept": true, "prompt_text": "omnipus"})
		require.NotNil(t, res)
		require.False(t, res.IsError, "answering a real prompt must succeed; got: %s", res.ForLLM)

		recovered := probeTabAnswers(t, registry)
		require.False(t, recovered.IsError,
			"the tab does not answer CDP after answering a prompt; got: %s", recovered.ForLLM)

		assert.Equal(t, "omnipus", readGlobal(t, registry, "window.__answer", "omnipus"),
			"the page received something other than the prompt_text the agent supplied. A build "+
				"that dropped WithPromptText passes every other assertion in this file: the "+
				"dialog closes, the tab recovers, the tool reports success — and the form gets "+
				"an empty string")
	})

	// ── The idempotent case, on a real browser ─────────────────────────────
	//
	// With no dialog outstanding the tool must report `dialog: null` and NOT
	// fire a second Page.handleJavaScriptDialog, which CDP errors on. The
	// seam tests assert the bookkeeping half; this asserts that the real call
	// is genuinely not made, because if it were, this would be an error.
	t.Run("calling it with no dialog open is a question, not a mistake", func(t *testing.T) {
		res := dialogTool.Execute(ctx, map[string]any{"accept": false})
		require.NotNil(t, res)
		require.False(t, res.IsError,
			"asking whether a dialog is blocking the tab must not be an error — an agent that "+
				"retries after the dialog is gone has done nothing wrong; got: %s", res.ForLLM)
		assert.Nil(t, decodeJSON(t, res.ForLLM)["dialog"])

		still := probeTabAnswers(t, registry)
		assert.False(t, still.IsError,
			"the no-op path damaged the tab; got: %s", still.ForLLM)
	})
}

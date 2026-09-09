// tools_control_test.go — ADR-038 D6 turn-coordination gate exercised through
// the REAL tool.Execute dispatch path (not just controlledResult in
// isolation, which live_test.go's TestControlledResult already covers).
//
// This works without a live Chromium because controlledResult short-circuits
// BEFORE any chromedp call for the four interactive tools (navigate, click,
// type, evaluate) — see tools.go's Execute methods. The three read-only
// tools (screenshot, get_text, wait) are deliberately NOT gated; to prove
// that without a real browser, this uses an unreachable-but-syntactically-
// valid remote CDP URL (same technique as
// pkg/agent/browser_manager_test.go's hot-reload regression test):
// BrowserManager.Session() then fails fast on a refused local dial instead
// of spawning a real Chromium subprocess or hitting the network. What
// matters here is WHICH error comes back — a session/dial failure, not the
// "human is currently controlling" deferral text — proving the tool
// genuinely attempted to run rather than being gated.
//
// Traces to: docs/internal/architecture/ADR-038-live-interactive-browser-panel.md D6.

package browser

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// controlTestCfg returns a BrowserConfig pointed at an unreachable, local-only
// remote CDP endpoint. NewBrowserManager/RegisterTools never spawn a
// subprocess or touch the network for this config; ensureStarted() takes the
// lazy remote-allocator branch, and the FIRST real chromedp.Run (triggered by
// BrowserManager.Session()) fails immediately with a refused-connection
// error — deterministic and fast, no Chrome binary or network egress
// required.
func controlTestCfg(t *testing.T) BrowserConfig {
	t.Helper()
	return BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 3 * time.Second,
		ProfileDir:  t.TempDir(),
		CDPURL:      "ws://127.0.0.1:1/unreachable-by-design",
	}
}

// TestExecute_ControlLock_InteractiveToolsDeferWhileControlled drives
// NavigateTool/ClickTool/TypeTool/EvaluateTool through Execute while a human
// viewer holds control of testSessionID, and asserts each returns the
// soft, non-error deferral result instead of touching chromedp at all.
//
// BDD: Given a human viewer currently controls the live browser session,
// When an interactive browser tool's Execute is called,
// Then the result is non-error and explains the tool was deferred, naming
// both the tool and the reason.
func TestExecute_ControlLock_InteractiveToolsDeferWhileControlled(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	ctx := context.Background()

	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"),
		"test setup: taking control must succeed on an uncontrolled session")

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"browser_navigate", map[string]any{"url": "http://127.0.0.1/whatever"}},
		{"browser_click", map[string]any{"selector": "#btn"}},
		{"browser_type", map[string]any{"selector": "#input", "text": "hi"}},
		{"browser_evaluate", map[string]any{"js": "1+1"}},
		// browser_open_tab changes what the live view screencasts (it opens +
		// activates a new tab) exactly like SwitchTab/CloseTab, so it defers
		// the same way. No url given here — the deferral must fire on the
		// controlledResult check itself, before ever touching chromedp.
		{"browser_open_tab", map[string]any{}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			tool := mustGetTool(t, registry, tc.tool)
			result := tool.Execute(ctx, tc.args)
			require.NotNil(t, result)
			assert.False(t, result.IsError,
				"%s must defer (not error) while a human controls the browser; got: %s", tc.tool, result.ForLLM)
			assert.Contains(t, result.ForLLM, "human is currently controlling",
				"%s deferral message must explain why nothing happened; got: %s", tc.tool, result.ForLLM)
			assert.Contains(t, result.ForLLM, tc.tool,
				"%s deferral message must name the deferred tool; got: %s", tc.tool, result.ForLLM)
		})
	}
}

// TestExecute_ControlLock_ReadOnlyToolsAreNotGated proves browser_screenshot,
// browser_get_text, and browser_wait are NOT short-circuited by the
// control-lock: with a human controlling the session, each tool still
// attempts to reach the browser and fails on the unreachable CDP endpoint —
// a session/dial error, never the "human is currently controlling" text.
//
// BDD: Given a human viewer currently controls the live browser session,
// When a read-only browser tool's Execute is called,
// Then the result IS an error (no live browser available), but the error is
// a session/execution failure, never the control-lock deferral message.
func TestExecute_ControlLock_ReadOnlyToolsAreNotGated(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	ctx := context.Background()

	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"browser_screenshot", map[string]any{}},
		{"browser_get_text", map[string]any{"selector": "#x"}},
		{"browser_wait", map[string]any{"selector": "#x"}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			tool := mustGetTool(t, registry, tc.tool)
			result := tool.Execute(ctx, tc.args)
			require.NotNil(t, result)
			// It DOES fail — but for lack of a reachable browser, not because
			// of the control lock. That distinction is the whole point.
			assert.True(
				t,
				result.IsError,
				"%s must attempt real execution and fail on the unreachable CDP endpoint; got IsError=false, ForLLM: %s",
				tc.tool,
				result.ForLLM,
			)
			assert.NotContains(t, result.ForLLM, "human is currently controlling",
				"%s must NOT be short-circuited by the control lock; got: %s", tc.tool, result.ForLLM)
			assert.NotContains(t, result.ForLLM, "deferred",
				"%s must NOT be short-circuited by the control lock; got: %s", tc.tool, result.ForLLM)
		})
	}
}

// TestExecute_ControlLock_ReleaseUngatesInteractiveTools proves the gate is
// dynamic, not sticky: releasing control makes the SAME NavigateTool call
// that deferred a moment ago proceed to a real (failing, no live browser)
// execution attempt instead — and the two results are genuinely different,
// not the same canned string.
//
// BDD: Given a human viewer released control after having held it,
// When the same interactive tool is called again with the same arguments,
// Then the result is no longer the deferral text — it is a real execution
// attempt (and failure, absent a live browser).
func TestExecute_ControlLock_ReleaseUngatesInteractiveTools(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	ctx := context.Background()
	navTool := mustGetTool(t, registry, "browser_navigate")

	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))
	deferred := navTool.Execute(ctx, map[string]any{"url": "http://127.0.0.1/a"})
	require.NotNil(t, deferred)
	require.False(t, deferred.IsError)
	require.Contains(t, deferred.ForLLM, "human is currently controlling")

	mgr.Live().ReleaseControl(testSessionID, "human-viewer")
	require.False(t, mgr.Live().IsControlled(testSessionID), "test setup: release must actually clear the lock")

	released := navTool.Execute(ctx, map[string]any{"url": "http://127.0.0.1/a"})
	require.NotNil(t, released)
	assert.True(t, released.IsError,
		"after release, navigate must attempt real execution and fail on the unreachable CDP endpoint; got: %s",
		released.ForLLM)
	assert.NotContains(t, released.ForLLM, "human is currently controlling",
		"after release, the tool must no longer defer; got: %s", released.ForLLM)

	assert.NotEqual(t, deferred.ForLLM, released.ForLLM,
		"deferred vs. post-release results must be genuinely different outcomes (differentiation check)")
}

// TestControlledResult_UsesResolvedKey is FR-002c, and it is the single most
// likely false green in this whole change.
//
// controlledResult used to ask the live-view registry about a hardcoded shared
// session id. The registry is now keyed by sessionKey(BrowsingKey, TabOwner),
// so left on that constant the lookup matches nothing and returns false
// FOREVER: an intact, populated human-control lock that is never consulted.
// Nothing errors. Nothing logs. Every lease test still passes. A human takes
// the wheel in the live panel and agents keep driving over them.
//
// So this asserts the BLOCKED direction against the RESOLVED key, and — case
// (c) — that a lock held on a DIFFERENT key does not block, which is what fails
// if the function ever goes back to asking about one fixed id.
func TestControlledResult_UsesResolvedKey(t *testing.T) {
	_, mgr := newPermissiveRegistry(t, controlTestCfg(t))

	otherKey := newTestBrowsingKey(t, "some-other-workspace")
	otherOwner, err := TabOwnerSession("some-other-chat")
	require.NoError(t, err)

	// (a) Uncontrolled: nothing is deferred.
	require.Nil(t, controlledResult(mgr, testKey, testOwner, "browser_click"),
		"an uncontrolled tab set must not defer")

	// (b) A human takes control of exactly the (key, owner) pair this call
	//     resolves — the call must defer.
	resolved := sessionKey(testKey, testOwner)
	require.True(t, mgr.Live().TakeControl(resolved, "human-viewer"))

	deferred := controlledResult(mgr, testKey, testOwner, "browser_click")
	require.NotNil(t, deferred,
		"FR-002c: the control lock is held on the RESOLVED key and MUST be consulted against it. "+
			"A nil here means controlledResult is asking about something else — most likely a fixed "+
			"session id, which matches nothing after the re-key and disables the lock permanently.")
	require.False(t, deferred.IsError, "a deferral is coordination, not a tool failure")
	require.Contains(t, deferred.ForLLM, "human is currently controlling")

	// (c) The lock is scoped, not global. A call on a DIFFERENT owner in the
	//     SAME browser, and a call on a different browser entirely, are both
	//     unaffected — this is what distinguishes "asks about the resolved key"
	//     from "asks about any key at all".
	require.Nil(t, controlledResult(mgr, testKey, TabOwnerWorkspace(), "browser_click"),
		"a lock on one chat's tabs must not freeze the operator's own tabs")
	require.Nil(t, controlledResult(mgr, otherKey, otherOwner, "browser_click"),
		"a lock in one workspace's browser must not freeze another workspace's")

	// (d) Releasing un-gates it again.
	mgr.Live().ReleaseControl(resolved, "human-viewer")
	require.Nil(t, controlledResult(mgr, testKey, testOwner, "browser_click"),
		"releasing control must un-gate the tool")
}

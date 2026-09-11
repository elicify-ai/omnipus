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
		// browser_open_tab USED to be in this list. D-G (operator decision,
		// 2026-09-11) removed it: it is the control gate's ESCAPE HATCH, and
		// it is asserted positively — that it does NOT defer — by
		// TestOpenTab_IsTheControlGateEscapeHatch below.
		//
		// Its two siblings browser_switch_tab/browser_close_tab are still
		// gated but cannot stand in for it HERE: both validate their `index`
		// argument before reaching controlledResult, so on this fixture (no
		// tabs, no reachable browser) they fail the range check first and
		// never reach the gate. Their gating is covered structurally instead,
		// by control_gate_membership_test.go's catalog-minus-exemptions
		// assertion.
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

// TestOpenTab_IsTheControlGateEscapeHatch is D-G's requirement, asserted from
// the production path: with a human holding the wheel, browser_open_tab must
// NOT come back as a deferral — it must reach the browser and fail on the
// unreachable CDP endpoint, exactly like an ungated tool.
//
// Why it is worth its own test rather than a row in a table. The agent is
// told, in three separate places (browser_handover's tool description, its
// success message and its FR-052 refusal message), that opening a new tab is
// the way to keep working while the operator holds the wheel. Before D-G's
// carve-out landed, browser_open_tab went through controlledResult like every
// other write verb and returned a deferral — the instruction was unfollowable,
// and nothing failed to say so. This test is what makes that promise
// falsifiable.
//
// BDD: Given a human viewer currently controls the live browser session,
// When browser_open_tab's Execute is called,
// Then the result is NOT a control-gate deferral.
func TestOpenTab_IsTheControlGateEscapeHatch(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	ctx := context.Background()

	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"),
		"test setup: taking control must succeed on an uncontrolled session")
	require.True(t, mgr.Live().IsStoodDown(testSessionID),
		"test setup: the take must actually have stood the tab set down, or this test proves nothing")

	result := mustGetTool(t, registry, "browser_open_tab").Execute(ctx, map[string]any{})
	require.NotNil(t, result)
	assert.NotContains(t, result.ForLLM, "human is currently controlling",
		"D-G: browser_open_tab must not defer while the wheel is held; got: %s", result.ForLLM)
	assert.NotContains(t, result.ForLLM, `"deferred":true`,
		"D-G: browser_open_tab must not defer while the wheel is held; got: %s", result.ForLLM)
	assert.Nil(t, result.Deferred,
		"D-G: the structural deferral signal the turn engine's ledger reads must be absent too; got: %+v",
		result.Deferred)
	assert.True(t, result.IsError,
		"browser_open_tab must have gone on to attempt real execution and failed on the unreachable "+
			"CDP endpoint — a non-error, non-deferral result would mean it short-circuited somewhere "+
			"else and this test proves nothing; got: %s", result.ForLLM)
}

// TestOpenTab_IsExcludedFromTheEngineShortCircuitSet is the other half of
// D-G's carve-out. ControlGatedToolNames() is consumed once, at construction,
// by pkg/agent/browser_deferral.go to build the FR-016 set the TURN ENGINE
// short-circuits before dispatch after three deferrals in one turn. A held
// wheel produces exactly those three deferrals, so leaving browser_open_tab in
// that set would slam the escape hatch shut from the engine side on precisely
// the turn the agent needs it — with the gate itself never consulted.
func TestOpenTab_IsExcludedFromTheEngineShortCircuitSet(t *testing.T) {
	names := ControlGatedToolNames()
	require.NotEmpty(t, names, "an empty roster would pass the exclusion assertion vacuously")
	assert.NotContains(t, names, "browser_open_tab",
		"D-G: the engine's short-circuit set must not contain the one tool the gate lets through")
	// Differentiation: a sibling write verb that is NOT the escape hatch must
	// still be in the set, so this cannot pass by the roster being broken.
	assert.Contains(t, names, "browser_switch_tab",
		"the roster must still carry the gated tab verbs — an empty/broken roster would make the "+
			"exclusion above meaningless")
}

// TestExecute_ControlLock_ExemptToolsAreNotGated proves the FR-035 EXEMPT
// class is not short-circuited by the control lock: with a human controlling
// the session, an exempt tool still attempts to reach the browser and fails on
// the unreachable CDP endpoint — a session/dial error, never the "human is
// currently controlling" text.
//
// browser_screenshot and browser_get_text USED to be in this list. ADR-085 D5
// moved them into the CAPTURE class, which IS gated (they can photograph or
// read a page a human is mid-typing into), and
// TestExecute_ControlLock_CaptureToolsDefer below asserts their new behaviour.
// Leaving them here asserted the exact exposure D5 exists to close.
//
// BDD: Given a human viewer currently controls the live browser session,
// When a control-gate-exempt browser tool's Execute is called,
// Then the result IS an error (no live browser available), but the error is
// a session/execution failure, never the control-lock deferral message.
func TestExecute_ControlLock_ExemptToolsAreNotGated(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	ctx := context.Background()

	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))

	cases := []struct {
		tool string
		args map[string]any
	}{
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

// TestExecute_ControlLock_CaptureToolsDefer is the assertion ADR-085 D5
// created and that this file was still missing: the three CAPTURE-class tools
// observe the page without injecting input, but under D1's "the agent keeps
// running" turn model they could otherwise photograph, read or describe a page
// a human is actively typing a credential into. They are gated.
//
// BDD: Given a human viewer currently controls the live browser session,
// When a capture-class browser tool's Execute is called,
// Then the result is the non-error control-gate deferral, naming the tool.
func TestExecute_ControlLock_CaptureToolsDefer(t *testing.T) {
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	ctx := context.Background()

	require.True(t, mgr.Live().TakeControl(testSessionID, "human-viewer"))

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"browser_screenshot", map[string]any{}},
		{"browser_get_text", map[string]any{"selector": "#x"}},
		{"browser_snapshot", map[string]any{}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result := mustGetTool(t, registry, tc.tool).Execute(ctx, tc.args)
			require.NotNil(t, result)
			assert.False(t, result.IsError,
				"%s must defer (not error) while a human controls the browser; got: %s", tc.tool, result.ForLLM)
			assert.Contains(t, result.ForLLM, "human is currently controlling",
				"%s deferral message must explain why nothing happened; got: %s", tc.tool, result.ForLLM)
			require.NotNil(t, result.Deferred,
				"%s must carry the STRUCTURAL deferral signal the turn engine's ledger reads, not just "+
					"the prose; got ForLLM: %s", tc.tool, result.ForLLM)
			assert.Equal(t, "browser_control", result.Deferred.Gate,
				"%s must defer under the browser_control gate", tc.tool)
		})
	}
}

// TestExecute_ControlLock_ReleaseUngatesInteractiveTools proves the gate is
// dynamic, not sticky: releasing control makes the SAME NavigateTool call
// that deferred a moment ago proceed to a real (failing, no live browser)
// execution attempt instead — and the two results are genuinely different,
// not the same canned string.
//
// RE-POINTED (ADR-085 FR-026a, Finding 7(b)). This drove the release through
// LiveViewRegistry.ReleaseControl, which clears ONLY lv.controller. Since
// FR-026a that is deliberately NOT a release: the stand-down latch survives
// it, precisely so a viewer who hits Escape or closes the panel does not hand
// the page back to an agent mid-way through what they were doing. The
// SERVER-INITIATED release — what the panel's release button, the FR-029
// prompt release and the FR-031a sweeper all now perform — is
// ReleaseStoodDown, which clears the lock, the latch and any handover-pending
// state together. Nothing about what this test ASSERTS has changed; only
// which release it performs.
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

	formerHolder, cleared := mgr.Live().ReleaseStoodDown(testSessionID)
	require.True(t, cleared, "test setup: the release must actually have cleared something")
	require.Equal(t, "human-viewer", formerHolder,
		"test setup: the release must name the viewer that held the wheel")
	require.False(t, mgr.Live().IsControlled(testSessionID), "test setup: release must actually clear the lock")
	require.False(t, mgr.Live().IsStoodDown(testSessionID),
		"test setup: release must clear the FR-026a stand-down latch too, not just the lock — "+
			"a latch left behind is Finding 7(b), and this test would then be asserting nothing")

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
	ctx := context.Background()

	otherKey := newTestBrowsingKey(t, "some-other-workspace")
	otherOwner, err := TabOwnerSession("some-other-chat")
	require.NoError(t, err)

	// (a) Uncontrolled: nothing is deferred.
	require.Nil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"an uncontrolled tab set must not defer")

	// (b) A human takes control of exactly the (key, owner) pair this call
	//     resolves — the call must defer.
	resolved := sessionKey(testKey, testOwner)
	require.True(t, mgr.Live().TakeControl(resolved, "human-viewer"))

	deferred := controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil)
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
	require.Nil(t, controlledResult(ctx, mgr, testKey, TabOwnerWorkspace(), "browser_click", nil),
		"a lock on one chat's tabs must not freeze the operator's own tabs")
	require.Nil(t, controlledResult(ctx, mgr, otherKey, otherOwner, "browser_click", nil),
		"a lock in one workspace's browser must not freeze another workspace's")

	// (d) ADR-085 FR-026a: a bare ReleaseControl does NOT un-gate the tool —
	// the stand-down latch survives a release that is not a genuine ADR-085
	// release (the operator's next prompt, or idle expiry).
	mgr.Live().ReleaseControl(resolved, "human-viewer")
	require.NotNil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"ADR-085 FR-026a: a bare ReleaseControl must not un-gate the tool")

	// The genuine ADR-085 release does.
	_, cleared := mgr.Live().ReleaseStoodDown(resolved)
	require.True(t, cleared)
	require.Nil(t, controlledResult(ctx, mgr, testKey, testOwner, "browser_click", nil),
		"ReleaseStoodDown must un-gate the tool")
}

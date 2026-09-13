// tools_handover_test.go — ADR-085 D7, BROWSER-FR-046/047/048a/049/052
// (wave B6). Exercises the REAL tool.Execute dispatch path, same technique
// as tools_control_test.go: no live Chromium is ever touched because
// browser_handover never calls chromedp — it only mutates LiveView state
// and (best-effort) writes an audit record.
//
// Traces to: docs/internal/specs/browser-control-handover-spec.md §G,
// US-9, US-11.

package browser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// handoverTestCtx builds a context carrying the tool-context fields
// recordHandover reads, so audit assertions have real, non-empty values to
// check rather than the zero-value strings every field falls back to.
func handoverTestCtx(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	ctx = tools.WithAgentID(ctx, "test-agent")
	ctx = tools.WithTranscriptSessionID(ctx, testTranscriptSessionID)
	ctx = tools.WithToolRootChatSessionID(ctx, testTranscriptSessionID)
	ctx = tools.WithToolCallID(ctx, "call-1")
	return ctx
}

// newHandoverTestManager builds a BrowserManager bound to the package's
// standard (testKey, testOwner) pair, with take-control ENABLED unless the
// caller overrides it — mirrors controlTestCfg's spirit but needs no CDP
// endpoint at all, since Execute never dials one.
func newHandoverTestManager(t *testing.T, takeControlEnabled bool) *BrowserManager {
	t.Helper()
	mgr, err := NewBrowserManager(BrowserConfig{TakeControlEnabled: takeControlEnabled}, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	mgr.key = testKey
	return mgr
}

func newHandoverTool(t *testing.T, mgr *BrowserManager) (*HandoverTool, *auditHarness) {
	t.Helper()
	h := newAuditHarness(t)
	tool := &HandoverTool{res: newFixedResolver(mgr)}
	tool.SetAuditLogger(h.log)
	return tool, h
}

// TestHandoverTool_ReturnsConcludeInstructionAndDoesNotPark pins
// BROWSER-FR-046/FR-049: the tool exists, takes a `reason` string, and its
// successful result is non-error, does not park the turn, and instructs the
// agent to conclude with an explanation.
//
// BDD (US-9 AS-1, AS-4): Given an agent reaches a step it should not
// perform, When it hands the browser over, Then the agent concludes its
// turn with an explanation and the turn ends normally — not suspended, not
// parked.
func TestHandoverTool_ReturnsConcludeInstructionAndDoesNotPark(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	tool, _ := newHandoverTool(t, mgr)

	result := tool.Execute(handoverTestCtx(t), map[string]any{"reason": "reached a payment step"})

	require.NotNil(t, result)
	assert.False(t, result.IsError, "a successful handover is not a tool error; got: %s", result.ForLLM)
	assert.False(t, result.ParksTurn, "BROWSER-FR-049: browser_handover MUST NOT set ParksTurn")
	assert.Contains(t, result.ForLLM, "browser_handover", "the result must name the tool")
	assert.Contains(t, result.ForLLM, "Conclude", "the result must instruct the agent to conclude its turn")
}

// TestHandoverTool_SetsHandoverPendingNotAViewerLock pins BROWSER-FR-047:
// the tool stands the browser down via the handover-pending state, which is
// distinct from lv.controller (there may be no viewer id to grant — the
// agent is handing the browser to an operator who need not be attached at
// all). This also proves the control gate treats it exactly like a held
// wheel for a SUBSEQUENT browser call in the same turn (US-9 AS-2).
//
// BDD (US-9 AS-2): Given the agent handed over, When it attempts browser
// work in the same turn, Then it defers.
func TestHandoverTool_SetsHandoverPendingNotAViewerLock(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	tool, _ := newHandoverTool(t, mgr)
	ctx := handoverTestCtx(t)

	result := tool.Execute(ctx, map[string]any{"reason": "need a human"})
	require.False(t, result.IsError)

	assert.True(t, mgr.Live().IsStoodDown(testSessionID),
		"handover must stand the tab set down for the control gate")
	assert.Empty(t, mgr.Live().Controller(testSessionID),
		"handover must NOT set a viewer/controller lock — there may be no viewer to grant it to")

	// A subsequent write-class call in the same turn must defer, exactly as
	// controlledResult defers for a human-held wheel.
	deferral := controlledResult(ctx, mgr, testKey, testOwner, "browser_navigate", &tool.browserAudit)
	require.NotNil(t, deferral, "a browser call after a handover must defer")
	assert.False(t, deferral.IsError)
	assert.Contains(t, deferral.ForLLM, "human is currently controlling")
}

// TestHandoverPending_IsNotAffectedByRepeatedCalls proves SetHandoverPending
// is safe to call twice — e.g. the agent hands over, and (per D-G) is told
// never to ask for the wheel back, but nothing here should panic or produce
// a second controller if it is called again before the next release.
func TestHandoverPending_IsNotAffectedByRepeatedCalls(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	tool, _ := newHandoverTool(t, mgr)
	ctx := handoverTestCtx(t)

	first := tool.Execute(ctx, map[string]any{"reason": "first"})
	second := tool.Execute(ctx, map[string]any{"reason": "second"})

	require.False(t, first.IsError)
	require.False(t, second.IsError)
	assert.True(t, mgr.Live().IsStoodDown(testSessionID))
	assert.False(t, second.ParksTurn)
}

// TestHandoverTool_ReasonAppearsInSurfaceAndAudit is the pkg/tools/browser
// half of BROWSER-FR-048a (the gateway half, covering the FR-041 waiting
// surface body, is pkg/gateway/browser_control_handover_test.go — outside
// this wave's write-set). It asserts the reason reaches both this tool's
// own result text and the FR-062 audit record, truncated at 200 runes.
func TestHandoverTool_ReasonAppearsInSurfaceAndAudit(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	tool, harness := newHandoverTool(t, mgr)
	ctx := handoverTestCtx(t)

	longReason := ""
	for i := 0; i < 250; i++ {
		longReason += "a"
	}

	result := tool.Execute(ctx, map[string]any{"reason": longReason})
	require.False(t, result.IsError)

	truncated := string([]rune(longReason)[:handoverReasonMaxRunes])
	assert.Contains(t, result.ForLLM, truncated, "the result must carry the truncated reason")
	assert.NotContains(t, result.ForLLM, longReason, "the result must NOT carry the untruncated reason")

	events := harness.eventsNamed(t, "browser_handover")
	require.Len(t, events, 1, "exactly one EventBrowserHandover record must be written")
	details, ok := events[0]["details"].(map[string]any)
	require.True(t, ok, "the audit entry must carry a details object")
	assert.Equal(t, truncated, details["reason"],
		"the audit record's reason must be truncated to %d runes", handoverReasonMaxRunes)
}

// TestHandoverTool_EmptyReasonProducesNoParenthetical pins BROWSER-FR-048a's
// "an empty or whitespace-only reason MUST NOT produce an empty
// parenthetical" rule, applied identically to the audit record: no "reason"
// key at all, never an empty-string one.
func TestHandoverTool_EmptyReasonProducesNoParenthetical(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	tool, harness := newHandoverTool(t, mgr)
	ctx := handoverTestCtx(t)

	result := tool.Execute(ctx, map[string]any{"reason": "   "})
	require.False(t, result.IsError)
	assert.NotContains(t, result.ForLLM, "()", "a blank reason must not leave an empty parenthetical")

	events := harness.eventsNamed(t, "browser_handover")
	require.Len(t, events, 1)
	details, ok := events[0]["details"].(map[string]any)
	require.True(t, ok)
	_, hasReason := details["reason"]
	assert.False(t, hasReason, "an empty reason must produce no reason key, never an empty-string one")
}

// TestHandoverTool_AuditRecordsAgentAsActor pins BROWSER-FR-062: the agent
// is recorded as the actor (Entry.AgentID), not a human "acting_user" —
// EventBrowserHandover's own doc comment (pkg/audit/events.go) draws this
// distinction explicitly against EventBrowserControlDeferred.
func TestHandoverTool_AuditRecordsAgentAsActor(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	tool, harness := newHandoverTool(t, mgr)
	ctx := tools.WithAgentID(handoverTestCtx(t), "the-acting-agent")

	result := tool.Execute(ctx, map[string]any{"reason": "handing over"})
	require.False(t, result.IsError)

	events := harness.eventsNamed(t, "browser_handover")
	require.Len(t, events, 1)
	assert.Equal(t, "the-acting-agent", events[0]["agent_id"])
	assert.Equal(t, "allow", events[0]["decision"], "an agent's own successful handover is an allow, not a deny")
	details, ok := events[0]["details"].(map[string]any)
	require.True(t, ok)
	_, hasActingUser := details["acting_user"]
	assert.False(t, hasActingUser, "a handover is the agent's own action; it must not carry a human acting_user field")
}

// TestHandoverTool_RefusedWhenTakeControlDisabled pins BROWSER-FR-052 —
// this wave's own share of that requirement, "the tool's own refusal".
//
// BDD (US-11 AS-2): Given take-control is disabled, When the agent attempts
// to hand the browser over, Then the handover does not take effect and the
// agent is told so.
func TestHandoverTool_RefusedWhenTakeControlDisabled(t *testing.T) {
	mgr := newHandoverTestManager(t, false)
	tool, harness := newHandoverTool(t, mgr)
	ctx := handoverTestCtx(t)

	result := tool.Execute(ctx, map[string]any{"reason": "reached a payment step"})

	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "disabled", "the agent must be told the feature is off")
	assert.False(t, result.ParksTurn)
	assert.False(t, mgr.Live().IsStoodDown(testSessionID),
		"BROWSER-FR-052: a disabled take-control feature must NOT take effect — no stand-down")

	// It must not become a bypass: no audit record for a handover that
	// never happened.
	events := harness.eventsNamed(t, "browser_handover")
	assert.Empty(t, events, "a refused handover must not be recorded as though it succeeded")
}

// TestHandoverTool_RefusalOffersNewTabNeverWait pins the D-G addition to
// this wave: the FR-052 refusal must positively instruct the agent NOT to
// wait for the flag to change and NOT to ask for the wheel back, and must
// offer the new-tab route as the way to continue other browser work.
func TestHandoverTool_RefusalOffersNewTabNeverWait(t *testing.T) {
	mgr := newHandoverTestManager(t, false)
	tool, _ := newHandoverTool(t, mgr)
	ctx := handoverTestCtx(t)

	result := tool.Execute(ctx, map[string]any{"reason": "reached a payment step"})

	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "browser_open_tab", "D-G: the refusal must offer the new-tab route")
	assert.Contains(t, result.ForLLM, "Do not wait", "D-G: the refusal must explicitly say not to wait")
	assert.Contains(t, result.ForLLM, "do not ask for it back", "D-G: the refusal must explicitly say not to ask for the wheel back")
}

// TestHandoverTool_NoReasonRequired proves `reason` is optional (FR-046
// says "taking a single reason string"; nothing requires it — FR-048a's own
// "an empty or whitespace-only reason MUST NOT produce an empty
// parenthetical" rule only makes sense if an empty/absent reason is a legal
// call, not a validation error).
func TestHandoverTool_NoReasonRequired(t *testing.T) {
	mgr := newHandoverTestManager(t, true)
	tool, _ := newHandoverTool(t, mgr)

	result := tool.Execute(handoverTestCtx(t), map[string]any{})

	require.NotNil(t, result)
	assert.False(t, result.IsError, "reason must be optional; got: %s", result.ForLLM)
}

// TestHandoverTool_ResolveTurnFailurePropagates proves the tool goes
// through the same resolveTurn seam as every other browser tool rather than
// inventing its own resolution, so a turn with no browsing context reports
// the same structured failure the rest of the package already relies on.
func TestHandoverTool_ResolveTurnFailurePropagates(t *testing.T) {
	tool := &HandoverTool{res: &fixedResolver{err: ErrNoBrowsingContext}}

	result := tool.Execute(handoverTestCtx(t), map[string]any{"reason": "x"})

	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, ErrNoBrowsingContext.Error())
}

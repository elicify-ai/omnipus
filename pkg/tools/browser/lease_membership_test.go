package browser

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// lease_membership_test.go — FR-019a, the biconditional.
//
// The rule is: a browser_* tool takes the write lease IF AND ONLY IF it is
// gated by the ADR-038 D6 human-control lock.
//
// The previous rule was "every tool that mutates page or tab state acquires the
// lease, and the exemption list is exactly these five" — which contradicted
// itself (browser_handle_dialog mutates page state and was on the exemption
// list), so no test could classify by it. The only test that rule admitted was
// membership of a hand-written list living in the spec, which is a test of the
// list, not of the code.
//
// The control-lock gate is a better classifier because it already exists in
// shipped code, it was decided in ADR-038 D6 on the same question ("may an
// agent do this while a human is driving?"), and it partitions the eleven
// shipped tools exactly the way the lease needs. One classification, two
// consumers, no second list to drift.

// minimalArgsFor returns arguments sufficient to get each tool past its own
// parameter validation and as far as the two gates. They are deliberately
// minimal — this test is about which gate answers, never about what the tool
// does afterwards.
func minimalArgsFor(name string) map[string]any {
	switch name {
	case "browser_navigate":
		return map[string]any{"url": "http://127.0.0.1/whatever"}
	case "browser_click", "browser_get_text", "browser_wait":
		return map[string]any{"selector": "#anything"}
	case "browser_type":
		return map[string]any{"selector": "#anything", "text": "hi"}
	case "browser_evaluate":
		return map[string]any{"js": "1+1"}
	case "browser_switch_tab", "browser_close_tab":
		return map[string]any{"index": float64(0)}
	// ADR-072 D2. These must get PAST their own parameter validation, or the
	// tool returns an argument error before reaching either gate and the
	// biconditional below reads "does not defer" for a tool that in fact does.
	case "browser_hover":
		return map[string]any{"selector": "#anything"}
	case "browser_select_option":
		return map[string]any{"selector": "#anything", "label": "Anything"}
	case "browser_press_key":
		// Deliberately NO locator: that is the one call shape that skips the
		// actionability gate, and it must still take the lease (spec A-10).
		return map[string]any{"key": "Enter"}
	default:
		return map[string]any{}
	}
}

const (
	humanControlDeferralMarker = "human is currently controlling"
	leaseDeferralMarker        = "did not finish within the wait budget"
)

// TestWriteLease_EveryActionToolIsLeased enumerates the REGISTRY — not a list in
// this file — and exercises every registered browser tool twice: once with a
// human holding the control lock, once with another turn holding the write
// lease. The two deferral answers must AGREE for every tool.
//
// Setup runs against TabOwnerWorkspace(), the operator's own tab set, because
// that is where BOTH gates are live. Running it against a chat's own tabs would
// exercise the lease but never the control lock's human-held branch, and would
// pass with the classification half-checked.
func TestWriteLease_EveryActionToolIsLeased(t *testing.T) {
	owner := TabOwnerWorkspace()

	// Pass 1 — a human holds the control lock.
	lockRegistry := tools.NewToolRegistry()
	lockMgr := registerToolsForTestAs(t, lockRegistry, controlTestCfg(t),
		security.NewSSRFChecker([]string{"127.0.0.1"}),
		func(m *BrowserManager) ManagerResolver { return newOperatorResolver(m) })
	t.Cleanup(lockMgr.Shutdown)
	require.True(t, lockMgr.Live().TakeControl(sessionKey(testKey, owner), "human-viewer"))

	// Pass 2 — another turn holds the write lease.
	leaseRegistry := tools.NewToolRegistry()
	leaseMgr := registerToolsForTestAs(t, leaseRegistry, controlTestCfg(t),
		security.NewSSRFChecker([]string{"127.0.0.1"}),
		func(m *BrowserManager) ManagerResolver { return newOperatorResolver(m) })
	t.Cleanup(leaseMgr.Shutdown)
	leaseMgr.cfg.LeaseWait = 20 * time.Millisecond
	release, ok, _ := leaseMgr.acquireWrite(context.Background(), testKey, owner, "another-turn")
	require.True(t, ok)
	t.Cleanup(release)

	names := map[string]bool{}
	for _, tool := range lockRegistry.GetAll() {
		names[tool.Name()] = true
	}
	// 11 shipped + ADR-072 D2's four registered tools (select_option,
	// press_key, hover, snapshot). browser_upload_file is implemented and
	// seeded but NOT registered while FR-029 holds it, and
	// browser_handle_dialog belongs to the dialog stream — both raise this
	// number when they land.
	require.Len(t, names, 15, "the browser tool surface is fifteen registered tools")

	var leasedCount, exemptCount int
	for name := range names {
		t.Run(name, func(t *testing.T) {
			args := minimalArgsFor(name)

			lockTool, found := lockRegistry.Get(name)
			require.True(t, found)
			lockResult := lockTool.Execute(context.Background(), args)
			require.NotNil(t, lockResult)
			defersUnderLock := strings.Contains(lockResult.ForLLM, humanControlDeferralMarker)

			leaseTool, found := leaseRegistry.Get(name)
			require.True(t, found)
			leaseResult := leaseTool.Execute(context.Background(), args)
			require.NotNil(t, leaseResult)
			defersUnderLease := strings.Contains(leaseResult.ForLLM, leaseDeferralMarker)

			require.Equal(t, defersUnderLock, defersUnderLease,
				"%s defers under the control lock = %v but under the write lease = %v. "+
					"A tool must take the lease IF AND ONLY IF it is control-gated (FR-019a): a tool "+
					"leased but ungated lets an agent act while a human drives; a tool gated but "+
					"unleased lets two turns interleave CDP commands on one page.",
				name, defersUnderLock, defersUnderLease)

			if defersUnderLock {
				leasedCount++
			} else {
				exemptCount++
			}
		})
	}

	// Ten leased: the seven shipped (navigate, click, type, evaluate,
	// switch_tab, close_tab, open_tab) plus D2's three interaction verbs
	// (select_option, press_key, hover). Five exempt: the four shipped
	// read-only tools (screenshot, get_text, wait, list_tabs) plus
	// browser_snapshot, which is read-only by requirement (D2 FR-038) and
	// must answer while a human drives the tab.
	//
	// They are asserted so that a build in which BOTH gates stopped working
	// cannot pass the biconditional above by agreeing on "never defers".
	require.Equal(t, 10, leasedCount, "ten registered tools are control-gated and therefore leased")
	require.Equal(t, 5, exemptCount, "five registered tools are read-only and therefore exempt from both gates")
}

// TestRegister_NoTakeControlTool is FR-070's structural half: acquisition of the
// operator's shared tab is IMPLICIT and has NO surface. No take-control tool, no
// twelfth policy entry, no wire field. An agent acquires the tab by acting on
// it, and the control lock is the whole mitigation.
func TestRegister_NoTakeControlTool(t *testing.T) {
	registry := tools.NewToolRegistry()
	mgr := registerToolsForTestAs(t, registry, controlTestCfg(t),
		security.NewSSRFChecker(nil),
		func(m *BrowserManager) ManagerResolver { return newOperatorResolver(m) })
	t.Cleanup(mgr.Shutdown)

	forbidden := []string{"control", "acquire", "claim", "take_over", "takeover", "handover", "handoff"}
	for _, tool := range registry.GetAll() {
		name := strings.ToLower(tool.Name())
		for _, word := range forbidden {
			require.NotContains(t, name, word,
				"%s looks like an acquisition surface. FR-070: taking the operator's tab is implicit — "+
					"there is no tool, no policy entry and no wire field for it, and adding one would "+
					"need the D1.9b ruling reopened.", tool.Name())
		}
	}
	require.Len(t, registry.GetAll(), 15, "the tool surface stays at fifteen registered tools")
}

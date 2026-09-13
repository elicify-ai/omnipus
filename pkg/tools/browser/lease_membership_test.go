package browser

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// lease_membership_test.go — FR-019a, AMENDED by ADR-085 R3-a/FR-036: the
// biconditional is now "leased iff ACTION-class", not "leased iff
// control-gated". The two used to coincide (§14 rule 3's original
// two-way split), but ADR-085 D5 adds three CAPTURE-class tools
// (browser_screenshot/get_text/snapshot) that defer under the SAME
// human-control lock without ever taking the write lease — capturing pixels
// or text is not a page mutation, so it never needs to serialise behind
// another tool's in-flight write. What survives from the original rule:
// every LEASED tool is still control-gated (action ⊆ gated); what does not
// survive: control-gated no longer implies leased (gated = action ∪
// capture, a strictly larger set).
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
	// ADR-075 D2. These must get PAST their own parameter validation, or the
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
// seedOperatorTabForGateTest puts ONE tab in `owner`'s tab set so that the two
// index-taking tools can resolve ownership and actually REACH the gates these
// tests are about.
//
// Why it is needed at all. ADR-075 D1.9b gave a turn's own tabs and the
// operator's tabs ONE merged index space, so browser_switch_tab and
// browser_close_tab must now resolve the index onto a tab set BEFORE either
// gate can be consulted — "is a human driving the set this call addresses" is
// unanswerable until you know which set that is (§14.2 rule 1 step 1; see
// resolveTabIndex). Against an EMPTY set, index 0 fails ownership resolution
// and comes back "tab index 0 is out of range", so both tools read as
// "defers under neither gate" and the ordering they are here to prove is never
// exercised. These fixtures were written against the pre-merge SINGLE-SET
// model, in which the control gate ran first and an empty set still reached
// it.
//
// The count oracle below is what caught this. Every subtest still PASSED,
// because the biconditional "leased iff control-gated" holds trivially when
// BOTH sides are absent; only leasedCount dropping from 10 to 8 said that the
// gated set had quietly shrunk.
//
// The seeded tab carries a live PLAIN context — deliberately not a
// chromedp one. That keeps the six exempt tools cheap and honest: chromedp.Run
// rejects a context it did not create, so browser_screenshot/get_text/wait/
// snapshot fail instantly with "invalid context" instead of allocating and
// LAUNCHING a real Chrome each. An already-cancelled context does not work
// either — Session() treats a dead active tab as a browser crash and deletes
// the whole browsing context, so the first ungated tool to run would wipe the
// seed before the index tools were reached.
//
// Nothing here touches the FR-060 host-memory gate: createFirstTab is the
// function that consults it, and this deliberately does not call it. That is
// why there is no memoryPressureFn pin — no host-load-dependent step is left
// to pin, so this fixture cannot go red because the machine is busy.
//
// ONE TOOL NOW RUNS PAST THE GATE, and the two lines that let it do so
// cheaply are the rest of this function. ADR-085's D-G carve-out
// (controlGateEscapeHatchTools, tools.go) makes browser_open_tab the one
// registered tool the control gate lets through unconditionally — so under a
// held wheel it no longer stops at the deferral, it goes on and really opens
// a tab. Against a hand-seeded sessionEntry that is not survivable as
// written:
//
//   - OpenTab's "existing browsing context" branch reads se.browserCtx and
//     hands it to chromedp.NewContext, which PANICS on a nil parent. A
//     production entry always has one; this one never did, because before
//     D-G nothing reached that line. createTabFn is the seam that skips the
//     whole CDP path (the same seam tabs_test.go's fake-tab manager uses),
//     and it ignores the parent context, so no browserCtx is needed and no
//     Chrome is launched.
//   - installTargetListenerLocked calls chromedp.ListenTarget on tab 0's
//     context, which panics on a context chromedp did not create — and it
//     panics while holding m.mu, hanging the next Shutdown. Seeding
//     listenerTarget to the root tab's own id takes its
//     already-installed early return instead.
//
// The fake tab carries a PLAIN context for the same reason the seeded one
// does: runTabFocusCDP and syncDialogListenersLocked both check
// chromedp.FromContext and skip a context that is not chromedp's, so the
// post-open focus and dialog-listener steps cost nothing and cannot allocate
// a browser.
func seedOperatorTabForGateTest(t *testing.T, m *BrowserManager, owner TabOwner) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sessionKey(testKey, owner)] = &sessionEntry{
		tabs:           []*tabEntry{{ctx: ctx, cancel: cancel, targetID: seededGateTestTargetID}},
		listenerTarget: seededGateTestTargetID,
	}
	var opened int64
	m.createTabFn = func(context.Context, target.ID) (*tabEntry, error) {
		tabCtx, tabCancel := context.WithCancel(context.Background())
		t.Cleanup(tabCancel)
		return &tabEntry{
			ctx:      tabCtx,
			cancel:   tabCancel,
			targetID: target.ID(fmt.Sprintf("gate-test-escape-hatch-tab-%d", atomic.AddInt64(&opened, 1))),
		}, nil
	}
}

// seededGateTestTargetID is the CDP target id of the tab seedOperatorTabForGateTest
// plants. It is a named constant because it has to appear twice — once as the
// tab's own id and once as the session's listenerTarget — and the early return
// that keeps chromedp.ListenTarget from panicking depends on the two being
// equal.
const seededGateTestTargetID target.ID = "seeded-for-gate-test"

func TestWriteLease_EveryActionToolIsLeased(t *testing.T) {
	owner := TabOwnerWorkspace()

	// Pass 1 — a human holds the control lock.
	lockRegistry := tools.NewToolRegistry()
	lockMgr := registerToolsForTestAs(t, lockRegistry, controlTestCfg(t),
		security.NewSSRFChecker([]string{"127.0.0.1"}),
		func(m *BrowserManager) ManagerResolver { return newOperatorResolver(m) })
	t.Cleanup(lockMgr.Shutdown)
	seedOperatorTabForGateTest(t, lockMgr, owner)
	require.True(t, lockMgr.Live().TakeControl(sessionKey(testKey, owner), "human-viewer"))

	// Pass 2 — another turn holds the write lease.
	leaseRegistry := tools.NewToolRegistry()
	leaseMgr := registerToolsForTestAs(t, leaseRegistry, controlTestCfg(t),
		security.NewSSRFChecker([]string{"127.0.0.1"}),
		func(m *BrowserManager) ManagerResolver { return newOperatorResolver(m) })
	t.Cleanup(leaseMgr.Shutdown)
	seedOperatorTabForGateTest(t, leaseMgr, owner)
	leaseMgr.cfg.LeaseWait = 20 * time.Millisecond
	release, ok, _ := leaseMgr.acquireWrite(context.Background(), testKey, owner, "another-turn")
	require.True(t, ok)
	t.Cleanup(release)

	names := map[string]bool{}
	for _, tool := range lockRegistry.GetAll() {
		names[tool.Name()] = true
	}
	// 11 shipped + ADR-075 D2's five registered tools (select_option,
	// press_key, hover, snapshot, handle_dialog) + ADR-085 D7's
	// browser_handover (BROWSER-FR-046). browser_upload_file is
	// implemented and seeded but NOT registered while FR-029 holds it, so it
	// raises this number when #659 closes.
	require.Len(t, names, 17, "the browser tool surface is seventeen registered tools")

	// The order is deterministic, and browser_handover runs LAST, on purpose.
	//
	// It is the one registered tool whose Execute MUTATES the state both gates
	// read: on a build where take-control is enabled it calls
	// LiveView.SetHandoverPending, which isStoodDownLocked then treats exactly
	// like a held wheel. Under Go's randomised map order that would make every
	// tool sequenced after it in the LEASE pass defer under the CONTROL gate
	// instead — reading as "not leased" for a tool that is — and the suite
	// would fail on roughly half its runs for a reason with no visible
	// connection to the lease.
	//
	// controlTestCfg leaves TakeControlEnabled at false today, so handover
	// takes its FR-052 refusal path and mutates nothing; this ordering is
	// insurance against that fixture changing, not a description of it.
	ordered := make([]string, 0, len(names))
	for name := range names {
		if name != "browser_handover" {
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	require.True(t, names["browser_handover"],
		"browser_handover is registered unconditionally (ADR-085 D7 / BROWSER-FR-046); if it is "+
			"absent the append below silently drops a tool from this enumeration")
	ordered = append(ordered, "browser_handover")

	var lockedCount, leasedCount int
	for _, name := range ordered {
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

			// FR-036 (ADR-085, amending FR-019a): leased IFF action-class —
			// no longer leased IFF control-gated. writeClassBrowserTools is
			// exactly the action-class roster.
			wantLeased := writeClassBrowserTools[name]
			require.Equal(t, wantLeased, defersUnderLease,
				"%s defers under the write lease = %v, want %v. A tool must take the lease IF AND "+
					"ONLY IF it is ACTION-class (FR-036): a tool leased but not action-class lets a "+
					"capture serialise behind an unrelated write; an action tool that is unleased "+
					"lets two turns interleave CDP commands on one page.",
				name, defersUnderLease, wantLeased)

			// What survives the FR-036 amendment from the original
			// biconditional: every leased tool is still control-gated
			// (action ⊆ gated), MINUS ADR-085 D-G's single named escape
			// hatch. What does not survive: the converse — a capture-class
			// tool is gated (defers under lock) without ever being leased.
			//
			// browser_open_tab is action-class (two turns must not interleave
			// tab creation on one browsing context, so it still takes the
			// lease) and is deliberately NOT control-gated: D-G's ruling is
			// that an agent whose wheel is held opens a NEW TAB rather than
			// waiting, and three separate prompts tell it so. Before the
			// carve-out it returned a deferral instead of a tab, making the
			// instruction unfollowable. The exception is read from
			// controlGateEscapeHatchTools rather than written out here, so a
			// second name added to that map cannot quietly acquire an
			// exemption this test never noticed.
			switch {
			case controlGateEscapeHatchTools[name]:
				require.False(t, defersUnderLock,
					"%s is in controlGateEscapeHatchTools, so the control gate must let it "+
						"through unconditionally (ADR-085 D-G) — a deferral here is the escape "+
						"hatch being shut on exactly the turn the agent needs it", name)
			case defersUnderLease:
				require.True(t, defersUnderLock,
					"%s takes the write lease but does not defer under the control lock — every "+
						"leased (action-class) tool must also be control-gated, unless it is a "+
						"declared D-G escape hatch", name)
			}

			if defersUnderLock {
				lockedCount++
			}
			if defersUnderLease {
				leasedCount++
			}
		})
	}

	// TWELVE defer under the control lock: NINE of the ten action-class tools
	// (navigate, click, type, evaluate, switch_tab, close_tab, select_option,
	// press_key, hover) PLUS ADR-085's three newly-gated capture-class tools
	// (screenshot, get_text, snapshot). The tenth action-class tool,
	// browser_open_tab, is action-class and therefore LEASED but is D-G's
	// escape hatch and therefore NOT control-gated — which is why the two
	// counts are 12 and 10 rather than both counting it. This is the one
	// place the two sets differ in that direction, and the switch inside the
	// loop is what holds it to exactly that one place.
	// FOUR registered tools defer under NEITHER gate, each for its own
	// reason: browser_list_tabs and browser_wait (genuinely read-only, no
	// ADR-085 exposure); browser_handle_dialog, exempt because it is the
	// recovery verb (D2 FR-035) and gating it behind the mechanisms the wedge
	// disables is a deadlock, not a safety property; and browser_handover
	// (ADR-085 D7 / BROWSER-FR-046), exempt because it is the tool that GIVES
	// the wheel away — gating it on who currently holds the wheel is circular,
	// so the agent could not hand over at the moment handing over is what it
	// needs to do. None of the four is action-class, so the leased count is
	// unchanged by handover's arrival.
	//
	// They are asserted so that a build in which BOTH gates stopped working
	// cannot pass the per-tool assertions above by agreeing on "never
	// defers".
	require.Equal(t, 12, lockedCount,
		"twelve registered tools defer under a held control lock: action ∪ capture (ADR-085 FR-036) "+
			"minus browser_open_tab, the one D-G escape hatch")
	require.Equal(t, 10, leasedCount, "ten registered tools are action-class and therefore leased")
}

// TestRegister_NoTakeControlTool is FR-070's structural half: ACQUISITION of the
// operator's shared tab is IMPLICIT and has NO surface. No take-control tool, no
// extra policy entry, no wire field. An agent acquires the tab by acting on
// it, and the control lock is the whole mitigation.
//
// FR-070 IS ABOUT ONE DIRECTION ONLY, and this test used to conflate the two.
// The word list below is a proxy for "a tool that TAKES the wheel from a human",
// and ADR-085 D7 (BROWSER-FR-046) adds browser_handover, which runs the other
// way: the agent GIVES the wheel to the operator, deliberately, on a step it
// should not perform itself. Nothing in FR-070 speaks to that direction — the
// D1.9b ruling it protects is about an agent helping itself to a tab a person
// is using, which browser_handover is the opposite of.
//
// So browser_handover is carved out by NAME and then held to the carve-out:
// the two assertions below prove it is a release surface rather than an
// acquisition one dressed in a friendlier word — it is on the exempt roster
// (it does not consult the control gate at all, because gating a hand-over on
// who holds the wheel is circular) and it is not action-class (it never takes
// the write lease and is never per-call audited as a page mutation). A tool
// that actually acquired the tab would fail both.
func TestRegister_NoTakeControlTool(t *testing.T) {
	registry := tools.NewToolRegistry()
	mgr := registerToolsForTestAs(t, registry, controlTestCfg(t),
		security.NewSSRFChecker(nil),
		func(m *BrowserManager) ManagerResolver { return newOperatorResolver(m) })
	t.Cleanup(mgr.Shutdown)

	forbidden := []string{"control", "acquire", "claim", "take_over", "takeover", "handover", "handoff"}
	for _, tool := range registry.GetAll() {
		if tool.Name() == "browser_handover" {
			continue // ADR-085 D7 — the release direction; held to that below.
		}
		name := strings.ToLower(tool.Name())
		for _, word := range forbidden {
			require.NotContains(t, name, word,
				"%s looks like an acquisition surface. FR-070: taking the operator's tab is implicit — "+
					"there is no tool, no policy entry and no wire field for it, and adding one would "+
					"need the D1.9b ruling reopened.", tool.Name())
		}
	}

	require.Contains(t, exemptBrowserTools, "browser_handover",
		"browser_handover is carved out of the FR-070 word list above on the grounds that it RELEASES "+
			"the wheel rather than taking it. A tool that consulted the control gate would be one that "+
			"can be refused while a human drives — i.e. one that competes for the wheel — so the "+
			"carve-out only holds while it is on the exempt roster")
	require.NotContains(t, writeClassBrowserTools, "browser_handover",
		"browser_handover is action-class, meaning it takes the write lease and is audited as a page "+
			"mutation. FR-070's concern is exactly a tool that seizes the shared tab; the carve-out "+
			"above is only defensible while handover mutates nothing on the page")

	require.Len(t, registry.GetAll(), 17,
		"the tool surface is seventeen registered tools: sixteen plus ADR-085 D7's browser_handover")
}

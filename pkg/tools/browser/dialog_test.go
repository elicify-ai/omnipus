package browser

// dialog_test.go — browser_handle_dialog and the pending-dialog bookkeeping.
//
// The acceptance question for a recovery verb is NOT "was the dialog
// dismissed". It is "does the tab answer CDP again afterwards" — that is what
// the wedge takes away, and what recovery has to give back. The tests are
// written that way: the reachability assertions are about the tool being
// REACHED at all in the states that block every other tool, and about exactly
// one CDP call being issued when two callers race.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newDialogToolForTest builds the tool over a manager the test controls,
// bypassing the registry: browser_handle_dialog's registration line has to land
// in the same commit as its tool-policy seed, which is another stream's, so the
// tool is not registered yet and cannot be fetched by name.
func newDialogToolForTest(t *testing.T, res ManagerResolver) *HandleDialogTool {
	t.Helper()
	return &HandleDialogTool{res: res}
}

// seedDialogOnActiveTab plants a pending dialog on a session's active tab
// exactly as the CDP listener would, without needing a page that calls
// alert().
func seedDialogOnActiveTab(t *testing.T, mgr *BrowserManager, sessionID string, dlg *PendingDialog) target.ID {
	t.Helper()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	se, ok := mgr.sessions[sessionID]
	if !ok || se == nil || se.active() == nil {
		t.Fatalf("test setup: session %q has no active tab", sessionID)
	}
	tid := se.active().targetID
	if se.pendingDialogs == nil {
		se.pendingDialogs = make(map[target.ID]*PendingDialog)
	}
	se.pendingDialogs[tid] = dlg
	return tid
}

// fakeSession installs a sessionEntry with one tab, with no Chrome behind it.
// Nothing in this file's assertions needs a live renderer: what is under test
// is the bookkeeping and the gating, and both are decided in Go.
func fakeSession(t *testing.T, mgr *BrowserManager, sessionID string) target.ID {
	t.Helper()
	// A REAL chromedp context, not context.Background(): ListenTarget panics
	// on a context it did not create. chromedp.NewContext allocates the
	// bookkeeping without dialing anything, so no Chrome starts here.
	ctx, cancel := chromedp.NewContext(context.Background())
	tid := target.ID("test-target-" + sessionID)
	// Drop the session before the manager's own Shutdown cleanup runs
	// (cleanups are LIFO, and this one is registered later). Otherwise
	// Shutdown cancels a chromedp context whose browser never started, which
	// never returns and costs cancelBounded's full 5s timeout per test.
	t.Cleanup(func() {
		mgr.mu.Lock()
		delete(mgr.sessions, sessionID)
		mgr.mu.Unlock()
		cancel()
	})
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.sessions[sessionID] = &sessionEntry{
		tabs:      []*tabEntry{{ctx: ctx, cancel: cancel, targetID: tid}},
		activeIdx: 0,
	}
	return tid
}

func newDialogManager(t *testing.T) *BrowserManager {
	t.Helper()
	registry, mgr := newPermissiveRegistry(t, controlTestCfg(t))
	_ = registry
	return mgr
}

// --- FR-035: the exemptions ---------------------------------------------

// TestDialog_RecoversWhileHumanControls — controlledResult defers every gated
// tool while a human holds the live view. A human looking at a wedged tab has
// no button to press, so if the dialog verb deferred too, the tab would be
// stuck for BOTH of them. It must not defer.
func TestDialog_RecoversWhileHumanControls(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testOperatorSessionID)

	if !mgr.Live().TakeControl(testOperatorSessionID, "human-viewer") {
		t.Fatal("test setup: taking control must succeed")
	}

	tool := newDialogToolForTest(t, newOperatorResolver(mgr))
	result := tool.Execute(context.Background(), map[string]any{"accept": false})

	if result == nil {
		t.Fatal("no result")
	}
	if strings.Contains(result.ForLLM, "human is currently controlling") {
		t.Fatalf("browser_handle_dialog must NOT be gated by controlledResult — it is the verb that "+
			"unwedges a tab, and a human holding the live view is exactly when it is needed. Got: %s",
			result.ForLLM)
	}
}

// TestDialog_RecoversWhileLeaseHeld — the click that RAISED the dialog is
// still running and still holds the write lease; that blockage IS the wedge.
// If the dialog verb took the lease it would defer for the whole wedge window,
// which is a deadlock, not a safety property.
//
// This is the test that actually covers FR-035's lease half.
// TestDialog_RecoversWhileCDPBlocked covers the other half and must not be
// read as evidence for this one.
func TestDialog_RecoversWhileLeaseHeld(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testOperatorSessionID)

	// Another turn holds the lease and has not released it.
	release, ok, _ := mgr.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "the-turn-that-clicked")
	if !ok {
		t.Fatal("test setup: the first acquire must succeed")
	}
	t.Cleanup(release)

	// Sanity: a LEASED tool really would defer in this state, so the assertion
	// below is about the dialog tool and not about a lease that is not held.
	deferred, rel := leaseWrite(context.Background(), mgr, testKey, TabOwnerWorkspace(), "another-agent", "browser_click")
	rel()
	if deferred == nil {
		t.Fatal("test setup: a leased tool must defer while the lease is held, or this test proves nothing")
	}

	tool := newDialogToolForTest(t, newOperatorResolver(mgr))
	result := tool.Execute(context.Background(), map[string]any{"accept": false})

	if result == nil {
		t.Fatal("no result")
	}
	if strings.Contains(result.ForLLM, "is currently driving this browser tab") {
		t.Fatalf("browser_handle_dialog must NOT take the write lease: the turn that raised the dialog "+
			"still holds it, and waiting for that turn to finish is waiting for the wedge to clear "+
			"itself. Got: %s", result.ForLLM)
	}
}

// TestDialog_RecoversWhileCDPBlocked — the CDP half. With the tab wedged,
// every other tool's route into it is dead; the dialog tool still reaches its
// own bookkeeping and answers, because clearing the map entry happens in Go
// before any CDP is attempted.
func TestDialog_RecoversWhileCDPBlocked(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{
		Type: "confirm", Message: "Leave site?", OpenedAt: time.Now(),
	})

	var calls int
	restore := swapDialogSeam(t, func(context.Context, bool, string, bool) error {
		calls++
		return nil
	})
	defer restore()

	tool := newDialogToolForTest(t, newFixedResolver(mgr))
	result := tool.Execute(context.Background(), map[string]any{"accept": false})

	if result.IsError {
		t.Fatalf("dismissing a dialog on a wedged tab must succeed; got: %s", result.ForLLM)
	}
	if calls != 1 {
		t.Fatalf("exactly one Page.handleJavaScriptDialog, got %d", calls)
	}
	// And the tab is no longer recorded as blocked — the state every other
	// tool assumes has been restored.
	if dlg := mgr.PendingDialogOn(testSessionID); dlg != nil {
		t.Fatalf("the pending-dialog entry must be gone after a handle; still holds %+v", dlg)
	}
}

// --- The concurrency invariant, asserted AT THE SEAM ---------------------

// TestDialog_ConcurrentHandlesIssueOneCDPCall — two goroutines, one open
// dialog, exactly one CDP call.
//
// This has to be asserted at the seam and concurrently. A sequential
// double-call passes just as happily with the map entry cleared AFTER the CDP
// call — which is the placement the invariant exists to forbid — and CDP
// errors on a second handle against a closed dialog.
func TestDialog_ConcurrentHandlesIssueOneCDPCall(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{Type: "alert", Message: "hi"})

	var mu sync.Mutex
	calls := 0
	restore := swapDialogSeam(t, func(context.Context, bool, string, bool) error {
		mu.Lock()
		calls++
		mu.Unlock()
		// Hold long enough that a racing caller reaches the seam too, if the
		// clear were placed on the wrong side of it.
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	defer restore()

	tool := newDialogToolForTest(t, newFixedResolver(mgr))

	var wg sync.WaitGroup
	results := make([]*tools.ToolResult, 2)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = tool.Execute(context.Background(), map[string]any{"accept": false})
		}(i)
	}
	close(start)
	wg.Wait()

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("two concurrent handles against ONE open dialog issued %d Page.handleJavaScriptDialog "+
			"calls, want exactly 1. The map entry must be cleared under m.mu BEFORE the CDP call, not after.", got)
	}
	// Neither errored, and the loser reported no dialog.
	losers := 0
	for i, r := range results {
		if r == nil {
			t.Fatalf("goroutine %d got no result", i)
		}
		if r.IsError {
			t.Errorf("goroutine %d errored: %s", i, r.ForLLM)
		}
		if strings.Contains(r.ForLLM, `"dialog": null`) || strings.Contains(r.ForLLM, `"dialog":null`) {
			losers++
		}
	}
	if losers != 1 {
		t.Errorf("exactly one caller must lose the race and see {\"dialog\": null}; %d did", losers)
	}
}

// TestDialog_NoDialogReturnsNullTwice — agents retry. A second call after the
// dialog is gone is a normal thing to do, must not error, and must not fire a
// second CDP call at a dialog that is already closed.
func TestDialog_NoDialogReturnsNullTwice(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)

	calls := 0
	restore := swapDialogSeam(t, func(context.Context, bool, string, bool) error {
		calls++
		return nil
	})
	defer restore()

	tool := newDialogToolForTest(t, newFixedResolver(mgr))
	for i := 0; i < 2; i++ {
		result := tool.Execute(context.Background(), map[string]any{"accept": false})
		if result.IsError {
			t.Fatalf("call %d: asking whether a dialog is blocking is a legitimate question, not an error; got: %s", i, result.ForLLM)
		}
		if !strings.Contains(strings.ReplaceAll(result.ForLLM, " ", ""), `"dialog":null`) {
			t.Fatalf("call %d: want {\"dialog\": null}; got %s", i, result.ForLLM)
		}
	}
	if calls != 0 {
		t.Fatalf("no CDP call may be issued when nothing is pending; got %d", calls)
	}
}

func TestDialog_PromptTextDelivered(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{
		Type: string(page.DialogTypePrompt), Message: "Your name?", DefaultPrompt: "anon",
	})

	var gotText string
	var gotIsPrompt bool
	restore := swapDialogSeam(t, func(_ context.Context, _ bool, promptText string, isPrompt bool) error {
		gotText, gotIsPrompt = promptText, isPrompt
		return nil
	})
	defer restore()

	tool := newDialogToolForTest(t, newFixedResolver(mgr))
	result := tool.Execute(context.Background(), map[string]any{"accept": true, "prompt_text": "Ada"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !gotIsPrompt {
		t.Error("a prompt() dialog must be answered with WithPromptText; the tool did not flag it as a prompt")
	}
	if gotText != "Ada" {
		t.Errorf("prompt_text reached CDP as %q, want %q", gotText, "Ada")
	}
}

// --- FR-041: the argument-level guard -----------------------------------

// TestDialog_AcceptRefusedWhenNoApprover — accepting a dialog agrees, on the
// user's behalf, to whatever the page asked. On a run with nobody to approve
// anything, that is refused.
func TestDialog_AcceptRefusedWhenNoApprover(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{Type: "confirm", Message: "Delete everything?"})

	calls := 0
	restore := swapDialogSeam(t, func(context.Context, bool, string, bool) error { calls++; return nil })
	defer restore()

	ctx := tools.WithAutoDenyAsk(context.Background(), true)
	tool := newDialogToolForTest(t, newFixedResolver(mgr))
	result := tool.Execute(ctx, map[string]any{"accept": true})

	if !result.IsError {
		t.Fatalf("accept:true on an unattended run must be refused; got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "accept:false") {
		t.Errorf("the refusal must point at the thing that DOES work — {accept:false} — or the agent is "+
			"left with a wedged tab and no move. Got: %s", result.ForLLM)
	}
	if calls != 0 {
		t.Errorf("a refused accept must issue no CDP call; got %d", calls)
	}
}

// TestDialog_DismissAlwaysAllowed — the other half, and S-65's point: a test
// that only asserted the refusal would pass over an implementation that had
// broken the unwedge path entirely. FR-041 must not be satisfiable by breaking
// browser_handle_dialog.
func TestDialog_DismissAlwaysAllowed(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{Type: "confirm", Message: "Delete everything?"})

	var acceptSeen bool
	calls := 0
	restore := swapDialogSeam(t, func(_ context.Context, accept bool, _ string, _ bool) error {
		calls++
		acceptSeen = accept
		return nil
	})
	defer restore()

	ctx := tools.WithAutoDenyAsk(context.Background(), true)
	tool := newDialogToolForTest(t, newFixedResolver(mgr))
	result := tool.Execute(ctx, map[string]any{"accept": false})

	if result.IsError {
		t.Fatalf("dismissing is never refused, in any state; got: %s", result.ForLLM)
	}
	if calls != 1 || acceptSeen {
		t.Fatalf("want exactly one dismiss (accept=false); calls=%d accept=%v", calls, acceptSeen)
	}
}

// TestDialog_AcceptDefaultsToDismiss — `accept` omitted means DISMISS.
// Dismissing unwedges the tab for every dialog type; accepting is the
// consequential one, so it must be asked for explicitly.
func TestDialog_AcceptDefaultsToDismiss(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{Type: "confirm", Message: "Are you sure?"})

	var acceptSeen bool
	restore := swapDialogSeam(t, func(_ context.Context, accept bool, _ string, _ bool) error {
		acceptSeen = accept
		return nil
	})
	defer restore()

	tool := newDialogToolForTest(t, newFixedResolver(mgr))
	if result := tool.Execute(context.Background(), map[string]any{}); result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if acceptSeen {
		t.Error("omitting `accept` must DISMISS, not accept — accepting a confirm() is a decision the page attributes to the user")
	}
}

// TestDialog_AcceptAllowedWhenApproverPresent, and FR-044's dialog half: with
// a human holding the live view on an ATTENDED turn, accept SUCCEEDS. The
// exemption is from the human-control gate, never from FR-041. This exposure
// is pinned so narrowing it later has to be a deliberate edit.
func TestDialog_AcceptSucceedsWhileHumanControls_AttendedTurn(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testOperatorSessionID)
	seedDialogOnActiveTab(t, mgr, testOperatorSessionID, &PendingDialog{Type: "confirm", Message: "Proceed?"})

	if !mgr.Live().TakeControl(testOperatorSessionID, "human-viewer") {
		t.Fatal("test setup: taking control must succeed")
	}

	var acceptSeen bool
	restore := swapDialogSeam(t, func(_ context.Context, accept bool, _ string, _ bool) error {
		acceptSeen = accept
		return nil
	})
	defer restore()

	// Attended: AutoDenyAsk unset.
	tool := newDialogToolForTest(t, newOperatorResolver(mgr))
	result := tool.Execute(context.Background(), map[string]any{"accept": true})

	if result.IsError {
		t.Fatalf("an attended turn may accept a dialog even while a human holds the live view; got: %s", result.ForLLM)
	}
	if !acceptSeen {
		t.Error("accept:true on an attended turn must reach CDP as an accept")
	}
}

// --- FR-014: the listener, and its idempotence key ----------------------

// TestDialog_ListenerReArmedExactlyOnceAfterCtxRecreation — chromedp
// ListenTarget is an APPEND. Calling install twice on one ctx stacks two
// handlers and every dialog is then recorded twice; the map key is what makes
// calling it at every tab-creation site safe.
func TestDialog_ListenerReArmedExactlyOnceAfterCtxRecreation(t *testing.T) {
	mgr := newDialogManager(t)
	tid := fakeSession(t, mgr, testSessionID)

	mgr.mu.Lock()
	se := mgr.sessions[testSessionID]
	for i := 0; i < 5; i++ {
		mgr.syncDialogListenersLocked(testSessionID, se)
	}
	got := len(se.dialogListeners)
	_, armed := se.dialogListeners[tid]
	mgr.mu.Unlock()

	if got != 1 || !armed {
		t.Fatalf("five sync calls left %d listener keys (armed for the tab: %v); the key exists precisely "+
			"so calling install at every tab-creation site is safe", got, armed)
	}
}

// TestDialog_OnNonZeroTab_StillDetected — the structural point the ADR names.
// Target DISCOVERY is browser-global, so one listener on tab 0 is right for
// it. A JavaScript dialog is per-target: a dialog on tab 2 with a tab-0-only
// listener is invisible, and the tab is wedged with nothing recording why.
func TestDialog_OnNonZeroTab_StillDetected(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)

	// A second tab, made active.
	ctx2, cancel2 := chromedp.NewContext(context.Background())
	t.Cleanup(cancel2)
	tid2 := target.ID("test-target-second")
	mgr.mu.Lock()
	se := mgr.sessions[testSessionID]
	se.tabs = append(se.tabs, &tabEntry{ctx: ctx2, cancel: cancel2, targetID: tid2})
	se.activeIdx = 1
	mgr.syncDialogListenersLocked(testSessionID, se)
	armedOnTab1 := len(se.dialogListeners)
	mgr.mu.Unlock()

	if armedOnTab1 != 2 {
		t.Fatalf("every tab needs its own dialog listener; %d armed across 2 tabs", armedOnTab1)
	}

	// A dialog raised on tab 1 is recorded and reachable.
	mgr.recordPendingDialog(testSessionID, tid2, &page.EventJavascriptDialogOpening{
		Type: page.DialogTypeAlert, Message: "on tab 1", URL: "http://example.test/",
	})
	if dlg := mgr.PendingDialogOn(testSessionID); dlg == nil || dlg.Message != "on tab 1" {
		t.Fatalf("a dialog on a non-zero tab must be detected; got %+v", dlg)
	}
}

// TestDialog_StateEvictedWhenTabGoes — a stale target id surviving a teardown
// makes the re-arm a silent no-op, and the original wedge comes back with no
// test failing anywhere obvious. So the eviction is asserted directly.
func TestDialog_StateEvictedWhenTabGoes(t *testing.T) {
	mgr := newDialogManager(t)
	tid := fakeSession(t, mgr, testSessionID)

	mgr.mu.Lock()
	se := mgr.sessions[testSessionID]
	mgr.syncDialogListenersLocked(testSessionID, se)
	mgr.mu.Unlock()

	mgr.recordPendingDialog(testSessionID, tid, &page.EventJavascriptDialogOpening{Type: page.DialogTypeAlert})
	mgr.NoteActivation(testSessionID, "a click")

	// The tab goes; a REPLACEMENT arrives with a different target id, exactly
	// as a ctx recreation produces.
	ctx2, cancel2 := chromedp.NewContext(context.Background())
	t.Cleanup(cancel2)
	newTID := target.ID("test-target-recreated")
	mgr.mu.Lock()
	se.tabs = []*tabEntry{{ctx: ctx2, cancel: cancel2, targetID: newTID}}
	se.activeIdx = 0
	mgr.syncDialogListenersLocked(testSessionID, se)
	_, staleListener := se.dialogListeners[tid]
	_, staleDialog := se.pendingDialogs[tid]
	_, staleActivation := se.lastActivation[tid]
	_, freshListener := se.dialogListeners[newTID]
	mgr.mu.Unlock()

	if staleListener || staleDialog || staleActivation {
		t.Errorf("all three pieces of per-tab dialog state must die with the tab; listener=%v dialog=%v activation=%v",
			staleListener, staleDialog, staleActivation)
	}
	if !freshListener {
		t.Error("the replacement tab must get its own listener, or the wedge returns silently")
	}
}

// --- FR-013: the timeout message ----------------------------------------

// TestDialog_UnhandledDialogNamedInTimeout — when a dialog IS recorded, the
// timeout says which one and names the verb that clears it.
func TestDialog_UnhandledDialogNamedInTimeout(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{Type: "confirm", Message: "Leave site?"})

	err, ok := dialogAwareTimeout(mgr, testSessionID, "browser_click", context.DeadlineExceeded)
	if !ok || err == nil {
		t.Fatal("want a rewritten error")
	}
	msg := err.Error()
	for _, want := range []string{"browser_handle_dialog", "confirm", "Leave site?"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the timeout must name %q; got %q", want, msg)
		}
	}
	if strings.Contains(msg, "may have an open dialog") {
		t.Errorf("a CONFIRMED dialog must not be reported with the hedged wording; got %q", msg)
	}
}

// TestDialog_PreListenerDialogIsSuspected — a dialog opened before its
// listener existed is undetectable: Page.javascriptDialogOpening is an event,
// not queryable state, and there is no Page.getPendingDialog. So a timeout
// with an EMPTY map still names the recovery verb, hedged.
//
// The predicate is deliberately wide — any CDP timeout on a tab with no
// recorded dialog. The narrow version ("only if this tab recently completed a
// command") is false in the motivating case: a tab adopted after the dialog
// opened has no completed command of its own.
func TestDialog_PreListenerDialogIsSuspected(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	// No dialog recorded, and no activation either — the adopted-tab case.

	err, _ := dialogAwareTimeout(mgr, testSessionID, "browser_click", context.DeadlineExceeded)
	msg := err.Error()

	if !strings.Contains(msg, "browser_handle_dialog") {
		t.Errorf("even a SUSPECTED dialog must name the recovery verb, or the requirement buys nothing; got %q", msg)
	}
	if !strings.Contains(msg, "may have an open dialog") {
		t.Errorf("the suspected wording must be hedged — it is a guess; got %q", msg)
	}
	if strings.Contains(msg, "context deadline exceeded") {
		t.Errorf("the bare timeout is exactly what this replaces; got %q", msg)
	}

	// The two messages must be textually distinct, or an operator reading a
	// log cannot tell a known dialog from a guess.
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{Type: "alert", Message: "x"})
	confirmedErr, _ := dialogAwareTimeout(mgr, testSessionID, "browser_click", context.DeadlineExceeded)
	confirmed := confirmedErr.Error()
	if confirmed == msg {
		t.Error("the confirmed and suspected messages must differ")
	}
}

// lastActivation sharpens the wording and NOTHING branches on it.
func TestDialog_SuspectedMessageNamesTheLastAction(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	mgr.NoteActivation(testSessionID, "a click")

	routed, _ := dialogAwareTimeout(mgr, testSessionID, "browser_click", context.DeadlineExceeded)
	msg := routed.Error()
	if !strings.Contains(msg, "after a click") {
		t.Errorf("when the last action IS known the message should say so; got %q", msg)
	}
}

// A non-timeout error passes through untouched — a real, specific failure is
// more useful than a dialog guess laid over it.
func TestDialog_NonTimeoutErrorsPassThrough(t *testing.T) {
	mgr := newDialogManager(t)
	fakeSession(t, mgr, testSessionID)
	seedDialogOnActiveTab(t, mgr, testSessionID, &PendingDialog{Type: "alert"})

	if _, ok := dialogAwareTimeout(mgr, testSessionID, "browser_click", context.Canceled); ok {
		t.Error("a non-timeout error must not be rewritten as a dialog guess — a real, specific failure is more useful than one laid over it")
	}
}

// --- Structural ----------------------------------------------------------

// TestDialog_ExecuteTakesNeitherGate reads the source. Both exemptions are
// invisible at runtime in the happy path — a tool that DID call
// controlledResult and leaseWrite would still work whenever neither was
// engaged — so the absence is asserted where it lives.
func TestDialog_ExecuteTakesNeitherGate(t *testing.T) {
	src := readSourceForTest(t, "tools_dialog.go")
	for _, banned := range []string{"controlledResult(", "leaseWrite("} {
		if strings.Contains(src, banned) {
			t.Errorf("browser_handle_dialog must not call %s — it is a recovery verb, and gating it behind "+
				"the mechanisms the fault disables is a deadlock (FR-035)", banned)
		}
	}
}

func TestDialog_DeclaresScopeCore(t *testing.T) {
	// A wrong scope COMPILES and is then denied fail-closed before the policy
	// merge, with no log line — so it is asserted, not assumed.
	if got := (&HandleDialogTool{}).Scope(); got != tools.ScopeCore {
		t.Errorf("Scope() = %v, want %v", got, tools.ScopeCore)
	}
}

// swapDialogSeam replaces the CDP seam for the duration of a test.
func swapDialogSeam(t *testing.T, fn func(context.Context, bool, string, bool) error) func() {
	t.Helper()
	prev := handleJavaScriptDialogFn
	handleJavaScriptDialogFn = fn
	restore := func() { handleJavaScriptDialogFn = prev }
	t.Cleanup(restore)
	return restore
}

// --- The dependency pin --------------------------------------------------

// TestChromedpEnablesPageDomainPerTarget pins a behaviour of chromedp that
// this whole feature rests on and that we do not control.
//
// Page.javascriptDialogOpening only fires if the Page domain is enabled on
// that target. We never call page.Enable() anywhere — chromedp does it for us,
// in the action list it runs when it attaches to any non-worker target. If a
// chromedp bump changed that list, every dialog would silently stop being
// recorded: no error, no failing test anywhere obvious, and the wedge back in
// full. So it is asserted rather than trusted.
//
// The test also incidentally confirms the other half: chromedp lists
// EventJavascriptDialogOpening among the events it explicitly IGNORES, so it
// does not answer dialogs itself and the page really does stall.
func TestChromedpEnablesPageDomainPerTarget(t *testing.T) {
	skipIfNoBrowser(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
		  <button id="go" onclick="alert('wedge me')">Go</button>
		</body></html>`))
	}))
	t.Cleanup(srv.Close)

	registry, mgr := newPermissiveRegistry(t, testBrowserCfg(t))
	nav := mustGetTool(t, registry, "browser_navigate")
	if res := nav.Execute(context.Background(), map[string]any{"url": srv.URL}); res.IsError {
		t.Fatalf("navigate: %s", res.ForLLM)
	}

	tabCtx, err := mgr.Session(testSessionID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	seen := make(chan *page.EventJavascriptDialogOpening, 1)
	chromedp.ListenTarget(tabCtx, func(ev any) {
		if e, ok := ev.(*page.EventJavascriptDialogOpening); ok {
			select {
			case seen <- e:
			default:
			}
		}
	})

	// Fire the alert WITHOUT waiting for the click to return: an open dialog
	// blocks the tab, so the click never completes. That blocking IS the
	// wedge; observing the event is the point.
	go func() {
		_ = chromedp.Run(tabCtx, chromedp.Click("#go", chromedp.ByQuery))
	}()

	select {
	case e := <-seen:
		if e.Message != "wedge me" {
			t.Errorf("dialog message = %q, want %q", e.Message, "wedge me")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no Page.javascriptDialogOpening observed. chromedp enables the Page domain per target " +
			"in its own attach action list and we rely on that; if a bump removed it, every dialog would " +
			"silently stop being recorded and the wedge would come back with nothing failing.")
	}

	// Unwedge, so the deferred cleanups are not fighting a blocked renderer.
	_ = chromedp.Run(tabCtx, page.HandleJavaScriptDialog(false))
}

// TestDialog_IsActuallyRegistered — green tests on an unreachable tool are
// the failure mode this project has shipped before. browser_handle_dialog
// exists, it is in the catalog, and RegisterTools puts it in the registry
// under its own name; a tool an agent cannot invoke is not done.
func TestDialog_IsActuallyRegistered(t *testing.T) {
	registry, _ := newPermissiveRegistry(t, controlTestCfg(t))

	tool, ok := registry.Get("browser_handle_dialog")
	if !ok || tool == nil {
		t.Fatal("browser_handle_dialog is not in the tool registry — implemented, seeded, and unreachable")
	}

	// And in the metadata catalog, which is what the tool-policy coverage
	// validator and the manifest tier partition are both fed from. A name in
	// the registry but not the catalog resolves a silent deny.
	var inCatalog bool
	for _, m := range BrowserBuiltinMetadata() {
		if m.Name() == "browser_handle_dialog" {
			inCatalog = true
			break
		}
	}
	if !inCatalog {
		t.Error("browser_handle_dialog is missing from BrowserBuiltinMetadata()")
	}
}

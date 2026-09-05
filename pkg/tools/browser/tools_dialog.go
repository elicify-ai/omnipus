package browser

// tools_dialog.go — browser_handle_dialog, and the per-tab dialog bookkeeping
// it reads (ADR-075 D2, spec §3 Stream C).
//
// The failure this exists to end: a page calls alert(), confirm() or prompt(),
// Chrome blocks the tab's execution waiting for an answer, and every
// subsequent CDP command on that target hangs until it times out. Before this,
// nothing recorded that a dialog had opened and no verb could clear one, so
// the tab stayed wedged for the rest of the session and every tool call
// against it returned "context deadline exceeded".
//
// Two design points that look like oversights and are not:
//
//  1. browser_handle_dialog calls NEITHER controlledResult NOR the write
//     lease. It is a RECOVERY verb, not a write. The click that raised the
//     dialog is still running — that blockage IS the wedge — and it holds the
//     lease; and controlledResult defers whenever a human holds the live view,
//     while a human looking at a wedged tab has no button to press. Gating the
//     one verb that unwedges a tab behind the two mechanisms the fault itself
//     disables is a deadlock, not a safety property.
//
//  2. Dialogs are never auto-dismissed. Silently accepting an onbeforeunload
//     confirm is indistinguishable from a click the agent did not make. The
//     tool is explicit; only the POINTER to it is automatic.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// dialogHandleTimeout bounds the single CDP call this tool makes. It is short
// on purpose: Page.handleJavaScriptDialog is answered by the browser process,
// not by the wedged renderer, so it does not queue behind the dialog.
const dialogHandleTimeout = 5 * time.Second

// handleJavaScriptDialogFn is the CDP seam, swappable so a test can assert
// HOW MANY TIMES it was called rather than only what the tool returned.
//
// That distinction is the whole test. The "clear the map entry before the CDP
// call" invariant exists so two concurrent handles issue exactly ONE
// Page.handleJavaScriptDialog; a SEQUENTIAL test passes just as happily with
// the clear placed after the call — the placement the invariant forbids — so
// the assertion has to be made here, at the seam.
var handleJavaScriptDialogFn = func(ctx context.Context, accept bool, promptText string, isPrompt bool) error {
	p := page.HandleJavaScriptDialog(accept)
	if isPrompt && promptText != "" {
		p = p.WithPromptText(promptText)
	}
	return chromedp.Run(ctx, p)
}

// --- browser_handle_dialog ---

type HandleDialogTool struct {
	tools.BaseTool
	browserAudit
	res ManagerResolver
}

func (t *HandleDialogTool) Name() string                 { return "browser_handle_dialog" }
func (t *HandleDialogTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *HandleDialogTool) Category() tools.ToolCategory { return tools.CategoryBrowser }

func (t *HandleDialogTool) Description() string {
	return "Answer a JavaScript dialog (alert / confirm / prompt / onbeforeunload) that is blocking a " +
		"tab. A page that opens one freezes every other browser tool on that tab until it is answered, " +
		"so if another call reports that the tab stopped answering, call this. By default it DISMISSES " +
		"the dialog (accept=false), which unwedges the tab for every dialog type and is what you want " +
		"unless you specifically mean to agree to something — accepting a confirm() is a decision the " +
		"page attributes to you. Pass prompt_text to fill in a prompt(). Calling it when no dialog is " +
		"open is not an error: it returns {\"dialog\": null}, which makes it safe to use as a check. " +
		"It acts on the active tab of the browsing context. " +
		"INTERIM: this tool acts on the workspace browser, which is shared with the operator and " +
		"carries their live logins."
}

func (t *HandleDialogTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"accept": map[string]any{
				"type": "boolean",
				"description": "true to ACCEPT the dialog (press OK), false to dismiss it (press Cancel). " +
					"Defaults to false. Dismissing unwedges the tab in every case; accepting is the " +
					"consequential one.",
			},
			"prompt_text": map[string]any{
				"type":        "string",
				"description": "Text to enter into a prompt() dialog. Ignored for alert, confirm and onbeforeunload.",
			},
		},
	}
}

func (t *HandleDialogTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	// `accept` defaults to FALSE when omitted: dismiss, not accept.
	accept, _ := args["accept"].(bool)
	promptText, _ := args["prompt_text"].(string)

	mgr, key, _, owner, sid, failure := resolveTurn(ctx, t.res, &t.browserAudit, t.Name())
	if failure != nil {
		return failure
	}
	// FR-051: this call is now IN FLIGHT against the workspace's browser, and
	// stays so until Execute returns. The pool reads this before evicting or
	// idle-closing, so that killing a Chrome never turns a running call into
	// an inexplicable error inside somebody's turn. Every browser tool
	// increments it — read-only ones too, because a screenshot that returns
	// "connection lost" mid-turn is no less confusing for having been
	// read-only. The defer is what makes a panicking or cancelled call
	// release; a leaked count is a browser that can never be reclaimed.
	defer mgr.EnterCall()()

	// FR-035: NO controlledResult and NO leaseWrite here, deliberately. See
	// this file's header. Execution goes straight from turn resolution to the
	// session.
	//
	// FR-041 is the compensating control, and it is argument-level rather
	// than policy-level because a tool policy cannot see an argument. On a
	// turn with nobody to approve anything, ACCEPTING a dialog — agreeing, on
	// the user's behalf, to whatever the page asked — is refused. Dismissing
	// is never refused in any state: refusing it would put the deadlock back.
	if accept && tools.ToolAutoDenyAsk(ctx) {
		return tools.ErrorResult("browser_handle_dialog: accepting a dialog agrees to whatever the page " +
			"asked, and this run has nobody to approve that. Call browser_handle_dialog{accept:false} to " +
			"dismiss it instead — that unwedges the tab just as well.")
	}

	// No recordBrowserAction: the FR-027 action trail is write-class only,
	// and audit.go refuses this tool by name. Answering a dialog changes no
	// page state — it releases a blocked execution context — so recording it
	// as an action would be recording something that did not happen.
	_, _ = key, owner

	tabCtx, dlg, err := mgr.TakePendingDialog(sid)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_handle_dialog: %s", err))
	}
	if dlg == nil {
		// Not an error. "Is a dialog blocking this tab?" is a legitimate
		// question, and an agent that retries after the dialog is already
		// gone must not be told it did something wrong — nor must a second
		// Page.handleJavaScriptDialog be fired at a closed dialog, which CDP
		// errors on.
		return jsonResult(map[string]any{"dialog": nil})
	}

	callCtx, cancel := context.WithTimeout(tabCtx, dialogHandleTimeout)
	defer cancel()

	if err := handleJavaScriptDialogFn(callCtx, accept, promptText, dlg.Type == string(page.DialogTypePrompt)); err != nil {
		return tools.ErrorResult(fmt.Sprintf("browser_handle_dialog: answering %s failed: %s", dlg.Summary(), err))
	}

	answered := map[string]any{
		"type":    dlg.Type,
		"message": dlg.Message,
		"url":     dlg.URL,
	}
	if dlg.DefaultPrompt != "" {
		answered["default_prompt"] = dlg.DefaultPrompt
	}
	return jsonResult(map[string]any{"dialog": answered, "accepted": accept})
}

// ---------------------------------------------------------------------------
// Manager-side bookkeeping
// ---------------------------------------------------------------------------

// syncDialogListenersLocked arms a Page.javascriptDialogOpening listener on
// every tab of se that does not already have one, and evicts the state of
// every tab that has gone.
//
// One idempotent primitive rather than four bespoke edits, called from every
// site that adds or removes a tab. That matters more than it looks: a stale
// target id surviving a teardown makes the re-arm a silent no-op — the map
// says "already listening" for a ctx that died — and the original wedge comes
// back with no test failing anywhere obvious.
//
// Must be called with m.mu held. chromedp.ListenTarget is a lock-free append,
// never a CDP round trip, so this is safe under the lock — the same reasoning
// installTargetListenerLocked already records.
func (m *BrowserManager) syncDialogListenersLocked(sessionID string, se *sessionEntry) {
	if se == nil {
		return
	}
	live := make(map[target.ID]struct{}, len(se.tabs))
	for _, te := range se.tabs {
		if te != nil {
			live[te.targetID] = struct{}{}
		}
	}

	// Evict everything belonging to a tab that no longer exists.
	for id := range se.dialogListeners {
		if _, ok := live[id]; !ok {
			delete(se.dialogListeners, id)
			delete(se.pendingDialogs, id)
			delete(se.lastActivation, id)
		}
	}

	for _, te := range se.tabs {
		if te == nil || te.ctx == nil || te.targetID == "" {
			continue
		}
		if se.dialogListeners == nil {
			se.dialogListeners = make(map[target.ID]struct{})
		}
		if _, ok := se.dialogListeners[te.targetID]; ok {
			continue
		}
		// chromedp.ListenTarget PANICS on a context it did not create. Every
		// production tab ctx is one of its own, but this runs under m.mu —
		// so a panic here would leave the manager's lock held and hang the
		// next Shutdown for as long as anyone waits. Checked, not assumed.
		if chromedp.FromContext(te.ctx) == nil {
			continue
		}
		se.dialogListeners[te.targetID] = struct{}{}
		tid := te.targetID
		chromedp.ListenTarget(te.ctx, func(ev any) {
			if e, ok := ev.(*page.EventJavascriptDialogOpening); ok {
				m.recordPendingDialog(sessionID, tid, e)
			}
		})
	}
}

// recordPendingDialog is the listener callback. It runs on chromedp's event
// dispatch goroutine, so it takes m.mu and does nothing else — no CDP call,
// no blocking work, exactly the discipline handleTargetEvent documents.
func (m *BrowserManager) recordPendingDialog(sessionID string, tid target.ID, e *page.EventJavascriptDialogOpening) {
	if e == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[sessionID]
	if !ok || se == nil {
		return
	}
	if se.pendingDialogs == nil {
		se.pendingDialogs = make(map[target.ID]*PendingDialog)
	}
	se.pendingDialogs[tid] = &PendingDialog{
		Type:          string(e.Type),
		Message:       e.Message,
		URL:           e.URL,
		DefaultPrompt: e.DefaultPrompt,
		OpenedAt:      time.Now(),
	}
}

// TakePendingDialog removes and returns the dialog blocking the session's
// ACTIVE tab, together with that tab's ctx, and reports (nil, nil) when there
// is none.
//
// The removal happens under m.mu and BEFORE the caller issues any CDP. That
// ordering is the whole concurrency contract: two goroutines racing on one
// open dialog means exactly one of them gets a non-nil dialog back and
// therefore exactly one Page.handleJavaScriptDialog is issued. Clearing after
// the CDP call instead would let both fire, and the second errors against a
// dialog that is already closed.
//
// browser_navigate's own onbeforeunload case is why this targets the ACTIVE
// tab rather than "the tab navigate returned": a confirm raised DURING a
// navigate wedges before navigate has a tab handle to give anyone. Navigate's
// target is by construction the active one, so the recovery verb reaches it.
func (m *BrowserManager) TakePendingDialog(sessionID string) (context.Context, *PendingDialog, error) {
	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok || se == nil {
		m.mu.Unlock()
		return nil, nil, ErrNoBrowsingContext
	}
	te := se.active()
	if te == nil {
		m.mu.Unlock()
		return nil, nil, ErrNoBrowsingContext
	}
	dlg := se.pendingDialogs[te.targetID]
	if dlg != nil {
		delete(se.pendingDialogs, te.targetID)
	}
	tabCtx := te.ctx
	m.mu.Unlock()
	return tabCtx, dlg, nil
}

// PendingDialogOn reports the dialog recorded against the session's active
// tab WITHOUT removing it — the read every other tool does when it times out.
func (m *BrowserManager) PendingDialogOn(sessionID string) *PendingDialog {
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[sessionID]
	if !ok || se == nil {
		return nil
	}
	te := se.active()
	if te == nil {
		return nil
	}
	return se.pendingDialogs[te.targetID]
}

// NoteActivation records that a tool just completed an action on the session's
// active tab. Advisory wording only — nothing branches on it.
func (m *BrowserManager) NoteActivation(sessionID, action string) {
	if action == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[sessionID]
	if !ok || se == nil {
		return
	}
	te := se.active()
	if te == nil {
		return
	}
	if se.lastActivation == nil {
		se.lastActivation = make(map[target.ID]string)
	}
	se.lastActivation[te.targetID] = action
}

func (m *BrowserManager) lastActivationOn(sessionID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	se, ok := m.sessions[sessionID]
	if !ok || se == nil {
		return ""
	}
	te := se.active()
	if te == nil {
		return ""
	}
	return se.lastActivation[te.targetID]
}

// ---------------------------------------------------------------------------
// FR-013 — the timeout error every other browser tool returns
// ---------------------------------------------------------------------------

// dialogAwareTimeout rewrites a CDP timeout into something an agent can act
// on. Two distinct messages, never one:
//
//   - A dialog IS recorded: say which one, and name the verb that clears it.
//   - No dialog is recorded: say so as a SUSPICION, hedged, and still name the
//     verb. A dialog opened before its listener existed is undetectable —
//     Page.javascriptDialogOpening is an event, not queryable state, and there
//     is no Page.getPendingDialog — so a tab adopted or re-attached after one
//     opened has an empty map and would otherwise get the bare timeout this
//     whole mechanism exists to replace.
//
// The predicate for the second case is deliberately WIDE: any CDP timeout on a
// tab with no recorded dialog. No "did this tab do something recently" test
// gates it, because the motivating case — a tab adopted after the dialog
// opened — has no completed command of its own, so a narrow predicate declines
// to fire in exactly the situation it was written for. A false positive costs
// one hedged sentence in an error the agent was already getting; a false
// negative costs the entire requirement.
//
// OBSERVED 2026-09-02, and the reason the suspected-case wording names two
// outcomes instead of one: three tools took this branch on a static fixture
// page that had no dialog at all and could not have had one.
//
// State the cause exactly, because I first recorded it here as "a renderer
// died under parallel load" and that was WRONG. The tab was alive and the page
// had not changed. resolveTarget was calling DOM.getDocument, which RESETS the
// DevTools DOM agent node-id map; chromedp caches ids from that map, so the
// next command on that tab polled an id the browser no longer recognised and
// sat there until its deadline. Fixed in f8f28a020. A wedged tab is therefore
// evidence of neither a dialog NOR a dead tab — stale CDP state alone does it.
//
// The hedge held, but "may have an open dialog" as the ONLY named cause sends
// an agent to browser_handle_dialog, which answers "no dialog", leaving it
// with no next move. Naming the second outcome costs nothing when the guess is
// right and is the whole recovery when it is wrong — and re-navigate is the
// correct action for a dead tab and a stale-node-id one alike, which is why
// the message commits to the ACTION and hedges the CAUSE. Keep both outcomes.
//
// Returns (rewritten, true) when err was a CDP timeout it could say something
// better about, and (nil, false) otherwise. The bool is what callers branch on
// rather than comparing the result against the input: an identity comparison
// on an error reads as an errors.Is mistake, and is one wrapping away from
// being one.
func dialogAwareTimeout(mgr *BrowserManager, sessionID, toolName string, err error) (error, bool) {
	if err == nil || mgr == nil || !isCDPTimeout(err) {
		return nil, false
	}

	if dlg := mgr.PendingDialogOn(sessionID); dlg != nil {
		return fmt.Errorf("%s: the tab is blocked by %s — answer it with browser_handle_dialog{accept:false} and retry",
			toolName, dlg.Summary()), true
	}

	after := ""
	if act := mgr.lastActivationOn(sessionID); act != "" {
		after = " after " + act
	}
	return fmt.Errorf("%s: the tab stopped answering%s. It may have an open dialog that predates this session's "+
		"listener — try browser_handle_dialog{accept:false}; if that reports no dialog, the tab is wedged (crashed, "+
		"closed, or holding stale CDP state) and a re-navigate is what recovers it",
		toolName, after), true
}

// isCDPTimeout reports whether err is the "the tab stopped answering" shape,
// as opposed to a real, specific failure. A wedged tab surfaces as a deadline
// on the tool's own bounded context; the string check catches the same thing
// after chromedp has wrapped it.
func isCDPTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(err.Error(), "context deadline exceeded")
}

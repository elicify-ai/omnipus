package browser

// tab_open.go — how a BRAND-NEW tab is opened: create the page target, ACTIVATE
// it, then attach, all inside one bounded budget, closing the target on any
// failure after it exists.
//
// Adopting a target that already exists (a popup, window.open) does not come
// through here: createTab attaches to those directly, as before.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// newTabCloseTimeout bounds the Target.closeTarget that releases a tab which
// failed to open. It is a browser-level command answered by the browser
// process itself — measured at 1–5ms even on the load-600 host where the
// attach stall below was diagnosed — so a few seconds is generous.
const newTabCloseTimeout = 5 * time.Second

// tabOpenTimeoutError reports that opening a tab did not finish within its
// budget. phase names the step that was still waiting: "open" (creating and
// activating the page target) or "attach" (binding chromedp to it). The
// "attach" wording is byte-identical to the message this package has always
// produced, which operators and the live-panel error surface already quote.
type tabOpenTimeoutError struct {
	after time.Duration
	phase string
}

func (e *tabOpenTimeoutError) Error() string {
	return fmt.Sprintf(
		"browser: timed out after %s waiting for the browser to %s the tab (target may be unresponsive)",
		e.after, e.phase,
	)
}

// openActivatedTarget creates a blank page target and ACTIVATES it before
// anything attaches to it.
//
// Why activation — measured 2026-09-14 on macOS against the managed Chrome for
// Testing 153.0.8010.36 (the version the installer's "Stable" channel serves
// today), over the CDP pipe, headless: a page target created with
// Target.createTarget and attached WITHOUT being activated did not answer the
// first tab-level command chromedp sends while attaching (Runtime.evaluate): in
// 11 of 11 launches the attach did not finish within the 60–180s the probe
// waited (twice Runtime.evaluate answered after ~46s, nine times not at all
// inside the window) — across real and software GPU, the full managed flag set
// and a bare --headless=new, and whether chromedp or this code created the
// target. The same target activated
// first attached in 0.9–1.7s, at a higher machine load (2 of 2). Chrome 151 and
// Chrome 152 attached in 3–5s without activation under the same load. Chrome
// sat idle through the stall (renderers sleeping, CPU time flat), so this is
// not a slow machine that a longer timeout would rescue: the new tab's page
// does not run until the tab is activated.
//
// Activating at creation moves no caller's final focus: the first tab,
// browser_open_tab and CloseTab's replacement all make the new tab the active
// one right afterwards anyway, and the capture encoder page's caller
// (CaptureSession.Start) re-focuses the agent's tab once the encoder exists.
//
// If activation fails, the created target is closed before returning, so a
// failed open never leaves a tab behind in Chrome.
func openActivatedTarget(
	ctx context.Context,
	exec cdp.Executor,
	browserContextID cdp.BrowserContextID,
) (target.ID, error) {
	create := target.CreateTarget("about:blank")
	if browserContextID != "" {
		create = create.WithBrowserContextID(browserContextID)
	}
	execCtx := cdp.WithExecutor(ctx, exec)
	id, err := create.Do(execCtx)
	if err != nil {
		return "", fmt.Errorf("browser: failed to create the new tab: %w", err)
	}
	if err := target.ActivateTarget(id).Do(execCtx); err != nil {
		closeTargetBestEffort(exec, id, "activating the new tab failed")
		return "", fmt.Errorf("browser: failed to activate the new tab %s: %w", id, err)
	}
	return id, nil
}

// closeTargetBestEffort closes a target this package created but could not
// finish opening. It runs on a fresh bounded context because the caller's is
// typically the one that just expired. A failure is logged, not returned: the
// caller is already reporting the error that made the tab unusable.
func closeTargetBestEffort(exec cdp.Executor, id target.ID, why string) {
	ctx, cancel := context.WithTimeout(context.Background(), newTabCloseTimeout)
	defer cancel()
	if err := target.CloseTarget(id).Do(cdp.WithExecutor(ctx, exec)); err != nil {
		logger.WarnCF("browser", "Could not close a new tab that failed to open; it may remain open in Chrome", map[string]any{
			"target_id": string(id),
			"reason":    why,
			"error":     err.Error(),
		})
	}
}

// newTabSteps are the browser-level steps of opening a brand-new tab, kept
// separate from openNewTab so its ordering, budget and cleanup contract can be
// tested without a browser.
type newTabSteps struct {
	// open creates and activates the page target (openActivatedTarget).
	open func(ctx context.Context) (target.ID, error)
	// attach builds the tab's chromedp context for id and returns the FIRST
	// chromedp.Run to perform on it. It must not run it.
	attach func(id target.ID) (ctx context.Context, cancel context.CancelFunc, firstRun func() error)
	// close releases the target after a failed attach.
	close func(id target.ID)
}

// newTabStepsFor wires newTabSteps to the real browser that parent is bound to
// — a sessionEntry.browserCtx, or the allocator root context when bootstrapping
// that browserCtx (see bootstrapBrowserCtx).
func newTabStepsFor(parent context.Context) (newTabSteps, error) {
	c := chromedp.FromContext(parent)
	if c == nil || c.Browser == nil {
		return newTabSteps{}, errors.New("browser: no connected browser to open a tab in")
	}
	b := c.Browser
	browserContextID := c.BrowserContextID
	return newTabSteps{
		open: func(ctx context.Context) (target.ID, error) {
			return openActivatedTarget(ctx, b, browserContextID)
		},
		attach: func(id target.ID) (context.Context, context.CancelFunc, func() error) {
			ctx, cancel := chromedp.NewContext(parent, chromedp.WithTargetID(id))
			return ctx, cancel, func() error { return chromedp.Run(ctx) }
		},
		close: func(id target.ID) {
			closeTargetBestEffort(b, id, "attaching to the new tab did not complete")
		},
	}, nil
}

// openNewTab opens, activates and attaches a brand-new tab within ONE budget.
//
// The budget is the one the attach alone used to have (firstAttachTimeout), so
// adding the open step adds no waiting time to any path: a browser that cannot
// open a tab still fails in the same bounded time, with an error naming the
// step it was stuck on.
//
// Any failure after the target exists closes the target. chromedp's own
// context cancel closes only a tab it finished attaching to, so an attach that
// timed out used to leave its tab open in Chrome — one more live page per
// failed call, on a browser that was already not keeping up.
//
// The first chromedp.Run is raced by runFirstAttach, never given a timed-out
// context: see runFirstAttach for why that distinction is load-bearing.
func openNewTab(
	parent context.Context,
	budget time.Duration,
	steps newTabSteps,
) (context.Context, context.CancelFunc, error) {
	deadline := time.Now().Add(budget)

	openCtx, openCancel := context.WithDeadline(parent, deadline)
	id, err := steps.open(openCtx)
	expired := errors.Is(openCtx.Err(), context.DeadlineExceeded)
	openCancel()
	if err != nil {
		if expired {
			return nil, nil, fmt.Errorf("%w: %w", &tabOpenTimeoutError{after: budget, phase: "open"}, err)
		}
		return nil, nil, err
	}

	ctx, cancel, firstRun := steps.attach(id)
	if err := runFirstAttachContext(parent, firstRun, time.Until(deadline)); err != nil {
		// Close before cancel: the target still exists and the browser
		// session is live, so the close is answered; cancelling first would
		// race chromedp's own (1s-bounded, attach-dependent) cleanup.
		steps.close(id)
		cancel()
		var timeout *tabOpenTimeoutError
		if errors.As(err, &timeout) {
			return nil, nil, &tabOpenTimeoutError{after: budget, phase: "attach"}
		}
		return nil, nil, err
	}
	return ctx, cancel, nil
}

package browser

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// viewportOperationTimeout is the sum of the existing maximum stage budgets:
// initial bounds plus one retry, scale, compensation, and two settle passes.
// Admission consumes this same budget; it does not add another waiting period.
const viewportOperationTimeout = 3*viewportSetTimeout + viewportScaleTimeout + 2*viewportSettleBudget

// SetViewportContext resizes an already-attached target within the caller's
// lifetime. It never starts or recreates a browser session. A true result means
// the initial bounds were acknowledged, even when a later stage was canceled.
func (r *LiveViewRegistry) SetViewportContext(caller context.Context, sessionID string, width, height int, scale float64) (bool, error) {
	if err := caller.Err(); err != nil {
		return false, err
	}
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return false, nil
	}
	lv.mu.Lock()
	tabCtx := lv.tabCtx
	lv.mu.Unlock()
	return lv.applyViewportContext(caller, tabCtx, width, height, scale)
}

func viewportContextError(caller, operation context.Context) error {
	if err := caller.Err(); err != nil {
		return err
	}
	return operation.Err()
}

// withViewportAdmission admits all viewport entry paths in the same order as
// input: manager tab command, live input, then viewport mutex. Async target
// reapplication enters here after its source callback has released admission.
func (lv *LiveView) withViewportAdmission(caller, tabCtx context.Context, work func(context.Context) (bool, error), after func(context.Context) error) (applied bool, err error) {
	if callerErr := caller.Err(); callerErr != nil {
		return false, callerErr
	}
	if tabCtx == nil {
		return false, nil
	}
	budget := viewportOperationTimeout
	if deadline, ok := caller.Deadline(); ok {
		budget = min(budget, time.Until(deadline))
	}
	operation, cancel := context.WithTimeout(tabCtx, budget)
	stop := context.AfterFunc(caller, cancel)
	defer cancel()
	defer stop()
	// Preserve the caller's error identity even when its cancellation hook
	// reaches the target-derived operation context asynchronously.
	defer func() {
		if ended := viewportContextError(caller, operation); ended != nil {
			if err == nil {
				err = fmt.Errorf("viewport completion: %w", ended)
			} else if !errors.Is(err, ended) {
				// Caller cancellation still wins, without erasing the failed stage.
				err = fmt.Errorf("%v: %w", err, ended)
			}
		}
	}()
	apply := func() (bool, error) {
		if lv.mgr != nil {
			release, admissionErr := lv.mgr.acquireLiveTabCommand(operation, lv.sessionID)
			if admissionErr != nil {
				return false, fmt.Errorf("viewport tab admission: %w", admissionErr)
			}
			defer release()
		}
		lv.mu.Lock()
		state := lv.inputStateLocked()
		lv.mu.Unlock()
		if inputErr := acquireInputGate(operation, state.gate); inputErr != nil {
			return false, fmt.Errorf("viewport input admission: %w", inputErr)
		}
		defer func() { <-state.gate }()
		if operationErr := viewportContextError(caller, operation); operationErr != nil {
			return false, operationErr
		}
		if lv.mgr != nil {
			active, _, snapshotErr := lv.mgr.activeTargetSnapshot(lv.sessionID)
			if snapshotErr != nil {
				return false, fmt.Errorf("viewport active target lookup: %w", snapshotErr)
			}
			if active != tabCtx {
				return false, fmt.Errorf("browser live: viewport target changed before resize")
			}
		}
		// Invalidate only once browser work is admitted, while both gates are
		// still held. A canceled waiter must not erase another command's cache.
		defer func() {
			if viewportContextError(caller, operation) != nil {
				lv.invalidateCSSViewportCache()
			}
		}()
		return work(operation)
	}
	applied, err = apply()
	if err == nil && after != nil {
		err = after(operation)
	}
	return applied, err
}

// --- moved from live.go 2026-09-15 ---

// SetViewport resizes sessionID's captured tab to width x height CSS pixels
// and renders it at deviceScaleFactor. Thin wrapper: it resolves the live view
// and the tab context currently bound to it, then hands both to applyViewport,
// which carries the whole mechanism and its doc comment.
//
// Returns false if no live view exists for sessionID (nothing to resize).
func (r *LiveViewRegistry) SetViewport(sessionID string, width, height int, deviceScaleFactor float64) (bool, error) {
	return r.SetViewportContext(context.Background(), sessionID, width, height, deviceScaleFactor)
}

// CSSViewport returns sessionID's cached CSS layout viewport — SetViewport's
// Page.getLayoutMetrics read-back (including its at-most-one chrome-delta
// compensation re-read), the CDP-verified truth of the tab's actual size
// (see SetViewport's mechanism doc comment). ok is false when no live view
// exists for sessionID, or the cache is unset/invalidated (zero — either
// SetViewport has never run for this session, or its last read-back failed
// or came back degenerate; see invalidateCSSViewportCache).
//
// Follow-up to
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md (measured
// 2026-07-31): the gateway's browser_ws.go handleViewport calls this right
// after a successful SetViewport so it can thread the verified dimensions
// through to CaptureSession.RecaptureAt — without them, the encoder's own
// chrome.tabs.get-based resolution can race the OS window reflow and pin
// the WebRTC stream to a stale tab size.
func (r *LiveViewRegistry) CSSViewport(sessionID string) (w, h int, ok bool) {
	sessionID = r.resolveSessionID(sessionID)
	lv, exists := r.lookup(sessionID)
	if !exists {
		return 0, 0, false
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.cssViewportW <= 0 || lv.cssViewportH <= 0 {
		return 0, 0, false
	}
	return lv.cssViewportW, lv.cssViewportH, true
}

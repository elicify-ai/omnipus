package browser

import (
	"context"
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

// applyViewportContext admits all viewport entry paths in the same order as
// input: manager tab command, live input, then viewport mutex. Async target
// reapplication enters here after its source callback has released admission.
func (lv *LiveView) applyViewportContext(caller, tabCtx context.Context, width, height int, scale float64) (applied bool, err error) {
	if err := caller.Err(); err != nil {
		return false, err
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
			err = ended
		}
	}()
	if lv.mgr != nil {
		release, err := lv.mgr.acquireLiveTabCommand(operation, lv.sessionID)
		if err != nil {
			return false, err
		}
		defer release()
	}
	lv.mu.Lock()
	state := lv.inputStateLocked()
	lv.mu.Unlock()
	if err := acquireInputGate(operation, state.gate); err != nil {
		return false, err
	}
	defer func() { <-state.gate }()
	if err := viewportContextError(caller, operation); err != nil {
		return false, err
	}
	if lv.mgr != nil {
		active, _, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
		if err != nil {
			return false, err
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
	return lv.applyViewportAdmitted(caller, tabCtx, operation, width, height, scale)
}

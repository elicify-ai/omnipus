package browser

import (
	"context"
	"errors"
)

// ReleaseInputSourceContext joins cleanup for an already-canceled input source.
// Callers must cancel the source and stop its dispatcher before calling this.
// A successful return permits the subsequent control change; failure does not.
func (r *LiveViewRegistry) ReleaseInputSourceContext(ctx context.Context, sessionID string, source context.Context) error {
	if !inputSourceEnded(source) {
		return errors.New("browser live: input source must be canceled before draining")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	lv, ok := r.lookup(r.resolveSessionID(sessionID))
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, interactiveInputTimeout)
	defer cancel()
	// Match dispatch's tab-then-input order. This also waits for an admitted
	// operation to leave its command gate before we acknowledge cleanup.
	if lv.mgr != nil {
		release, err := lv.mgr.acquireLiveTabCommand(ctx, lv.sessionID)
		if err != nil {
			return err
		}
		defer release()
	}
	lv.mu.Lock()
	state := lv.inputStateLocked()
	lv.mu.Unlock()
	if err := acquireInputGate(ctx, state.gate); err != nil {
		return err
	}
	defer func() { <-state.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	// The asynchronous source callback uses the same input gate; either path
	// may win, and completed releases are removed by flushPendingInput.
	if err := lv.flushPendingInput(ctx); err != nil {
		return err
	}
	lv.mu.Lock()
	delete(state.sources, source)
	lv.mu.Unlock()
	return nil
}

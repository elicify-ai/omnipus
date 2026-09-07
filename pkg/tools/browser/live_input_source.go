package browser

import (
	"context"
)

func inputSourceEnded(source context.Context) bool {
	return source != nil && source.Err() != nil
}

func navigationInputKind(kind string) bool {
	switch kind {
	case "navigate", "back", "forward", "reload":
		return true
	default:
		return false
	}
}

// One callback is registered per original input source. Checking the source on
// each held record also lets the next input flush it before this callback runs.
func (lv *LiveView) releaseInputSource(source context.Context) {
	lv.mu.Lock()
	state := lv.inputStateLocked()
	delete(state.sources, source)
	lv.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), interactiveInputTimeout)
	defer cancel()
	err := acquireInputGate(ctx, state.gate)
	if err == nil {
		err = lv.flushPendingInput(ctx)
		<-state.gate
	}
	if err != nil {
		lv.reportInputCleanup(err)
	}
}

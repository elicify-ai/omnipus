package browser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const interactiveInputTimeout = 2 * time.Second
const maxHeldInputsPerViewer = 256

// liveInputState uses LiveView.mu for bookkeeping. gate separately serializes
// browser commands and held-state transitions without holding mu during I/O.
type liveInputState struct {
	gate           chan struct{}
	requests       map[*liveInputRequest]struct{}
	held           map[heldInputID]LiveInput
	pendingRelease map[heldInputID]bool
	retired        map[context.Context]bool
	abandoned      map[string]bool
	cleaning       bool
	sources        map[context.Context]bool
}
type liveInputRequest struct {
	viewer string
	target context.Context
	cancel context.CancelFunc
}
type heldInputID struct {
	target context.Context
	viewer string
	key    string
	source context.Context
}

func (lv *LiveView) inputStateLocked() *liveInputState {
	if lv.inputState == nil {
		lv.inputState = &liveInputState{
			gate:           make(chan struct{}, 1),
			requests:       make(map[*liveInputRequest]struct{}),
			held:           make(map[heldInputID]LiveInput),
			pendingRelease: make(map[heldInputID]bool),
			retired:        make(map[context.Context]bool),
			abandoned:      make(map[string]bool),
			sources:        make(map[context.Context]bool),
		}
	}
	return lv.inputState
}

// InputContext binds an attached viewer's input to its caller and attachment.
// Attachment is independent of the presentation-only control indicator.
func (r *LiveViewRegistry) InputContext(ctx context.Context, sessionID, viewerID string, in LiveInput) error {
	sessionID = r.resolveSessionID(sessionID)
	lv, ok := r.lookup(sessionID)
	if !ok {
		return realInputError("browser live: no active live view for session %q", sessionID)
	}
	scope, cancel := context.WithCancel(ctx)
	request := &liveInputRequest{viewer: viewerID, cancel: cancel}
	lv.mu.Lock()
	if _, attached := lv.viewers[viewerID]; !attached {
		lv.mu.Unlock()
		cancel()
		return benignInputError("browser live: viewer is not attached")
	}
	lv.inputStateLocked().requests[request] = struct{}{}
	lv.mu.Unlock()
	defer func() { cancel(); lv.mu.Lock(); delete(lv.inputState.requests, request); lv.mu.Unlock() }()
	return lv.dispatchInputContext(scope, viewerID, in)
}

func (lv *LiveView) dispatchInputContext(caller context.Context, viewerID string, in LiveInput) error {
	if inputSourceEnded(in.SourceContext) {
		return realInputError("browser live: input source canceled: %w", in.SourceContext.Err())
	}
	if err := caller.Err(); err != nil {
		return realInputError("browser live: input canceled: %w", err)
	}
	budget := interactiveInputTimeout
	if lv.mgr != nil {
		if configured := lv.mgr.PageTimeout(); configured > 0 && configured < budget {
			budget = configured
		}
	}
	if deadline, ok := caller.Deadline(); ok {
		budget = min(budget, time.Until(deadline))
	}
	lv.mu.Lock()
	targetCtx := lv.tabCtx
	state := lv.inputStateLocked()
	if targetCtx == nil {
		allowed := lv.allowInputLocked(in.Kind)
		lv.mu.Unlock()
		if !allowed {
			return inputRateLimitError(in.Kind)
		}
		return realInputError("browser live: session is not attached")
	}
	ctx, cancel := context.WithTimeout(targetCtx, budget)
	stop := context.AfterFunc(caller, cancel)
	if in.SourceContext != nil {
		stopSource := context.AfterFunc(in.SourceContext, cancel)
		defer stopSource()
	}
	request := &liveInputRequest{viewer: viewerID, target: targetCtx, cancel: cancel}
	state.requests[request] = struct{}{}
	lv.mu.Unlock()
	defer func() { stop(); cancel(); lv.mu.Lock(); delete(state.requests, request); lv.mu.Unlock() }()
	if lv.mgr != nil {
		release, err := lv.mgr.acquireLiveTabCommand(ctx, lv.sessionID)
		if err != nil {
			return realInputError("browser live: input canceled while waiting for tab operation: %w", err)
		}
		defer release()
	}
	if err := acquireInputGate(ctx, state.gate); err != nil {
		return realInputError("browser live: input canceled while queued: %w", err)
	}
	defer func() { <-state.gate }()
	if err := caller.Err(); err != nil {
		return realInputError("browser live: input canceled: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return realInputError("browser live: input canceled: %w", err)
	}
	if inputSourceEnded(in.SourceContext) {
		return realInputError("browser live: input source canceled: %w", in.SourceContext.Err())
	}
	if err := lv.flushPendingInput(ctx); err != nil {
		return realInputError("browser live: held input cleanup failed: %w", err)
	}
	lv.mu.Lock()
	retired := state.retired[targetCtx] || lv.tabCtx != targetCtx
	lv.mu.Unlock()
	if retired {
		return benignInputError("browser live: input target changed; retry on the current tab")
	}
	lv.mu.Lock()
	_, tracked := state.held[heldInputID{target: targetCtx, viewer: viewerID, key: inputHoldKey(in), source: in.SourceContext}]
	releasing := in.Kind == "key_up" || in.Kind == "mouse_up"
	lv.mu.Unlock()
	// Ordinary interaction must describe the committed picture. Navigation
	// controls and an already-owned release do not depend on that picture.
	if !(tracked && releasing) && !navigationInputKind(in.Kind) {
		if lv.mgr == nil {
			return benignInputError("browser live: no capture for this panel; wait for the current picture")
		}
		cs := lv.mgr.CaptureSessionForPanel(lv.sessionID)
		if cs == nil {
			return benignInputError("browser live: no capture for this panel; wait for the current picture")
		}
		frame := cs.FrameState()
		activeCtx, activeID, err := lv.mgr.activeTargetSnapshot(lv.sessionID)
		if err != nil || activeCtx != targetCtx || activeID == "" || string(activeID) != frame.TargetID {
			return benignInputError("browser live: displayed target changed; wait for the current picture")
		}
		if !frame.Ready || in.CaptureID == "" || in.CaptureID != frame.CaptureID || in.CaptureGeneration == 0 || in.CaptureGeneration != frame.Generation {
			return benignInputError("browser live: displayed frame changed; wait for the current picture")
		}
	}
	lv.mu.Lock()
	allowed := tracked && releasing || lv.allowInputLocked(in.Kind)
	lv.mu.Unlock()
	if !allowed {
		return inputRateLimitError(in.Kind)
	}
	err := lv.dispatchInputCommand(ctx, targetCtx, viewerID, in)
	if err != nil && releasing {
		// A release rejected before command delivery (for example, unavailable
		// coordinate mapping) still owes cleanup of any previously accepted hold.
		lv.recordHeldInput(targetCtx, viewerID, in, false)
	}
	return err
}

// Rate admission follows serialized press completion so an in-flight press
// cannot make its queued release appear unowned and therefore disposable.
func inputRateLimitError(kind string) error {
	limit := maxDiscreteInputEventsPerSecond
	if isCoalescibleInputKind(kind) {
		limit = maxCoalescibleInputEventsPerSecond
	}
	return benignInputError("browser live: input rate limit exceeded for %s (%d/s)", kind, limit)
}

func acquireInputGate(ctx context.Context, gate chan struct{}) error {
	select {
	case gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func inputRemaining(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return max(time.Until(deadline), time.Nanosecond)
	}
	return interactiveInputTimeout
}

func (lv *LiveView) dispatchTrackedInput(ctx, targetCtx context.Context, viewerID string, in LiveInput) error {
	action, err := buildInputAction(in)
	if err != nil {
		return realInputError("%w", err)
	}
	if in.Kind == "navigate" {
		if err := lv.mgr.ValidateURL(ctx, in.URL); err != nil {
			return realInputError("browser live: navigate blocked: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return realInputError("browser live: input canceled: %w", err)
	}
	skip, buttons, modifiers, err := lv.prepareHeldInput(targetCtx, viewerID, in)
	if err != nil {
		return realInputError("%w", err)
	}
	if skip {
		return nil
	}
	switch a := action.(type) {
	case *input.DispatchMouseEventParams:
		a.Buttons = buttons
		a.Modifiers = input.Modifier(modifiers)
	case *input.DispatchKeyEventParams:
		a.Modifiers = input.Modifier(modifiers)
	}
	err = lv.runCDP(ctx, inputRemaining(ctx), action)
	// Only an explicit protocol rejection proves a press was not accepted.
	// Other transport failures have uncertain delivery; remember the possible
	// hold for cleanup without replaying the press or typed content.
	var rejection *cdproto.Error
	if err == nil || !errors.As(err, &rejection) || in.Kind == "key_up" || in.Kind == "mouse_up" {
		lv.recordHeldInput(targetCtx, viewerID, in, err == nil)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return realInputError("browser live: input dispatch failed: %w", err)
	}
	return nil
}

func inputHoldKey(in LiveInput) string {
	switch in.Kind {
	case "key_down", "key_up":
		if in.Code != "" {
			return "key:" + in.Code
		}
		if in.KeyCode > 0 {
			return fmt.Sprintf("keycode:%d", in.KeyCode)
		}
		if in.Key != "" {
			return "key:" + in.Key
		}
	case "mouse_down", "mouse_up":
		if buttonBits(in.Button) != 0 {
			return "mouse:" + in.Button
		}
	}
	return ""
}
func buttonBits(button string) int64 {
	switch button {
	case "left":
		return 1
	case "right":
		return 2
	case "middle":
		return 4
	case "back":
		return 8
	case "forward":
		return 16
	}
	return 0
}
func keyModifier(in LiveInput) int {
	switch in.Key {
	case "Alt":
		return 1
	case "Control":
		return 2
	case "Meta":
		return 4
	case "Shift":
		return 8
	}
	switch in.Code {
	case "AltLeft", "AltRight":
		return 1
	case "ControlLeft", "ControlRight":
		return 2
	case "MetaLeft", "MetaRight":
		return 4
	case "ShiftLeft", "ShiftRight":
		return 8
	}
	return 0
}

// Called with the command gate held. Successful release ownership changes and
// the actual browser command remain in one serialized operation.
func (lv *LiveView) prepareHeldInput(target context.Context, viewer string, in LiveInput) (bool, int64, int, error) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	state := lv.inputStateLocked()
	id := heldInputID{target: target, viewer: viewer, key: inputHoldKey(in), source: in.SourceContext}
	release := in.Kind == "key_up" || in.Kind == "mouse_up"
	if release {
		if _, owned := state.held[id]; !owned {
			return true, 0, 0, nil
		}
	}
	ownCount := 0
	var buttons int64
	mods := clampModifiers(in.Modifiers)
	if in.Kind == "key_up" {
		mods &^= keyModifier(in)
	}
	for heldID, held := range state.held {
		if heldID.viewer == viewer {
			ownCount++
		}
		if heldID.target != target {
			continue
		}
		if release && heldID.key == id.key && heldID != id {
			delete(state.held, id)
			delete(state.pendingRelease, id)
			return true, 0, 0, nil
		}
		if release && heldID == id {
			continue
		}
		buttons |= buttonBits(held.Button)
		mods |= keyModifier(held)
	}
	if (in.Kind == "key_down" || in.Kind == "mouse_down") && id.key != "" {
		if _, exists := state.held[id]; !exists && ownCount >= maxHeldInputsPerViewer {
			return false, 0, 0, fmt.Errorf("browser live: too many held inputs for this viewer")
		}
		buttons |= buttonBits(in.Button)
		mods |= keyModifier(in)
	}
	return false, buttons, mods, nil
}
func (lv *LiveView) recordHeldInput(target context.Context, viewer string, in LiveInput, confirmed bool) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	state := lv.inputStateLocked()
	id := heldInputID{target: target, viewer: viewer, key: inputHoldKey(in), source: in.SourceContext}
	switch in.Kind {
	case "key_down", "mouse_down":
		if id.key != "" {
			state.held[id] = LiveInput{SourceContext: in.SourceContext, Kind: in.Kind, Key: in.Key, Code: in.Code, KeyCode: in.KeyCode, Button: in.Button, X: in.X, Y: in.Y, HasXY: in.HasXY}
			if in.SourceContext != nil && !state.sources[in.SourceContext] {
				state.sources[in.SourceContext] = true
				context.AfterFunc(in.SourceContext, func() { lv.releaseInputSource(in.SourceContext) })
			}
		}
	case "key_up", "mouse_up":
		if confirmed {
			delete(state.held, id)
			delete(state.pendingRelease, id)
		} else if _, exists := state.held[id]; exists {
			state.pendingRelease[id] = true
		}
	}
	if confirmed && in.HasXY {
		for heldID, held := range state.held {
			if heldID.target == target && held.Kind == "mouse_down" {
				held.X = in.X
				held.Y = in.Y
				state.held[heldID] = held
			}
		}
	}
}

// detachInput is called after membership removal. No new public request can
// enter for this viewer; cancellation covers requests that were already admitted.
func (lv *LiveView) detachInput(viewer string) {
	lv.mu.Lock()
	state := lv.inputStateLocked()
	state.abandoned[viewer] = true
	var cancels []context.CancelFunc
	for request := range state.requests {
		if request.viewer == viewer {
			cancels = append(cancels, request.cancel)
		}
	}
	lv.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
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

// Tab notifications can originate from event callbacks. A single bounded worker
// releases retired-target holds without blocking the browser's event reader.
func (lv *LiveView) retireInputTarget(oldTarget, newTarget context.Context) {
	lv.mu.Lock()
	state := lv.inputStateLocked()
	state.retired[oldTarget] = true
	delete(state.retired, newTarget)
	var cancels []context.CancelFunc
	for request := range state.requests {
		if request.target == oldTarget {
			cancels = append(cancels, request.cancel)
		}
	}
	start := !state.cleaning
	state.cleaning = true
	lv.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if !start {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), interactiveInputTimeout)
		defer cancel()
		err := acquireInputGate(ctx, state.gate)
		if err == nil {
			for {
				err = lv.flushPendingInput(ctx)
				lv.mu.Lock()
				pending := false
				for id := range state.held {
					if state.retired[id.target] || state.abandoned[id.viewer] || state.pendingRelease[id] {
						pending = true
						break
					}
				}
				if err != nil || !pending {
					state.cleaning = false
					lv.mu.Unlock()
					break
				}
				lv.mu.Unlock()
			}
			<-state.gate
		} else {
			lv.mu.Lock()
			state.cleaning = false
			lv.mu.Unlock()
		}
		if err != nil {
			lv.reportInputCleanup(err)
		}
	}()
}

// flushPendingInput runs with the command gate held. Failed releases remain
// pending for the next operation; they are surfaced rather than silently lost.
func (lv *LiveView) flushPendingInput(ctx context.Context) error {
	for {
		lv.mu.Lock()
		state := lv.inputStateLocked()
		var id heldInputID
		var held LiveInput
		found := false
		for candidate, value := range state.held {
			if state.retired[candidate.target] || state.abandoned[candidate.viewer] || state.pendingRelease[candidate] || inputSourceEnded(candidate.source) {
				id, held, found = candidate, value, true
				break
			}
		}
		if !found {
			clear(state.abandoned)
			for target := range state.retired {
				if target.Err() != nil {
					delete(state.retired, target)
				}
			}
			lv.mu.Unlock()
			return nil
		}
		if id.target.Err() != nil {
			delete(state.held, id)
			delete(state.pendingRelease, id)
			lv.mu.Unlock()
			continue
		}
		lv.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if held.Kind == "key_down" {
			held.Kind = "key_up"
		} else {
			held.Kind = "mouse_up"
		}
		skip, buttons, modifiers, err := lv.prepareHeldInput(id.target, id.viewer, held)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		action, err := buildInputAction(held)
		if err != nil {
			return err
		}
		switch a := action.(type) {
		case *input.DispatchMouseEventParams:
			a.Buttons = buttons
			a.Modifiers = input.Modifier(modifiers)
		case *input.DispatchKeyEventParams:
			a.Modifiers = input.Modifier(modifiers)
		}
		commandCtx, cancel := context.WithTimeout(id.target, inputRemaining(ctx))
		stop := context.AfterFunc(ctx, cancel)
		err = lv.runCDP(commandCtx, inputRemaining(ctx), action)
		stop()
		cancel()
		if err != nil {
			return err
		}
		lv.recordHeldInput(id.target, id.viewer, held, true)
	}
}
func (lv *LiveView) reportInputCleanup(err error) {
	logger.WarnCF("browser", "live view: could not release held input; cleanup will retry before the next input", map[string]any{"session_id": lv.sessionID, "error": err.Error()})
}

type navigationInputAction struct{ url string }

func (a navigationInputAction) Do(ctx context.Context) error {
	_, _, destinationError, _, err := page.Navigate(a.url).Do(ctx)
	if err != nil {
		return err
	}
	if destinationError != "" {
		return fmt.Errorf("page load error %s", destinationError)
	}
	return nil
}

type historyBackInputAction struct{}

func (historyBackInputAction) Do(ctx context.Context) error {
	index, entries, err := page.GetNavigationHistory().Do(ctx)
	if err != nil {
		return err
	}
	if index <= 0 || index > int64(len(entries)) {
		return nil
	}
	return page.NavigateToHistoryEntry(entries[index-1].ID).Do(ctx)
}

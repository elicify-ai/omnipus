// browser_ws_input.go: Translate socket input events (keys, pointer, scroll) into browser commands.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// handleInput dispatches a viewer input event, gated by the LiveView's
// control lock (ADR-038 D6). ADR-038 finding #4: LiveViewRegistry.Input
// classifies its failure as benign or real (browser.IsBenignLiveInputError).
// Benign, high-frequency rejections (not attached, not controlling,
// rate-limited) are logged at Debug and NOT surfaced as a status frame — a
// stray mouse-move sent just before/after losing control is an expected,
// frequent occurrence, and a status frame per rejected event would flood a
// client that's still moving the mouse. Real failures (session not attached
// at all, an unknown input kind, an SSRF-/scheme-blocked "navigate" URL
// (ADR-039 D-A2), or — most importantly — a genuine CDP transport error
// meaning the tab crashed or is unreachable) ARE surfaced, throttled to at
// most one IDENTICAL browser_status(error) per minInputErrorInterval so a
// burst of failed dispatches against a dead tab can't flood the connection —
// but a DIFFERENT failure message (e.g. the user's quick retry against a
// different blocked navigate URL) is never suppressed just for landing
// inside that same cooldown window (7-reviewer LOW finding: the user must
// always see WHY their navigate was refused), AND a "navigate"-kind error is
// NEVER throttled at all regardless of message content (B4, 7-reviewer
// finding): unlike a mouse-move stream, a navigate is one submission per
// Enter keypress, and the SPA clears its error banner optimistically on each
// submit — so suppressing even a byte-identical repeat (e.g. resubmitting
// the exact same blocked URL) would leave the user looking at no error at
// all after their retry was refused again.
func (h *BrowserWSHandler) handleInput(wc *browserWSConn, state *browserConnState, viewerID string, data []byte) {
	attachment := state.commandAttachment()
	h.handleInputContext(attachment.ctx, wc, state, attachment, viewerID, data)
}

func (h *BrowserWSHandler) handleInputContext(ctx context.Context, wc *browserWSConn, state *browserConnState, attachment browserAttachmentSnapshot, viewerID string, data []byte, timing ...*browserInputTiming) {
	var probe *browserInputTiming
	if len(timing) > 0 {
		probe = timing[0]
	}
	runBrowserConnWorkHook(workKindInput)
	// Use the identity captured when this command was admitted. A replacement
	// attachment cancels this lifetime before any new identity is published.
	if ctx.Err() != nil || attachment.ctx.Err() != nil {
		return
	}
	mgr, sessionID, panelSessionID := attachment.mgr, attachment.sessionID, attachment.panelSessionID
	if mgr == nil || sessionID == "" {
		return
	}
	var frame generated.BrowserInputFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return
	}

	in := browserInputFrameToLiveInput(frame)
	in.SourceContext = attachment.ctx
	if probe != nil {
		in.Timing = &browser.LiveInputTimingObserver{Observe: probe.mark}
	}
	inputErr := mgr.Live().InputContext(ctx, panelSessionID, viewerID, in)
	if inputErr == nil {
		markBrowserInputControlSuccess(ctx)
	}
	if probe != nil {
		probe.outcome = "completed"
		if inputErr != nil {
			probe.outcome = "failed"
		}
	}
	if err := inputErr; err != nil {
		if commandWasSuperseded(ctx, attachment) {
			return
		}
		if browser.IsBenignLiveInputError(err) {
			slog.Debug("browser-ws: input rejected (benign)", "error", err, "session_id", sessionID)
			// The not-controller repair that lived here is gone: input is
			// never gated on a control lock (see dispatchInput). It could
			// still refuse a human whenever a DIFFERENT viewer was attached
			// and holding control — a second panel or a stale automation
			// session was enough to leave the real user with a dead mouse
			// and keyboard. Only the self-correcting rate limit remains.
			return
		}
		slog.Warn("browser-ws: input dispatch failed", "error", err, "session_id", sessionID)
		message := fmt.Sprintf("browser input failed: %s", err)
		now := time.Now()
		// B4 (7-reviewer finding): a navigate error is one-per-Enter and
		// user-initiated, unlike the high-frequency mouse_move/etc kinds this
		// cooldown exists to tame. The SPA clears its error banner
		// optimistically on every navigate submit, so suppressing a
		// byte-identical repeat here — the same URL rejected twice in a row
		// — would leave the user looking at NO error after resubmitting,
		// even though their submission was refused again. Navigate errors
		// therefore always emit; every other kind keeps the content-aware
		// cooldown.
		if state.shouldSendInputFailure(attachment, frame.Kind, message, now) {
			wc.sendCriticalScopedGen(operationErrorStatus(sessionID, message),
				dropContext(sessionID, viewerID, "input-error"), attachment.ctx, nil)
		}
	}
}

// browserInputFrameToLiveInput converts a generated.BrowserInputFrame into
// the engine-level browser.LiveInput dispatchInput expects. Extracted from
// handleInput (ADR-047 / wave-plan W2-A item 4) so the WS input path
// (handleInput, above) and the WebRTC data-channel input path
// (browser_webrtc_input_context.go's contextual sink) convert EXACTLY the same way and can
// never drift — both funnel into the SAME
// mgr.Live().Input(<the tab set this connection resolved at attach>, viewerID,
// in) call this function's result feeds (issue #671).
func browserInputFrameToLiveInput(frame generated.BrowserInputFrame) browser.LiveInput {
	in := browser.LiveInput{Kind: frame.Kind}
	if frame.X != nil {
		in.X = *frame.X
	}
	if frame.Y != nil {
		in.Y = *frame.Y
	}
	// HasXY records whether BOTH coordinates were actually present on the
	// wire (ADR-038 finding #5) — LiveInput.X/Y are plain float64, so without
	// this flag "coordinate omitted" would be indistinguishable from "0,0
	// sent explicitly," and buildInputAction would silently dispatch a
	// (0,0)-origin mouse event for a malformed frame instead of rejecting it.
	in.HasXY = frame.X != nil && frame.Y != nil
	if frame.Button != nil {
		in.Button = *frame.Button
	}
	if frame.DeltaX != nil {
		in.DeltaX = *frame.DeltaX
	}
	if frame.DeltaY != nil {
		in.DeltaY = *frame.DeltaY
	}
	if frame.Key != nil {
		in.Key = *frame.Key
	}
	if frame.Code != nil {
		in.Code = *frame.Code
	}
	if frame.KeyCode != nil {
		in.KeyCode = *frame.KeyCode
	}
	if frame.Text != nil {
		in.Text = *frame.Text
	}
	if frame.Url != nil {
		in.URL = *frame.Url
	}
	if frame.Modifiers != nil {
		in.Modifiers = *frame.Modifiers
	}
	// CaptureWidth/CaptureHeight (contracts/components/schemas/BrowserInputFrame.yaml):
	// the intrinsic pixel size of the capture frame the client mapped X/Y
	// into. Absent (older client, or a kind with no coordinates) leaves both
	// at their zero value, which dispatchInput's rescale gate
	// (CaptureWidth > 0 && CaptureHeight > 0) treats as "dispatch X/Y
	// unscaled" — see root-cause doc Fault 3
	// (docs/internal/browser-viewport-input-rootcause-2026-07-31.md).
	if frame.CaptureWidth != nil {
		in.CaptureWidth = *frame.CaptureWidth
	}
	if frame.CaptureHeight != nil {
		in.CaptureHeight = *frame.CaptureHeight
	}
	if frame.CaptureId != nil {
		in.CaptureID = *frame.CaptureId
	}
	if frame.CaptureGeneration != nil && *frame.CaptureGeneration > 0 {
		in.CaptureGeneration = uint64(*frame.CaptureGeneration)
	}

	return in
}

// inputKindIsDiscrete identifies navigation commands that interrupt pending
// navigation and surface refusals without the repeated-input error cooldown.
func inputKindIsDiscrete(kind string) bool {
	switch kind {
	case "navigate", "navigate_back", "reload", "stop_loading":
		return true
	}
	return false
}

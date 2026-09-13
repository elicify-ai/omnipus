package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// browserDedicatedInput is attachment-local ownership, never a wire payload.
type browserDedicatedInput struct {
	mu                             sync.Mutex
	closed                         bool
	epoch, offer, control, applied int
	peer                           *webrtc.DedicatedInputPeer
	cancel                         context.CancelFunc
	controlChanged                 chan struct{}
	failureLoggedEpoch             int
}

func (s *browserConnState) dedicatedInput() *browserDedicatedInput {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	return s.input
}
func (s *browserConnState) setDedicatedInput(enabled bool) {
	s.inputMu.Lock()
	old := s.input
	s.input = nil
	if enabled {
		s.input = &browserDedicatedInput{}
	}
	s.inputMu.Unlock()
	if old != nil {
		old.close()
	}
}
func (d *browserDedicatedInput) close() {
	d.mu.Lock()
	d.closed = true
	d.notifyControlChangedLocked()
	if d.cancel != nil {
		d.cancel()
	}
	peer := d.peer
	d.mu.Unlock()
	if peer != nil {
		peer.Close()
	}
}
func (d *browserDedicatedInput) current(epoch, offer int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.closed && d.epoch == epoch && d.offer == offer
}

// Diagnostic labels are fixed; transport errors and browser payloads never
// become log fields. One current-epoch failure is recorded even if its source
// has already been canceled and cannot receive the WebSocket notification.
func dedicatedFailureLogReason(reason string) string {
	switch reason {
	case "reliable input queue expired":
		return "queue_expired"
	case "reliable input queue full":
		return "queue_full"
	case "input connection closed", "input data channel closed", "input data channel failed":
		return "connection_closed"
	case "input channels did not open", "input channels not ready":
		return "channels_not_ready"
	case "invalid input data channel", "invalid input message", "invalid input payload", "button input requires coordinates", "invalid input identity", "invalid hover payload", "invalid reliable payload", "invalid reliable sequence", "invalid gesture barrier":
		return "invalid_input"
	case "Input negotiation failed. Retry input.":
		return "negotiation_failed"
	default:
		return "other"
	}
}

// stateSender reads the current control epoch when a connection event occurs.
func (d *browserDedicatedInput) stateSender(wc *browserWSConn, request browserAttachmentRequest, viewer string, f generated.BrowserInputOfferFrame, source context.Context) func(string) {
	return func(reason string) {
		d.mu.Lock()
		control := d.control
		logFailure := reason != "ready" && !d.closed && d.epoch == f.InputEpoch && d.offer == f.OfferId && d.failureLoggedEpoch != f.InputEpoch
		if logFailure {
			d.failureLoggedEpoch = f.InputEpoch
		}
		d.mu.Unlock()
		if logFailure {
			slog.Warn("browser dedicated input failed", "input_epoch", f.InputEpoch, "offer_id", f.OfferId, "control_epoch", control, "reason", dedicatedFailureLogReason(reason))
		}
		stateName := "failed"
		var detail *string
		if reason == "ready" {
			stateName = "ready"
		} else {
			detail = &reason
		}
		wc.sendCriticalScopedGen(generated.BrowserInputStateFrame{Type: "browser_input_state", SessionId: f.SessionId, InputEpoch: f.InputEpoch, OfferId: f.OfferId, ControlEpoch: control, State: stateName, Reason: detail}, dropContext(f.SessionId, viewer, "input-state"), request.ctx, func() bool { return d.current(f.InputEpoch, f.OfferId) && (reason != "ready" || source.Err() == nil) })
	}
}

func (h *BrowserWSHandler) dispatchDedicatedInputOffer(wc *browserWSConn, state *browserConnState, viewer string, data []byte, cfg *config.Config) {
	d := state.dedicatedInput()
	request := state.attachmentRequest()
	if d == nil || request.ctx == nil {
		wc.sendCriticalGen(errorStatus("dedicated input requires a dedicated attachment"), dropContext("", viewer, "input-offer-mode"))
		return
	}
	var f generated.BrowserInputOfferFrame
	if message, _ := ValidateInboundFrameJSON("BrowserInputOfferFrame", data); message != "" {
		wc.sendCriticalScopedGen(errorStatus("invalid dedicated input offer"), dropContext("", viewer, "input-offer-invalid"), request.ctx, nil)
		return
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	d.mu.Lock()
	if d.closed || f.InputEpoch <= d.epoch || f.OfferId <= d.offer || f.ControlEpoch != d.control || d.applied != d.control {
		d.mu.Unlock()
		wc.sendCriticalScopedGen(generated.BrowserInputStateFrame{Type: "browser_input_state", SessionId: f.SessionId, InputEpoch: f.InputEpoch, OfferId: f.OfferId, ControlEpoch: f.ControlEpoch, State: "failed", Reason: strPtr("Input offer is stale or control is pending. Retry input.")}, dropContext(f.SessionId, viewer, "input-offer-stale"), request.ctx, nil)
		return
	}
	if d.cancel != nil {
		d.cancel()
	}
	old := d.peer
	source, cancel := context.WithCancel(request.ctx)
	d.epoch, d.offer, d.cancel = f.InputEpoch, f.OfferId, cancel
	d.mu.Unlock()
	if old != nil {
		old.Close()
	}
	h.activeConns.Add(1)
	go func() {
		defer h.activeConns.Done()
		ctx, stop := context.WithTimeout(source, 30*time.Second)
		defer stop()
		current := func() bool { return source.Err() == nil && d.current(f.InputEpoch, f.OfferId) }
		sendState := d.stateSender(wc, request, viewer, f, source)
		if old != nil {
			select {
			case <-old.Closed():
			case <-ctx.Done():
				sendState("Input replacement cleanup timed out.")
				return
			}
		}
		a, err := state.awaitAttachment(ctx, request)
		if err != nil {
			return
		}
		mgr, outcome := h.agentLoop.BrowserManagerForAgent(ctx, f.AgentId, h.sessionWorkspaceID(f.SessionId))
		if outcome != agent.BrowserResolveOK || mgr != a.mgr || a.sessionID != f.SessionId {
			sendState("Input offer does not match the attached browser.")
			cancel()
			return
		}
		if old != nil {
			if releaseErr := mgr.Live().ReleaseInputSourceContext(ctx, a.panelSessionID, old.RetiredSource()); releaseErr != nil {
				sendState("Previous input release failed. Retry input.")
				cancel()
				return
			}
		}
		var queueTiming webrtc.InputQueueTiming
		sink := newWebRTCContextInputSink(true, func() webrtc.InputQueueTiming { return queueTiming })
		route, err := withWebRTCInputRoute(source, mgr, a.panelSessionID, func(origin context.Context, kind string, err error) {
			wc.sendCriticalScopedGen(operationErrorStatus(a.sessionID, fmt.Sprintf("browser input failed: %s", err)), dropContext(a.sessionID, viewer, "dedicated-input-error"), origin, current)
		})
		if err != nil {
			cancel()
			return
		}
		peer, err := d.installPeer(ctx, f.InputEpoch, f.OfferId, func(control int) (*webrtc.DedicatedInputPeer, error) {
			h.mediaLifecycleMu.RLock()
			defer h.mediaLifecycleMu.RUnlock()
			h.mediaConnMu.Lock()
			closed := h.mediaClosed
			h.mediaConnMu.Unlock()
			if closed {
				return nil, errors.New("browser transport is closed")
			}
			return webrtc.NewDedicatedInputPeer(route, webrtc.Config{StunServer: cfg.Tools.Browser.WebRTCStunServer, MediaUDPMux: h.sharedMediaUDPMux(cfg), MediaTCPMux: h.sharedMediaTCPMux(cfg), PublicIPs: resolveWebRTCPublicIPs(cfg)}, f.InputEpoch, control, func(origin context.Context, in generated.BrowserInputFrame) {
				raw, marshalErr := json.Marshal(in)
				if marshalErr == nil {
					sink(origin, viewer, raw)
				}
			}, func(raw []byte) error {
				message, _ := ValidateInboundFrameJSON("BrowserInputFrame", raw)
				if message != "" {
					return errors.New(message)
				}
				return nil
			}, sendState), nil
		})
		if err != nil {
			sendState(err.Error())
			cancel()
			return
		}
		if h.inputTimingEnabled {
			peer.SetQueueTimingObserver(func(_ generated.BrowserInputFrame, timing webrtc.InputQueueTiming) { queueTiming = timing })
		}
		defer func() { peer.Close(); <-peer.Closed() }()
		answer, err := peer.Answer(ctx, f.Sdp)
		if err != nil {
			sendState("Input negotiation failed. Retry input.")
			peer.Close()
			return
		}
		if !wc.sendCriticalScopedGen(generated.BrowserInputAnswerFrame{Type: "browser_input_answer", SessionId: f.SessionId, InputEpoch: f.InputEpoch, OfferId: f.OfferId, ControlEpoch: f.ControlEpoch, Sdp: answer}, dropContext(f.SessionId, viewer, "input-answer"), request.ctx, current) {
			peer.Close()
		}
		stop()
		// Keep shutdown accounting until this peer releases borrowed transports.
		<-peer.Closed()
	}()
}

// Dedicated control admission advances the epoch on socket arrival. The serial
// job then joins old input before applying the command; it never blocks reads.
func (h *BrowserWSHandler) dispatchDedicatedControl(wc *browserWSConn, state *browserConnState, viewer, user string, data []byte, typ string, cfg *config.Config) {
	d := state.dedicatedInput()
	if d == nil {
		return
	}
	request := state.attachmentRequest()
	if request.ctx == nil {
		return
	}
	var f generated.BrowserInputFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	d.mu.Lock()
	observedEpoch := d.epoch
	d.mu.Unlock()
	fail := func(reason string) {
		d.mu.Lock()
		if d.closed || d.epoch != observedEpoch {
			d.mu.Unlock()
			return
		}
		epoch, offer, control := d.epoch, d.offer, d.control
		peer := d.peer
		if d.cancel != nil {
			d.cancel()
		}
		d.mu.Unlock()
		if peer != nil {
			peer.Close()
		}
		session := state.commandAttachment().sessionID
		current := func() bool { d.mu.Lock(); defer d.mu.Unlock(); return !d.closed && d.epoch == observedEpoch }
		acknowledge := epoch == 0 || offer == 0
		// A control refusal must remain distinguishable from transport loss
		// after the client closes its input peer. Echo the failed request's
		// identity, never a newer control; malformed counters stay bounded.
		if f.ControlEpoch != nil && *f.ControlEpoch >= 0 && *f.ControlEpoch <= 9007199254740991 {
			control = *f.ControlEpoch
			acknowledge = true
		}
		if acknowledge {
			wc.sendCriticalScopedGen(generated.BrowserInputControlAckFrame{Type: "browser_input_control_ack", SessionId: session, InputEpoch: epoch, ControlEpoch: control, Ok: false, Reason: &reason}, dropContext(session, viewer, "input-control-failed"), request.ctx, current)
		} else {
			wc.sendCriticalScopedGen(generated.BrowserInputStateFrame{Type: "browser_input_state", SessionId: session, InputEpoch: epoch, OfferId: offer, ControlEpoch: control, State: "failed", Reason: &reason}, dropContext(session, viewer, "input-control-failed"), request.ctx, current)
		}
	}
	schema := wsFrameSchemaName(typ)
	if message, _ := ValidateInboundFrameJSON(schema, data); message != "" {
		fail("Invalid browser control message.")
		return
	}
	if typ == "browser_input" && !inputKindIsDiscrete(f.Kind) {
		fail("This attachment accepts gestures only on its dedicated input connection.")
		return
	}
	d.mu.Lock()
	if d.closed || f.InputEpoch == nil || f.ControlEpoch == nil || *f.InputEpoch != d.epoch || *f.ControlEpoch != d.control+1 {
		d.mu.Unlock()
		fail("Browser control identity is stale. Reconnect input.")
		return
	}
	epoch, next := d.epoch, *f.ControlEpoch
	d.control = next
	peer := d.peer
	var source context.Context
	var done <-chan struct{}
	var err error
	var retired *webrtc.DedicatedInputPeer
	if peer != nil {
		source, done, err = peer.PauseControl(next)
		if err != nil {
			// An admitted replacement may still be joining the canceled old
			// peer. Its retirement belongs to this control too; never cancel
			// the new offer merely because the old queue cannot pause again.
			retiredSource := peer.RetiredSource()
			if retiredSource.Err() != nil {
				retired = peer
				peer.Close()
				source, done, err = retiredSource, peer.Closed(), nil
				peer = nil
			}
		}
	}
	d.mu.Unlock()
	if err != nil {
		fail(err.Error())
		return
	}
	job := browserCommand{navigation: inputKindIsDiscrete(f.Kind), run: func(parent context.Context) {
		failJob := func(reason string) {
			d.failControlUnlessSuperseded(parent, request.ctx, epoch, next, reason, fail)
		}
		a, err := state.awaitAttachment(parent, request)
		if err != nil {
			failJob("Browser attachment was not ready. Retry input.")
			return
		}
		ctx, cancel := a.bindContext(parent)
		defer cancel()
		ack := generated.BrowserInputControlAckFrame{Type: "browser_input_control_ack", SessionId: a.sessionID, InputEpoch: epoch, ControlEpoch: next, Ok: false}
		reply := func() {
			wc.sendCriticalScopedGen(ack, dropContext(a.sessionID, viewer, "input-control-ack"), a.ctx, func() bool { d.mu.Lock(); defer d.mu.Unlock(); return !d.closed && d.epoch == epoch })
		}
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				ack.Reason = strPtr("Input retirement timed out.")
				reply()
				failJob("Input retirement timed out.")
				return
			}
		}
		if source != nil {
			if err = a.mgr.Live().ReleaseInputSourceContext(ctx, a.panelSessionID, source); err != nil {
				ack.Reason = strPtr(err.Error())
				reply()
				failJob("Input release failed.")
				return
			}
		}
		if retired != nil {
			d.mu.Lock()
			if d.peer == retired {
				d.peer = nil
			}
			d.mu.Unlock()
		}
		ok := runBrowserInputControl(ctx, func(operation context.Context) {
			switch typ {
			case "browser_input":
				h.handleInputContext(operation, wc, state, a, viewer, data)
			case "browser_control":
				h.handleControlContext(operation, wc, state, a, viewer, user, data, cfg)
			case "browser_tab_action":
				h.handleTabActionContext(operation, wc, state, a, viewer, data)
			case "browser_viewport":
				h.handleViewportContext(operation, wc, state, a, viewer, data)
			}
		})
		if ok {
			cs := a.mgr.CaptureSessionForPanel(a.panelSessionID)
			if cs != nil {
				if typ == "browser_tab_action" {
					err = a.mgr.Live().RefreshCaptureFrameContext(ctx, a.panelSessionID, cs)
					ok = err == nil
				}
				frame := cs.FrameState()
				if frame.CaptureID != "" {
					generation := int(frame.Generation)
					ack.CaptureId = &frame.CaptureID
					ack.CaptureGeneration = &generation
				}
			}
		}
		d.mu.Lock()
		if d.closed || d.epoch != epoch {
			d.mu.Unlock()
			return
		}
		if ok {
			if d.control == next && peer != nil {
				err = peer.ResumeControl(next)
				ok = err == nil
			}
			if ok {
				d.applied = next
				d.notifyControlChangedLocked()
			}
		}
		d.mu.Unlock()
		ack.Ok = ok
		if !ok {
			ack.Reason = strPtr("Browser control failed. Retry input.")
		}
		reply()
		if !ok {
			failJob("Browser control failed. Retry input.")
		}
	}, onDiscard: func() { fail("Browser control was canceled. Retry input.") }}
	if !state.commands.submit(&h.activeConns, job) {
		fail("Browser control queue is full. Retry input.")
	}
}

// A newer navigation cancels the active command through the serial queue.
// That older cancellation must not retire the peer needed by its successor.
// Deadlines, actual refusals, and cancellation of the latest command still fail.
func (d *browserDedicatedInput) failControlUnlessSuperseded(operation, attachment context.Context, epoch, control int, reason string, fail func(string)) {
	if operation.Err() == context.Canceled && attachment.Err() == nil {
		d.mu.Lock()
		superseded := !d.closed && d.epoch == epoch && d.control > control
		d.mu.Unlock()
		if superseded {
			return
		}
	}
	fail(reason)
}

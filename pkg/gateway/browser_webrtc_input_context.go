package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

type webRTCInputRouteKey struct{}
type disabledMediaInputKey struct{}

type webRTCInputRoute struct {
	manager        *browser.BrowserManager
	panelSessionID string
	attachment     context.Context
	report         func(context.Context, string, error)
}

// withWebRTCInputRoute stores the original connection's immutable input route.
// The report callback must remain bound to that connection and recheck its
// own send admission; it must not resolve a replacement by viewer ID.
func withWebRTCInputRoute(parent context.Context, mgr *browser.BrowserManager, panelSessionID string, report func(context.Context, string, error)) (context.Context, error) {
	if parent == nil {
		return nil, fmt.Errorf("browser input route: missing attachment context")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if mgr == nil || panelSessionID == "" {
		return nil, fmt.Errorf("browser input route: missing manager or panel session")
	}
	route := webRTCInputRoute{manager: mgr, panelSessionID: panelSessionID, attachment: parent, report: report}
	return context.WithValue(parent, webRTCInputRouteKey{}, route), nil
}

func newWebRTCContextInputSink(validateInbound bool, enqueued ...func() webrtc.InputQueueTiming) webrtc.ContextInputSink {
	return newWebRTCContextInputSinkWithSampling(validateInbound, nil, enqueued...)
}

func newWebRTCContextInputSinkWithSampling(validateInbound bool, sampling *browserInputTimingSampling, enqueued ...func() webrtc.InputQueueTiming) webrtc.ContextInputSink {
	return newWebRTCContextInputSinkWithDispatchSampling(validateInbound, sampling, func(ctx context.Context, mgr *browser.BrowserManager, panel, viewer string, in browser.LiveInput) error {
		return mgr.Live().InputContext(ctx, panel, viewer, in)
	}, enqueued...)
}

// The dispatch function is the existing browser-input module boundary. Route,
// validation and source ownership remain in this gateway adapter.
func newWebRTCContextInputSinkWithDispatch(validateInbound bool, dispatch func(context.Context, *browser.BrowserManager, string, string, browser.LiveInput) error, enqueued ...func() webrtc.InputQueueTiming) webrtc.ContextInputSink {
	return newWebRTCContextInputSinkWithDispatchSampling(validateInbound, nil, dispatch, enqueued...)
}

func newWebRTCContextInputSinkWithDispatchSampling(validateInbound bool, sampling *browserInputTimingSampling, dispatch func(context.Context, *browser.BrowserManager, string, string, browser.LiveInput) error, enqueued ...func() webrtc.InputQueueTiming) webrtc.ContextInputSink {
	timingEnabled := os.Getenv("OMNIPUS_BROWSER_INPUT_TIMING") == "1"
	if sampling == nil {
		sampling = &browserInputTimingSampling{}
	}
	return func(ctx context.Context, viewerID string, raw []byte) {
		if ctx == nil || ctx.Err() != nil {
			return
		}
		if disabled, _ := ctx.Value(disabledMediaInputKey{}).(bool); disabled {
			return
		}
		route, ok := ctx.Value(webRTCInputRouteKey{}).(webRTCInputRoute)
		if !ok || route.attachment == nil || route.attachment.Err() != nil {
			return
		}
		if validateInbound {
			if message, _ := ValidateInboundFrameJSON("BrowserInputFrame", raw); message != "" {
				slog.Debug("browser-webrtc: dropping invalid input data-channel frame", "viewer_id", viewerID, "error", message)
				return
			}
		}
		var frame generated.BrowserInputFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			slog.Warn("browser-webrtc: dropping malformed input data-channel frame", "viewer_id", viewerID, "error", err)
			return
		}
		var probe *browserInputTiming
		if timingEnabled {
			received := time.Now()
			var queued webrtc.InputQueueTiming
			if len(enqueued) > 0 {
				queued = enqueued[0]()
				if !queued.EnqueuedAt.IsZero() {
					received = queued.EnqueuedAt
				}
			}
			probe = sampling.begin(frame, received)
			// The merged wire frame keeps the first reliable sequence. The observer
			// alone supplies the complete range; it never changes input authorization.
			if probe != nil && queued.FirstReliableSeq == probe.reliableSeq && queued.LastReliableSeq >= queued.FirstReliableSeq && int64(queued.LastReliableSeq) <= maxBrowserCounter && queued.InputCount == queued.LastReliableSeq-queued.FirstReliableSeq+1 {
				probe.firstReliableSeq, probe.lastReliableSeq, probe.inputCount = queued.FirstReliableSeq, queued.LastReliableSeq, queued.InputCount
			}
			probe.mark("queue_started")
			defer probe.finish()
		}
		in := browserInputFrameToLiveInput(frame)
		if probe != nil {
			in.Timing = &browser.LiveInputTimingObserver{Observe: probe.mark, ObserveBudget: probe.observeBudget}
		}
		in.SourceContext = ctx
		err := dispatch(ctx, route.manager, route.panelSessionID, viewerID, in)
		if probe != nil {
			probe.outcome = browserTimingOutcome(err)
			if browser.IsBenignLiveInputError(err) {
				probe.benignReason = browserTimingBenignReason(err.Error())
			}
		}
		if err == nil || ctx.Err() != nil || route.attachment.Err() != nil {
			return
		}
		if browser.IsBenignLiveInputError(err) {
			slog.Debug("browser-webrtc: input rejected (benign)", "viewer_id", viewerID, "error", err)
			return
		}
		slog.Warn("browser-webrtc: input dispatch failed", "viewer_id", viewerID, "error", err)
		if route.report != nil {
			route.report(ctx, frame.Kind, err)
		}
	}
}

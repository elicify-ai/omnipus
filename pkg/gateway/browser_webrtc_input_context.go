package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

type webRTCInputRouteKey struct{}

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

func newWebRTCContextInputSink(validateInbound bool) webrtc.ContextInputSink {
	return newWebRTCContextInputSinkWithDispatch(validateInbound, func(ctx context.Context, mgr *browser.BrowserManager, panel, viewer string, in browser.LiveInput) error {
		return mgr.Live().InputContext(ctx, panel, viewer, in)
	})
}

// The dispatch function is the existing browser-input module boundary. Route,
// validation and source ownership remain in this gateway adapter.
func newWebRTCContextInputSinkWithDispatch(validateInbound bool, dispatch func(context.Context, *browser.BrowserManager, string, string, browser.LiveInput) error) webrtc.ContextInputSink {
	return func(ctx context.Context, viewerID string, raw []byte) {
		if ctx == nil || ctx.Err() != nil {
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
		in := browserInputFrameToLiveInput(frame)
		in.SourceContext = ctx
		err := dispatch(ctx, route.manager, route.panelSessionID, viewerID, in)
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

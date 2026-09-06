package gateway

// Live-browser video-health fan-out (issue #674).
//
// The gateway learns that the capture's video feed died the instant Pion hands
// it a terminal PeerConnection state. The SPA used to learn only by exhausting
// its own first-frame timeout, so the panel showed "Connecting…" for tens of
// seconds after the answer was already known. This file closes that gap: one
// browser_video_health frame per transition, pushed to every viewer attached
// to the capture, the moment the transition happens.
//
// It is deliberately NOT a variant of browser_webrtc_state. That frame is
// per-viewer and describes signalling (may you offer, are you negotiated, what
// ICE servers do you get). This one describes the SHARED upstream capture and
// goes to every viewer of it, which is also why it carries the bounded
// recovery's attempt counters — the panel can say "reconnecting, attempt 2 of
// 3" instead of implying a retry that never ends.
//
// ADR-061 rule, restated because this file is exactly where it would be
// violated: WebRTC is the only live-browser video path. An unrecoverable state
// is a terminal, named failure that MUST reach the user. Nothing here may
// quietly substitute another stream or swallow the event.

import (
	"errors"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// onVideoHealth is the observer registered on every BrowserManager this
// handler attaches to (see handleAttach's SetVideoHealthObserver call, and
// BrowserManager.EnsureCaptureSession, which installs it on each
// CaptureSession). It runs on whichever goroutine observed the transition —
// a Pion callback, or the recovery state machine's timer — so it must do
// nothing but format and send.
func (h *BrowserWSHandler) onVideoHealth(ev browser.VideoHealthEvent) {
	// Logged at the level the state actually deserves: an unrecoverable video
	// path is an operator-visible fault, and a recovery is news worth having
	// in the log next to the loss it answers.
	logAttrs := []any{
		"agent_id", ev.AgentID,
		"state", string(ev.State),
		"attempt", ev.Attempt,
		"max_attempts", ev.MaxAttempts,
		"viewers", len(ev.ViewerIDs),
		"detail", ev.Detail,
	}
	switch ev.State {
	case browser.VideoHealthUnrecoverable:
		slog.Error("browser-video: live video is unrecoverable — automatic recapture budget exhausted", logAttrs...)
	case browser.VideoHealthLost, browser.VideoHealthRecovering:
		slog.Warn("browser-video: live video feed interrupted", logAttrs...)
	default:
		slog.Info("browser-video: live video health changed", logAttrs...)
	}

	frame := generated.BrowserVideoHealthFrame{
		Type:  string(generated.WsFrameTypeBrowserVideoHealth),
		State: string(ev.State),
	}
	if ev.Attempt > 0 {
		attempt := ev.Attempt
		frame.Attempt = &attempt
	}
	if ev.MaxAttempts > 0 {
		maxAttempts := ev.MaxAttempts
		frame.MaxAttempts = &maxAttempts
	}
	// Reuse the browser_webrtc_state redactor rather than writing a second
	// one: it whitespace-collapses, strips labelled secrets / bearer tokens /
	// bare long hex runs (the capture token's shape) and cuts to the schema's
	// length bound on a rune boundary. A parallel implementation here would be
	// one more place for a capture token to leak into a browser.
	if detail := webrtcReasonDetail(errors.New(ev.Detail)); ev.Detail != "" && detail != "" {
		frame.Detail = &detail
	}

	for _, viewerID := range ev.ViewerIDs {
		v, ok := h.viewerConns.Load(viewerID)
		if !ok {
			// The viewer detached between the snapshot and now. Normal, and
			// nothing to do: a viewer that is gone needs no telling.
			continue
		}
		vc, ok := v.(*webrtcViewerConn)
		if !ok {
			slog.Warn("browser-video: viewer registry held an unexpected value; skipping",
				"viewer_id", viewerID, "agent_id", ev.AgentID)
			continue
		}
		f := frame // copy per viewer: SessionId differs
		sessionID := vc.sessionID
		f.SessionId = &sessionID
		vc.wc.sendCriticalGen(f, dropContext(sessionID, viewerID, "video-health:"+string(ev.State)))
	}
}

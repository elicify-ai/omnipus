package gateway

import (
	"log/slog"

	"github.com/pion/ice/v4"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func (h *BrowserWSHandler) sharedMediaUDPMux(cfg *config.Config) ice.UDPMux {
	if h.sharedMediaConn(cfg) == nil {
		return nil
	}
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	return h.mediaUDPMux
}

func (h *BrowserWSHandler) sharedMediaTCPMux(cfg *config.Config) ice.TCPMux {
	if h.sharedMediaTCP(cfg) == nil {
		return nil
	}
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	return h.mediaTCPMux
}

// closeMediaTransport is gateway shutdown, not a viewer detach or config reload.
// Fence new captures, stop existing sessions, then close the shared readers and
// their underlying transports. Session.Close only removes its ICE registrations.
func (h *BrowserWSHandler) closeMediaTransport() {
	h.mediaLifecycleMu.Lock()
	defer h.mediaLifecycleMu.Unlock()
	h.mediaConnMu.Lock()
	if h.mediaClosed {
		h.mediaConnMu.Unlock()
		return
	}
	h.mediaClosed = true
	udp, tcp := h.mediaUDPMux, h.mediaTCPMux
	h.mediaConnMu.Unlock()
	if h.captures != nil {
		for cs := range h.captures.otherSessions("") {
			cs.Stop()
		}
	}
	if udp != nil {
		if err := udp.Close(); err != nil {
			slog.Warn("browser-webrtc: close shared UDP mux", "error", err)
		}
	}
	if tcp != nil {
		if err := tcp.Close(); err != nil {
			slog.Warn("browser-webrtc: close shared TCP mux", "error", err)
		}
	}
}

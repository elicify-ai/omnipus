package gateway

import (
	"fmt"
	"strings"
)

// mediaTransportNotice combines all active causes in one status: the panel
// replaces its previous status, and the wire contract limits messages to 512.
func (h *BrowserWSHandler) mediaTransportNotice() string {
	h.mediaConnMu.Lock()
	defer h.mediaConnMu.Unlock()
	var causes []string
	if fallback := h.mediaPortFallback; fallback != nil {
		if fallback.bound > 0 {
			causes = append(causes, fmt.Sprintf("UDP port %d is unavailable; using port %d, which remote viewers may not reach", fallback.configured, fallback.bound))
		} else {
			causes = append(causes, fmt.Sprintf("UDP ports %d–%d are unavailable; using a random UDP port that remote viewers may not reach", fallback.configured, fallback.lastProbed))
		}
	}
	if h.mediaTCPBindErr != nil {
		causes = append(causes, "the configured TCP media port is unavailable")
	}
	if h.turnStartErr != nil {
		causes = append(causes, "the configured TURN relay could not start")
	}
	if len(causes) == 0 {
		return ""
	}
	return "Live video connection needs attention: " + strings.Join(causes, "; ") + ". Free the configured ports or update the configuration, then restart."
}

package gateway

import "github.com/elicify-ai/omnipus/pkg/api/generated"

// SetSystemNotice carries an already-persisted harness message to Finalize.
// The ordinary replay-message path renders it live and after reload alike;
// it must never become assistant tokens or inherit the provider's model.
func (s *wsStreamer) SetSystemNotice(frame generated.ReplayMessageFrame) {
	s.statsMu.Lock()
	s.systemNotice = &frame
	s.statsMu.Unlock()
}

func (wsf *wsStreamerFinalize) publishSystemNotice() {
	if wsf.shadow || wsf.systemNotice == nil {
		return
	}
	if h := wsf.s.wsHandler(); h != nil && wsf.s.sessionID != "" {
		h.hubPublishFrame(wsf.s.sessionID, string(generated.WsFrameTypeReplayMessage), *wsf.systemNotice, nil)
	}
}

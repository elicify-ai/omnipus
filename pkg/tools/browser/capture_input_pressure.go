package browser

import "time"

func (cs *CaptureSession) noteInputPressure(kind string, elapsed time.Duration, now time.Time) bool {
	// Navigation completion includes ordinary page loading. Only interactive
	// Chrome command latency is useful as a video-load pressure signal.
	switch kind {
	case "key_down", "key_up", "text", "mouse_move", "mouse_down", "mouse_up", "wheel", "click":
	default:
		return false
	}
	if elapsed < 100*time.Millisecond {
		return false
	}
	cs.mu.Lock()
	if cs.stopped || cs.ingestSend == nil || cs.inputPressureSending || !cs.inputPressureLastAt.IsZero() && now.Sub(cs.inputPressureLastAt) < time.Second {
		cs.mu.Unlock()
		return false
	}
	send := cs.ingestSend
	epoch := cs.ingestEpoch
	cs.inputPressureLastAt = now
	cs.inputPressureSending = true
	cs.mu.Unlock()
	// Capture signaling must never block the input gate. One in-flight send
	// plus the one-second limit bounds work even if the extension is stalled.
	go func() {
		cs.mu.Lock()
		if cs.stopped || cs.ingestEpoch != epoch {
			cs.inputPressureSending = false
			cs.mu.Unlock()
			return
		}
		cs.mu.Unlock()
		err := send("input_pressure", nil, 0, 0, 0)
		cs.mu.Lock()
		cs.inputPressureSending = false
		cs.mu.Unlock()
		if cs.logf == nil {
			return
		}
		if err != nil {
			cs.logf("capture[%s]: input pressure signal failed: %v", cs.agentID, err)
		} else {
			cs.logf("capture[%s]: input pressure signal sent (Chrome input %s)", cs.agentID, elapsed.Round(time.Millisecond))
		}
	}()
	return true
}

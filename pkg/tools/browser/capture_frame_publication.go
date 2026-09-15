package browser

import "slices"

const VideoHealthTransitioning VideoHealthState = "transitioning"

// claimFramePublicationLocked unifies frame boundaries and health ordering.
// Caller holds cs.mu and delivers the returned event only after unlocking.
func (cs *CaptureSession) claimFramePublicationLocked() VideoHealthEvent {
	frame := cs.frameStateLocked()
	if cs.stopped || frame.Generation == 0 || frame.TargetID == "" {
		return VideoHealthEvent{}
	}
	state, attempt, detail := VideoHealthTransitioning, 0, ""
	previous := cs.videoHealthLatest
	if previous.Version != 0 && previous.Version == cs.videoHealthVersion && sameCaptureFrameIdentity(previous.Frame, frame) {
		switch previous.State {
		case VideoHealthLost, VideoHealthRecovering, VideoHealthUnrecoverable:
			state, attempt, detail = previous.State, previous.Attempt, previous.Detail
		}
	}
	if state == VideoHealthTransitioning && frame.Ready {
		state = VideoHealthRecovered
	}
	return cs.videoHealthEventLocked(state, attempt, detail)
}

// CurrentVideoHealthEvent returns a non-advancing snapshot for a new viewer.
func (cs *CaptureSession) CurrentVideoHealthEvent() (VideoHealthEvent, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	event := cs.videoHealthLatest
	if cs.stopped || cs.videoHealthExhausted || event.Version == 0 || event.Version != cs.videoHealthVersion || event.Frame.Generation == 0 || !sameCaptureFrameIdentity(event.Frame, cs.frameStateLocked()) {
		return VideoHealthEvent{}, false
	}
	return cloneVideoHealthEvent(event), true
}

func cloneVideoHealthEvent(event VideoHealthEvent) VideoHealthEvent {
	event.ViewerIDs = slices.Clone(event.ViewerIDs)
	return event
}

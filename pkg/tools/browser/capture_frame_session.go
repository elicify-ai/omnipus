package browser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// CaptureFrameState binds a displayed picture to one capture, target and layout.
// It is an internal event; the gateway maps it into generated wire contracts.
type CaptureFrameState struct { // not-wire-format: internal capture observer event.
	CaptureID  string
	Generation uint64
	TargetID   string
	Width      int
	Height     int
	Scale      float64
	Timestamp  uint32
	Ready      bool
}

func (cs *CaptureSession) BeginFrameTransition(targetID string, width, height int, scale float64) (CaptureFrameState, error) {
	cs.mu.Lock()
	if cs.stopped {
		state := cs.frameStateLocked()
		cs.mu.Unlock()
		return state, fmt.Errorf("capture session is stopped")
	}
	before := cs.frames.snapshot().Generation
	frame, err := cs.frames.begin(captureFrameGeometry{TargetID: targetID, Width: width, Height: height, Scale: scale})
	var event VideoHealthEvent
	if err == nil && frame.Generation != before {
		if cs.documentTransition != nil && targetID != cs.documentTransition.targetID {
			cs.retireDocumentTransitionLocked()
		}
		cs.replaceFrameLifetimeLocked()
		cs.resetCaptureHealthForFrameLocked()
		event = cs.claimFramePublicationLocked()
	}
	state := cs.frameStateLocked()
	fn := cs.onFrameState
	cs.mu.Unlock()
	if event.Version != 0 {
		cs.emitVideoHealth(event)
	}
	if err == nil && frame.Generation != before && fn != nil {
		fn(state)
	}
	return state, err
}

func (cs *CaptureSession) FrameState() CaptureFrameState {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.frameStateLocked()
}

func (cs *CaptureSession) SetOnFrameState(fn func(CaptureFrameState)) {
	cs.mu.Lock()
	cs.onFrameState = fn
	state := cs.frameStateLocked()
	cs.mu.Unlock()
	if fn != nil && state.Generation != 0 {
		fn(state)
	}
}

func (cs *CaptureSession) CommitFrameBoundary(generation uint64, targetID string, timestamp uint32) bool {
	cs.mu.Lock()
	if cs.stopped || cs.documentPendingLocked() || !cs.frames.commit(generation, targetID, timestamp) {
		cs.mu.Unlock()
		return false
	}
	state := cs.frameStateLocked()
	fn := cs.onFrameState
	event := cs.claimFramePublicationLocked()
	cs.mu.Unlock()
	if event.Version != 0 {
		cs.emitVideoHealth(event)
	}
	if fn != nil {
		fn(state)
	}
	return true
}

func (cs *CaptureSession) AcceptsInputGeneration(captureID string, generation uint64) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return !cs.stopped && !cs.documentPendingLocked() && captureID != "" && captureID == cs.frameStateLocked().CaptureID && cs.frames.accepts(generation)
}

// frameStateLocked derives a public identity from the per-capture random token.
// The digest is not an ingest credential: authentication still requires the
// original token. Keeping it stable lets viewers detect capture replacement.
// Caller holds cs.mu; observer callbacks must always run after releasing it.
func (cs *CaptureSession) frameStateLocked() CaptureFrameState {
	frame := cs.frames.snapshot()
	if cs.documentTransition != nil {
		frame.Geometry.Width, frame.Geometry.Height, frame.Ready = 0, 0, false
	}
	digest := sha256.Sum256(cs.token)
	return CaptureFrameState{
		CaptureID:  hex.EncodeToString(digest[:]),
		Generation: frame.Generation,
		TargetID:   frame.Geometry.TargetID,
		Width:      frame.Geometry.Width,
		Height:     frame.Geometry.Height,
		Scale:      frame.Geometry.Scale,
		Timestamp:  frame.Timestamp,
		Ready:      frame.Ready && !cs.stopped,
	}
}

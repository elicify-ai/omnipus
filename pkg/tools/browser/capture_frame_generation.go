package browser

import (
	"fmt"
	"math"
	"strings"
	"sync"
)

// captureFrameGeometry identifies the page and CSS layout represented by video.
type captureFrameGeometry struct { // not-wire-format: internal capture identity.
	TargetID string
	Width    int
	Height   int
	Scale    float64
}

type captureFrameSnapshot struct { // not-wire-format: internal generation state.
	Generation uint64
	Geometry   captureFrameGeometry
	Timestamp  uint32
	Ready      bool
}

type captureFrameTracker struct { // not-wire-format: capture-local state machine.
	mu      sync.Mutex
	current captureFrameSnapshot
}

func (f *captureFrameTracker) begin(geometry captureFrameGeometry) (captureFrameSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.TrimSpace(geometry.TargetID) == "" || len(geometry.TargetID) > 128 ||
		geometry.Width < 0 || geometry.Height < 0 || geometry.Width > 16384 || geometry.Height > 16384 ||
		(geometry.Width == 0) != (geometry.Height == 0) ||
		math.IsNaN(geometry.Scale) || geometry.Scale < 1 || geometry.Scale > 4 {
		return f.current, fmt.Errorf("invalid capture target or geometry")
	}
	if f.current.Generation != 0 && f.current.Geometry == geometry {
		return f.current, nil
	}
	// Generations cross JavaScript and Go. Never wrap or exceed the exact
	// integer range, because either would revive a stale viewer's claim.
	if f.current.Generation >= 9007199254740991 {
		return f.current, fmt.Errorf("capture generation exhausted; replace capture session")
	}
	f.current = captureFrameSnapshot{Generation: f.current.Generation + 1, Geometry: geometry}
	return f.current, nil
}

func (f *captureFrameTracker) commit(generation uint64, targetID string, timestamp uint32) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if generation == 0 || generation != f.current.Generation || targetID != f.current.Geometry.TargetID || f.current.Geometry.Width == 0 {
		return false
	}
	if f.current.Ready && int32(timestamp-f.current.Timestamp) < 0 {
		return false
	}
	f.current.Timestamp = timestamp
	f.current.Ready = true
	return true
}

func (f *captureFrameTracker) accepts(generation uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return generation != 0 && generation == f.current.Generation && f.current.Ready
}

func (f *captureFrameTracker) snapshot() captureFrameSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

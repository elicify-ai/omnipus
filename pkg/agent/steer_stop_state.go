package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// publishCurrentStoppedState projects only a winning, still-current landing.
// Historical stopped-child notice delivery never calls this publisher.
func (al *AgentLoop) publishCurrentStoppedState(landed *session.LifecycleRecord) error {
	if landed == nil || landed.SteeredBy == nil {
		return nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return fmt.Errorf("steer: stopped state publication: lifecycle store is not wired")
	}
	mu := lifecycle.Lock(landed.SessionID)
	mu.Lock()
	defer mu.Unlock()
	current, readErr := lifecycle.LoadLocked(landed.SessionID)
	if readErr != nil {
		return fmt.Errorf("steer: stopped state publication: read current session: %w", readErr)
	}
	if !sameStoppedLanding(landed, current) {
		return nil // A Resume or newer landing owns the current display.
	}
	// Keep validation and both durable/live projections in the same owning
	// lock hold. No provider, wake or model call is made by the state builder.
	return al.deliverSubagentState(current.SteeringSessionID(), current, string(session.LifecycleStopped), nil)
}

func sameStoppedLanding(landed, current *session.LifecycleRecord) bool {
	if landed == nil || current == nil || landed.SessionID != current.SessionID || landed.Generation != current.Generation ||
		current.State != session.LifecycleStopped || current.Stop != nil || landed.StopNote == nil || current.StopNote == nil {
		return false
	}
	if landed.StopNote.Seq != current.StopNote.Seq || landed.StopNote.Cause != current.StopNote.Cause ||
		landed.StopNote.By != current.StopNote.By || !landed.StopNote.At.Equal(current.StopNote.At) {
		return false
	}
	if (landed.ExecutionID == nil) != (current.ExecutionID == nil) ||
		(landed.ExecutionID != nil && *landed.ExecutionID != *current.ExecutionID) {
		return false
	}
	return (landed.StopEffect == nil && current.StopEffect == nil) ||
		(landed.StopEffect != nil && current.StopEffect != nil && *landed.StopEffect == *current.StopEffect)
}

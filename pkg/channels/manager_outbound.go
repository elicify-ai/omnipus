package channels

// Manager read accessors of the channels spec (channels-spec-r8, T-7,
// FR-044, FR-051). SetPolicySource is still the WP-0 shell (WP-2).

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Availability reports whether instance id is running, from the manager's
// existing records only (T-7, FR-051): running = registered (GetChannel) and
// IsRunning; not_running = registered but not running, reason
// LastStartError's text or "not started"; not_loaded = not registered,
// reason the FailedChannels entry for id or "not loaded".
//
// It takes m.mu.RLock (through those accessors), so it is for the
// send_message tool, the Channels block, GET and Configure's outcome only —
// never the dispatch hook or a worker (B-01).
func (m *Manager) Availability(id string) Availability {
	if ch, registered := m.GetChannel(id); registered {
		if ch.IsRunning() {
			return Availability{State: AvailabilityRunning}
		}
		if err := m.LastStartError(id); err != nil {
			return Availability{State: AvailabilityNotRunning, Reason: err.Error()}
		}
		return Availability{State: AvailabilityNotRunning, Reason: "not started"}
	}
	failed := m.FailedChannels()
	// The newest entry for id is the current construction failure. It is
	// rendered with the instance key, not ChannelInitError.Error()'s display
	// name, which is the same for every instance of a type ("line").
	for i := len(failed) - 1; i >= 0; i-- {
		if failed[i].Name == id && failed[i].Err != nil {
			return Availability{State: AvailabilityNotLoaded,
				Reason: fmt.Sprintf("channel %q: %v", failed[i].Name, failed[i].Err)}
		}
	}
	return Availability{State: AvailabilityNotLoaded, Reason: "not loaded"}
}

// SetPolicySource wires the live policy source the dispatch and dequeue
// start checks read (FR-044); gateway setup passes agentLoop.GetConfig. The
// source must never block on a lock held while Reload waits for workers.
//
// WP-0 shell: stores nothing.
func (m *Manager) SetPolicySource(src func() *config.Config) {
}

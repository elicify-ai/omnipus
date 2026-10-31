package channels

// Channels package WP-0 shells on Manager (channels-spec-r8, T-7, FR-044,
// FR-051). Signatures only; nothing in production calls them yet.

import (
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Availability reports whether instance id is running, from the manager's
// existing records only (T-7, FR-051): running = registered (GetChannel) and
// IsRunning; not_running = registered but not running, reason
// LastStartError's text or "not started"; not_loaded = not registered,
// reason the FailedChannels entry for id or "not loaded".
//
// It takes m.mu.RLock (through those accessors), so it is for the
// send_message tool, the Channels block and GET only — never the dispatch
// hook or a worker (B-01).
//
// WP-0 shell: returns the zero Availability.
func (m *Manager) Availability(id string) Availability {
	return Availability{}
}

// SetPolicySource wires the live policy source the dispatch and dequeue
// start checks read (FR-044); gateway setup passes agentLoop.GetConfig. The
// source must never block on a lock held while Reload waits for workers.
//
// WP-0 shell: stores nothing.
func (m *Manager) SetPolicySource(src func() *config.Config) {
}

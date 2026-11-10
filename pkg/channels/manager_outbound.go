package channels

// Manager read accessors of the channels spec (channels-spec-r8, T-7,
// FR-044, FR-051) and the dispatch/dequeue start check (FR-044).

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Availability reports whether instance id is running, from the manager's
// existing records only (T-7, FR-051): running = registered (GetChannel) and
// IsRunning; not_running = registered but not running, reason the channel's
// own NotRunningReason when it has one (#1687: a WhatsApp connection being
// re-established), else LastStartError's text or "not started"; not_loaded = not registered,
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
		if r, ok := ch.(NotRunningReasoner); ok {
			if reason := r.NotRunningReason(); reason != "" {
				return Availability{State: AvailabilityNotRunning, Reason: reason}
			}
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
// nil unwires it: every start is then refused (fail closed).
func (m *Manager) SetPolicySource(src func() *config.Config) {
	if src == nil {
		m.policySource.Store(nil)
		return
	}
	m.policySource.Store(&src)
}

// The two stages of the FR-044 start check.
const (
	startStageDispatch = "dispatch"
	startStageDequeue  = "dequeue"
)

// startRefusalLog is the log message of each stage's refusal.
var startRefusalLog = map[string]string{
	startStageDispatch: "refused a start at dispatch",
	startStageDequeue:  "refused a queued start",
}

// startMayProceed runs StartAllowed (T-3) for an Operation=start message at
// stage, on the policy source's snapshot and the availability of ch, the
// worker's channel (FR-044). It takes no manager lock: no GetChannel,
// configSnapshot or Availability here. A refusal is one info line with the
// stage, instance, agent, target and code, never the text and never a chat
// notice; the caller drops the message.
func (m *Manager) startMayProceed(msg bus.OutboundMessage, ch Channel, stage string) bool {
	var policy *config.Config
	if src := m.policySource.Load(); src != nil {
		policy = (*src)()
	}
	avail := Availability{State: AvailabilityNotRunning, Reason: "not running"}
	if ch != nil && ch.IsRunning() {
		avail = Availability{State: AvailabilityRunning}
	}
	decision := StartAllowed(policy, avail, msg.Channel, msg.ChatID, msg.WorkspaceID, msg.AgentID)
	if decision.Allowed {
		return true
	}
	fields := map[string]any{
		"stage":    stage,
		"instance": msg.Channel,
		"agent_id": msg.AgentID,
		"target":   msg.ChatID,
		"code":     string(decision.Code),
	}
	if policy == nil {
		fields["policy_source"] = "not wired"
	}
	logger.InfoCF("channels", startRefusalLog[stage], fields)
	return false
}

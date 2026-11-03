package agent

// send_message's start rule (channels-spec-r8 T-3, FR-041): the tool's
// StartPolicy, fed by the live policy source (GetConfig, the same
// configuration dispatch reads) and Manager.Availability.

import (
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// messageStartPolicy implements tools.StartPolicy over the live AgentLoop,
// read on every call so a reload or an outbound save takes effect at once.
type messageStartPolicy struct{ al *AgentLoop }

// IsConnector reports T-4 step 3: webchat, system and internal channels are
// not connectors.
func (p messageStartPolicy) IsConnector(channel string) bool { return isConnectorChannel(channel) }

// CheckStart runs StartAllowed (T-3) on the live configuration and the
// manager's availability for channel. No manager means nothing is loaded.
func (p messageStartPolicy) CheckStart(channel, chatID, workspaceID, agentID string) tools.StartVerdict {
	cfg := p.al.GetConfig()
	avail := channels.Availability{State: channels.AvailabilityNotLoaded, Reason: "not loaded"}
	if mgr := p.al.GetChannelManager(); mgr != nil {
		avail = mgr.Availability(channel)
	}
	decision := channels.StartAllowed(cfg, avail, channel, chatID, workspaceID, agentID)
	if !decision.Allowed {
		return tools.StartVerdict{Text: decision.Text}
	}
	// StartAllowed passed, so the instance exists and chatID canonicalizes
	// to a listed target (T-3 rows a and d).
	canonical, _ := channels.CanonicalizeTarget(channels.InstancePlatform(channel, cfg.Channels[channel]), chatID)
	return tools.StartVerdict{Allowed: true, BareChatID: channels.BareTargetID(canonical)}
}

// busOperation maps the tool's recorded decision onto the bus field
// (FR-043); anything else is a system send ("").
func busOperation(op string) string {
	switch op {
	case tools.SendOperationChecked:
		return bus.OperationChecked
	case tools.SendOperationStart:
		return bus.OperationStart
	default:
		return ""
	}
}

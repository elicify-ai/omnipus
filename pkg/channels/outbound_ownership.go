// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// outboundOwnershipRefusals counts sends refused because the originating
// (workspace, agent) pair does not own the target instance (ADR-065 FR-6).
//
// Deliberately SEPARATE from any transport-failure counter: "the network ate
// it" and "an agent tried to speak through a channel that is not its own" are
// different events, and collapsing them would bury the second in the noise of
// the first.
var outboundOwnershipRefusals atomic.Int64

// OutboundOwnershipRefusals reports how many agent-originated sends dispatch
// has refused on ownership grounds.
func OutboundOwnershipRefusals() int64 { return outboundOwnershipRefusals.Load() }

// allowAgentOriginatedSend is the dispatch-time re-check (ADR-065 FR-7).
//
// # Why a second check exists at all
//
// The tool layer is the real enforcement (spec FR-1): send_message refuses a
// channel the acting agent does not own. But that safety property moved from
// UNREPRESENTABLE to VALIDATED when the operator ruled the agent must keep
// naming its own channel — and a validated property holds only while every
// path from a send to the bus passes the same check. A second route added
// later would void it silently, with no test failing.
//
// So this sits at the last common point before the wire and asks the same
// question again. If it ever fires, something upstream regressed and the WARN
// says which agent, which workspace and which instance.
//
// # Scope, stated rather than emergent
//
// Applies ONLY to messages carrying a non-empty AgentID — i.e. sends that came
// from send_message. The ~19 system producers (streamed replies, notifyDrop
// backpressure, schedule delivery, device notifications) carry no AgentID, are
// not model-addressable, and pass unchecked. That exemption is by enumeration,
// not by accident of an empty field.
//
// Unbound instances and webchat are unowned and therefore unrestricted, exactly
// as at the tool layer: webchat is SHARED by operator decision.
func allowAgentOriginatedSend(cfg *config.Config, msg bus.OutboundMessage) bool {
	instanceID, agentID, workspaceID := msg.Channel, msg.AgentID, msg.WorkspaceID
	if agentID == "" {
		return true // system-originated; not in scope
	}
	// The send tool already applied the rule, with the turn context this layer
	// does not have. Re-deciding here with less information produces false
	// refusals — an ordinary delegated reply is sent by the DELEGATE into the
	// PARENT's conversation, so the pair legitimately mismatches the instance
	// owner. Trust the decision; verify that one was made.
	if msg.OwnershipChecked {
		return true
	}
	if cfg == nil {
		// No config to check against. Fail OPEN here, deliberately: the tool
		// layer already refused anything unowned, and dropping real user
		// traffic because dispatch briefly has no config would be a worse
		// failure than the one this guards. The WARN below never fires in
		// that case, which is itself the signal.
		return true
	}
	inst, ok := cfg.Channels[strings.ToLower(strings.TrimSpace(instanceID))]
	if !ok || !inst.IsWorkspaceBound() {
		return true // unowned: unbound instance, webchat, or unknown id
	}
	if inst.WorkspaceID == workspaceID && inst.Identity != nil && inst.Identity.ID == agentID {
		return true
	}

	outboundOwnershipRefusals.Add(1)
	owner := ""
	if inst.Identity != nil {
		owner = inst.Identity.ID
	}
	logger.WarnCF("channels", "refused an agent-originated send: the sender does not own this channel",
		map[string]any{
			"instance_id":      instanceID,
			"sender_agent":     agentID,
			"sender_workspace": workspaceID,
			"owner_agent":      owner,
			"owner_workspace":  inst.WorkspaceID,
			"note": "this message carried an agent identity but no ownership decision, so it " +
				"reached the bus without going through send_message (ADR-065 FR-1). That is the " +
				"regression FR-7 exists to catch: a second send path added later.",
		})
	return false
}

// configSnapshot returns the Manager's current config under the read lock.
//
// The dispatch goroutine MUST NOT read m.config directly — runWorker's doc
// comment says so explicitly, and config is swapped on reload, so an unguarded
// read there is a data race. Everything else in this file is pure given a
// config, so this is the only place that touches shared state.
func (m *Manager) configSnapshot() *config.Config {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

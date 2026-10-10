package channels

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ErrReturnRouteRefused marks an answer the final egress binding check
// refused (session-core C-REPLY, FR-027). Nothing was sent.
var ErrReturnRouteRefused = errors.New("channels: return route refused")

var returnRouteRefusals atomic.Int64

// ReturnRouteRefusals reports how many answers the final binding check has
// refused. Separate from transport failures: "the network ate it" and "the
// source instance was rebound or deleted" are different events.
func ReturnRouteRefusals() int64 { return returnRouteRefusals.Load() }

// checkReturnRoute is the final egress binding check for a message carrying a
// bus.ReturnRoute. It compares the instance's CURRENT binding with the owner
// captured when the request was admitted, so a rebind, an unbind or a delete
// between admission and send refuses the answer instead of delivering it
// through a connector the source owner no longer owns. Messages without a
// route are not in scope and pass.
//
// It deliberately ignores bus.OutboundMessage.OwnershipChecked and the
// author's own pair: neither can grant a route. cfg nil refuses (fail
// closed) — unlike allowAgentOriginatedSend, a missing config here must not
// become a guessed send.
func checkReturnRoute(cfg *config.Config, msg bus.OutboundMessage) error {
	route := msg.Return
	if route == nil {
		return nil
	}
	if strings.TrimSpace(route.RequestID) == "" || strings.TrimSpace(msg.ChatID) == "" {
		return fmt.Errorf("%w: the captured request or destination chat is missing", ErrReturnRouteRefused)
	}
	if cfg == nil {
		return fmt.Errorf("%w: channel configuration is unavailable", ErrReturnRouteRefused)
	}
	inst, ok := cfg.Channels[strings.ToLower(strings.TrimSpace(msg.Channel))]
	if !ok {
		return fmt.Errorf("%w: instance %q is no longer configured", ErrReturnRouteRefused, msg.Channel)
	}
	capturedBound := route.OwnerWorkspaceID != "" || route.OwnerAgentID != ""
	if !capturedBound {
		if inst.IsWorkspaceBound() {
			return fmt.Errorf("%w: instance %q was unbound when the request arrived and is now owned by another agent",
				ErrReturnRouteRefused, msg.Channel)
		}
		return nil
	}
	if !inst.IsWorkspaceBound() || inst.WorkspaceID != route.OwnerWorkspaceID ||
		inst.Identity == nil || inst.Identity.ID != route.OwnerAgentID {
		return fmt.Errorf("%w: instance %q is no longer owned by the agent the request arrived through",
			ErrReturnRouteRefused, msg.Channel)
	}
	return nil
}

// refuseReturnRoute runs checkReturnRoute and, on refusal, counts and logs it.
// It returns true when the message may proceed.
func (m *Manager) refuseReturnRoute(cfg *config.Config, msg bus.OutboundMessage, boundary string) bool {
	err := checkReturnRoute(cfg, msg)
	if err == nil {
		return true
	}
	returnRouteRefusals.Add(1)
	logger.ErrorCF("channels", "refused an answer: the return binding no longer holds; nothing was sent",
		map[string]any{
			"instance_id": msg.Channel,
			"request_id":  msg.Return.RequestID,
			"boundary":    boundary,
			"author":      msg.AgentID,
			"error":       err.Error(),
		})
	if obs := m.returnRefusalObserver.Load(); obs != nil {
		(*obs)(msg, err)
	}
	return false
}

// ReturnRefusalObserver is told about every refused answer so a caller can
// make the failure visible to the responding agent.
type ReturnRefusalObserver func(msg bus.OutboundMessage, err error)

// SetReturnRefusalObserver installs (or clears, with nil) the observer.
func (m *Manager) SetReturnRefusalObserver(o ReturnRefusalObserver) {
	if o == nil {
		m.returnRefusalObserver.Store(nil)
		return
	}
	m.returnRefusalObserver.Store(&o)
}

// liveConfig is the lock-free current config. Workers MUST use this, never
// configSnapshot: Reload holds m.mu for writing while it waits for workers to
// exit, so a worker taking the read lock would deadlock it.
func (m *Manager) liveConfig() *config.Config {
	if m == nil {
		return nil
	}
	return m.liveCfg.Load()
}

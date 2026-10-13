package agent

import (
	"strings"
	"sync"
	"time"
)

// RoutingDrop is one channel instance's routing degradation: the reason the
// last unroutable inbound message was dropped and when (omnipus-system-agent
// FR-121, K15).
type RoutingDrop struct {
	Reason string
	At     time.Time
}

// routingDropTracker holds the live routing degradation per channel
// instance. In-memory by design: a degradation describes traffic this
// process refused, and the next unroutable message after a restart records
// it again.
type routingDropTracker struct {
	mu    sync.Mutex
	drops map[string]RoutingDrop
}

// RecordRoutingDrop marks instanceID degraded with reason. An empty
// instanceID records nothing (there is no channel to show it on).
func (al *AgentLoop) RecordRoutingDrop(instanceID, reason string) {
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return
	}
	al.routingDrops.mu.Lock()
	defer al.routingDrops.mu.Unlock()
	if al.routingDrops.drops == nil {
		al.routingDrops.drops = make(map[string]RoutingDrop)
	}
	al.routingDrops.drops[instanceID] = RoutingDrop{Reason: reason, At: time.Now().UTC()}
}

// ClearRoutingDrop removes instanceID's routing degradation (FR-129: saving
// a valid binding clears it).
func (al *AgentLoop) ClearRoutingDrop(instanceID string) {
	al.routingDrops.mu.Lock()
	defer al.routingDrops.mu.Unlock()
	delete(al.routingDrops.drops, strings.TrimSpace(instanceID))
}

// RoutingDropFor returns instanceID's active routing degradation, if any.
func (al *AgentLoop) RoutingDropFor(instanceID string) (RoutingDrop, bool) {
	al.routingDrops.mu.Lock()
	defer al.routingDrops.mu.Unlock()
	drop, ok := al.routingDrops.drops[strings.TrimSpace(instanceID)]
	return drop, ok
}

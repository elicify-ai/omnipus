package channels

// Rejected-sender memory (channels-spec-r8, FR-060, FR-061; S-53..S-56).

import (
	"slices"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// rejectedSendersRecent is how many distinct rejected senders an adapter
// remembers (FR-060).
const rejectedSendersRecent = 20

// rejectedSenderMemory is one adapter instance's record of ignored senders.
// It lives on the adapter, so a rebuilt adapter starts empty (accepted).
type rejectedSenderMemory struct {
	mu     sync.Mutex
	count  int
	recent []string // canonical ids, most recent first, at most rejectedSendersRecent
}

// recordRejected notes one inbound message from a sender that is not on the
// allowed list (FR-060): behind a mutex it increments the counter; if the
// sender's canonical id is not among the last 20 recorded it logs one info
// line "channel ignored a sender not on the allowed list" (instance_id,
// sender, hint; never the text) and puts it first, otherwise it moves it
// first. sender is the canonical "platform:id". No reply is sent.
func (c *BaseChannel) recordRejected(sender string) {
	mem := &c.rejected
	mem.mu.Lock()
	mem.count++
	idx := slices.Index(mem.recent, sender)
	if idx >= 0 {
		mem.recent = slices.Delete(mem.recent, idx, idx+1)
	}
	mem.recent = slices.Insert(mem.recent, 0, sender)
	if len(mem.recent) > rejectedSendersRecent {
		mem.recent = mem.recent[:rejectedSendersRecent]
	}
	mem.mu.Unlock()
	if idx >= 0 {
		return // already recorded recently: moved first, no second log line
	}
	instanceID := c.instanceID
	if instanceID == "" {
		instanceID = c.name
	}
	logger.InfoCF("channels", "channel ignored a sender not on the allowed list", map[string]any{
		"instance_id": instanceID,
		"sender":      sender,
		"hint":        "add this id to allow_from to admit it",
	})
}

// RecordRejected is recordRejected for the adapter packages' own rejection
// branches (FR-060, S-54): adapters live in sub-packages and cannot reach the
// unexported method. sender is the canonical "platform:id".
func (c *BaseChannel) RecordRejected(sender string) {
	c.recordRejected(sender)
}

// RejectedSenders returns what recordRejected has recorded on this adapter
// instance (FR-061): the number of ignored messages and the canonical sender
// ids of the last 20 distinct senders, most recent first. GET /channels
// reports it as ChannelEntry.rejected_senders when count >= 1.
func (c *BaseChannel) RejectedSenders() (count int, recent []string) {
	mem := &c.rejected
	mem.mu.Lock()
	defer mem.mu.Unlock()
	return mem.count, slices.Clone(mem.recent)
}

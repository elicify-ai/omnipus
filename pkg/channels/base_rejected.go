package channels

// Rejected-sender memory WP-0 shells (channels-spec-r8, FR-060, FR-061).
// Signatures only; no adapter calls them yet.

// recordRejected notes one inbound message from a sender that is not on the
// allowed list (FR-060): behind a mutex it increments the counter; if the
// sender's canonical id is not among the last 20 recorded it logs one info
// line "channel ignored a sender not on the allowed list" (instance_id,
// sender, hint; never the text) and puts it first, otherwise it moves it
// first. sender is the canonical "platform:id". No reply is sent.
//
// WP-0 shell: does nothing.
//
//nolint:unused // WP-0 shell: the test author's tests and WP-1's adapter call sites are its users.
func (c *BaseChannel) recordRejected(sender string) {
}

// RejectedSenders returns what recordRejected has recorded on this adapter
// instance (FR-061): the number of ignored messages and the canonical sender
// ids of the last 20 distinct senders, most recent first. GET /channels
// reports it as ChannelEntry.rejected_senders when count >= 1. (The spec
// leaves this read accessor's signature open; this is the chosen one.)
//
// WP-0 shell: returns (0, nil).
func (c *BaseChannel) RejectedSenders() (count int, recent []string) {
	return 0, nil
}

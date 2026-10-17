package gateway

import "testing"

// The baseline contains the secret-card contracts but no request_secret
// producer. This cannot be turned into a fake card test: FR-169 requires a real
// decision raised by the room receiver's turn. A later QA RED round drives the
// WU-4 producer once its named public boundary is available.
func TestWU15Rooms_ReceiverSecretCardLivesOnlyInMainAndSetsWaitingOnUser(t *testing.T) {
	t.Fatal("BLOCKED: WU-4 request_secret turn producer not implemented on the required RED baseline — required by FR-169 / §6.11 / US-17 AS-7; secret card belongs to receiver main, room shows waiting_on_user with no secret action or value")
}

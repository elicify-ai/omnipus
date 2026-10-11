// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// BDD (delta review R1 #4): Given a delivered, still-unanswered request, When
// the reminder cannot be published (the bus is closed or full), Then the request
// is still owed its one reminder, so a later turn end retries - the sender must
// not wait forever because a single publish failed.
func TestRequestReminder_FailedPublishLeavesTheReminderOwed(t *testing.T) {
	al, _ := newSteerAL(t)
	const session, request = "sess-r1-4", "req-r1-4"
	al.pendingRequests.Store(request, session)
	al.markRequestDelivered(request)

	al.bus.Close() // every PublishInbound now fails
	al.remindUnansweredRequests(session)

	require.Equal(t, []string{request}, al.takeUnansweredRequests(session),
		"a reminder that was never published must still be owed")
}

// BDD (delta review R1 #9): the delivered / reminded sets must not outlive a
// request. A settled request (answered or discarded) leaves no entry behind, and
// an inbound that is not a pending request (every human message carries a
// transcript entry id) is never recorded at all.
func TestRequestReminder_SettledAndNonRequestIdsAreNotRetained(t *testing.T) {
	al, _ := newSteerAL(t)
	const session = "sess-r1-9"

	// Not a request: nothing may be stored.
	al.markRequestDelivered("human-message-1")
	require.Zero(t, syncMapLen(&al.deliveredRequests), "a non-request inbound must not be recorded")

	// A request that is delivered and reminded, then settled, leaves nothing.
	al.pendingRequests.Store("req-r1-9", session)
	al.markRequestDelivered("req-r1-9")
	require.Equal(t, []string{"req-r1-9"}, al.takeUnansweredRequests(session))
	require.Equal(t, 1, syncMapLen(&al.deliveredRequests))
	require.Equal(t, 1, syncMapLen(&al.remindedRequests))

	al.settleRequest("req-r1-9")
	require.Zero(t, syncMapLen(&al.pendingRequests))
	require.Zero(t, syncMapLen(&al.deliveredRequests), "delivered must be dropped when the request settles")
	require.Zero(t, syncMapLen(&al.remindedRequests), "reminded must be dropped when the request settles")
}

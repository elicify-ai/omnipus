// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package generated

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// S-152, TD-8, T-55. A stored group entry of 100,000 bytes (and anything up
// to the 5,242,880 read bound) must pass both generated validators. One byte
// past that read bound must not. The 65,536 write limit is not a read limit.

func TestGroupChat1449_StoredEntryOf100000BytesPassesBothValidators(t *testing.T) {
	mustPassBothCopies(t, "RoomEntry", groupChatStoredEntry(t, 100000))
	mustPassBothCopies(t, "RoomEntry", groupChatStoredEntry(t, 5242880))
	over := groupChatStoredEntry(t, 5242881)
	require.Error(t, validateAgainstAsyncAPISchema(t, "RoomEntry", over),
		"RoomEntry.text one byte past 5242880 must fail the asyncapi copy")
	require.Error(t, validateAgainstComponentSchema(t, "RoomEntry", over),
		"RoomEntry.text one byte past 5242880 must fail the component copy")
}

func groupChatStoredEntry(t *testing.T, n int) RoomEntry {
	t.Helper()
	return RoomEntry{
		Id:         "entry-stored",
		RoomId:     "room-stored",
		Seq:        1,
		Author:     RoomEntryAuthor{Kind: "user"},
		Text:       strings.Repeat("a", n),
		Addressees: []string{},
		AfterPause: false,
		Deliveries: []RoomDeliveryState{},
		Timestamp:  "2026-10-28T00:00:00Z",
	}
}

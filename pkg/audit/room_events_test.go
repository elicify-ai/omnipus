// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// FR-172: group-chat changes are audited under a registered event name, so
// they never trip the unknown-event warn-once path. The wire value is pinned
// independently of the constant.
func TestEventRoomChanged_IsRegistered(t *testing.T) {
	if audit.EventRoomChanged != "room.changed" {
		t.Fatalf("EventRoomChanged = %q, want the wire value room.changed", audit.EventRoomChanged)
	}
	if !audit.IsValidEventName(audit.EventName(audit.EventRoomChanged)) {
		t.Fatal("room.changed must be a valid audit event name")
	}
	// Control: the check can fail.
	if audit.IsValidEventName(audit.EventName("room.unregistered")) {
		t.Fatal("instrument: an unregistered name must not validate")
	}
}

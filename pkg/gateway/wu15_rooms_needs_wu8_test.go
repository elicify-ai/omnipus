package gateway

import "testing"

// These cases genuinely require WU-8's root-chain resolution and complete
// reconnect snapshots, which do not exist on the required 9b225b6d4 RED base.
// They are kept separate from room-owned receiver approvals (the executable
// HTTP/WS cases in wu15_rooms_approvals_test.go). No made-up internal API and
// no skipped tests: later QA RED supplies the real helper/root-chain stimulus.
func TestWU15Rooms_InteractiveReceiverHelperApprovalRootsInReceiverMain(t *testing.T) {
	t.Fatal("BLOCKED: WU-8 root-chain approval resolution for a helper below the room receiver's main not implemented — required by amendment R2 / FR-169 / FR-251; approval must ask inline in the receiver main, never the room")
}

func TestWU15Rooms_TaskSenderDoesNotMakeReceiverMainUnattended(t *testing.T) {
	t.Fatal("BLOCKED: WU-8 root-origin classification and task-sender room-origin stimulus not implemented — required by amendment R2 / FR-251; receiver main and its helpers remain interactive even when sender was a task run")
}

func TestWU15Rooms_ReceiverHelperApprovalReloadRestoresCompleteApprovableCard(t *testing.T) {
	t.Fatal("BLOCKED: WU-8 complete root-main approval reconnect snapshot not implemented — required by amendment §1.3 / R2 / FR-169; same tool_call_id, turn_id, args and root_session_id after reload, never a deny-only stub")
}

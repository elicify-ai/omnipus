package gateway

import "testing"

// Chief Q2 A (2026-10-14): these are intentionally loud placeholders, not
// behavioural sensitivity proof. The public contracts exist, but no named
// crash-cut observer exists on this RED baseline. A later QA RED round (never
// the implementation author) replaces these once a public seam is handed back.
// Do not skip, reflect on private symbols, or invent a room implementation API.
func TestWU15Rooms_CrashAfterDeliveryIDBeforeReceiverCapture(t *testing.T) {
	t.Fatal("BLOCKED: public crash cut after durable delivery id, before receiver capture not implemented — required by BDD-17.32 / FR-216")
}

func TestWU15Rooms_CrashAfterCommittedReceiverAppendBeforeRoomAcknowledgement(t *testing.T) {
	t.Fatal("BLOCKED: public crash cut after committed receiver capture/append, before room acknowledgement not implemented — required by BDD-17.32 / FR-216; Retry must append and wake nothing for the committed receiver")
}

func TestWU15Rooms_CrashAfterRoomAnswerBeforeMainMirror(t *testing.T) {
	t.Fatal("BLOCKED: public crash cut after room answer append, before main mirror not implemented — required by BDD-17.32 / FR-216; deterministic answer/mirror ids must prevent duplicate room answer")
}

func TestWU15Rooms_CrashAfterMainMirrorBeforeMentionAdmission(t *testing.T) {
	t.Fatal("BLOCKED: public crash cut after main mirror, before mention admission not implemented — required by BDD-17.32 / FR-216; exactly one mention delivery after Retry")
}

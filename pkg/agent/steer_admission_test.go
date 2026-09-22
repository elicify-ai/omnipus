package agent

import "testing"

func TestSteerAdmission_PromotedEntryKeepsItsReservation(t *testing.T) {
	gate := newSteerAdmission(func() int { return 1 })
	if admitted, _ := gate.tryAdmit("running", 1); !admitted {
		t.Fatal("first turn was not admitted")
	}
	if admitted, position := gate.tryAdmit("queued", 1); admitted || position != 1 {
		t.Fatalf("second turn = admitted %v, position %d; want queued at 1", admitted, position)
	}

	next, ok := gate.release("running", 1)
	if !ok || next.sessionID != "queued" || next.generation != 1 {
		t.Fatalf("release promotion = (%+v, %v), want queued generation 1", next, ok)
	}
	if !gate.hasReservation("queued", 1) {
		t.Fatal("promoted entry lost its admission reservation")
	}
	if gate.queueLen() != 0 {
		t.Fatalf("queue length after promotion = %d, want 0", gate.queueLen())
	}
}

func TestSteerAdmission_StaleGenerationCannotReleaseRevivedTurn(t *testing.T) {
	gate := newSteerAdmission(func() int { return 1 })
	if admitted, _ := gate.tryAdmit("revived", 2); !admitted {
		t.Fatal("revived generation was not admitted")
	}

	if _, promoted := gate.release("revived", 1); promoted {
		t.Fatal("stale generation unexpectedly promoted a queued turn")
	}
	if !gate.hasReservation("revived", 2) {
		t.Fatal("stale generation released the revived generation's slot")
	}
}

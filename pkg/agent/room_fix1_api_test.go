package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// WU-15 fix round 1, agent side: the tri-state room fence (S1) and the
// settlement of room requests a failed turn was handed (ruling A).

// S1 (FR-217): when the room cannot decide (a read or write failure), the
// request is neither run nor discarded; it stays undelivered and queued.
func TestFix1_DeferredFenceNeitherRunsNorDiscards(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fenceRuntime{fakeRoomRuntime: fakeRoomRuntime{allow: true}, decision: RoomConsumeDefer}
	f.al.SetRoomRuntime(rt)
	if _, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission()); err != nil {
		t.Fatalf("admission: %v", err)
	}
	wake, _ := addrDrainInbound(t, f.bus)
	if _, proceed := f.al.gateRoomRequest(wake); proceed {
		t.Fatal("S1: a request the room could not confirm was run")
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	c, _, err := f.al.RequestLedger().Get(mainID, roomTestDelivery)
	if err != nil || c.Discarded || c.Delivered {
		t.Fatalf("S1: capture discarded=%v delivered=%v err=%v; want kept and undelivered", c.Discarded, c.Delivered, err)
	}
	rt.decision = RoomConsumeCancel
	if _, proceed := f.al.gateRoomRequest(wake); proceed {
		t.Fatal("control: a cancelled request is not run")
	}
	if c, _, _ = f.al.RequestLedger().Get(mainID, roomTestDelivery); !c.Discarded {
		t.Fatal("control: a cancelled request is discarded")
	}
}

type fenceRuntime struct {
	fakeRoomRuntime
	decision RoomConsumeDecision
	released []string
}

func (f *fenceRuntime) DecideRoomConsume(_, requestID, _ string) RoomConsumeDecision {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.begun = append(f.begun, requestID)
	return f.decision
}

func (f *fenceRuntime) RoomConsumeReleased(_, requestID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, requestID)
	return nil
}

// Ruling A (§6.11): a turn that fails fails every room request it was handed
// besides its starting message, so no member is left "working".
func TestFix1_FailedTurnSettlesItsJoinedRoomRequests(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fakeRoomRuntime{allow: true}
	f.al.SetRoomRuntime(rt)
	f.al.roomRequests.Store("joined-1", "room-1")
	m := &roomCostMeter{al: f.al, inputs: 1, rooms: map[string]int{}}
	m.addInputs([]string{roomSteerCorrelation("joined-1"), "web-1"}, 2)
	if err := f.al.settleRoomInputs(m, context.Canceled); err != nil {
		t.Fatal(err)
	}
	if len(rt.ended) != 1 || rt.ended[0] != "joined-1" {
		t.Fatalf("the failed turn settled %v; want the joined room request", rt.ended)
	}
	rt.ended = nil
	if err := f.al.settleRoomInputs(m, nil); err != nil {
		t.Fatal(err)
	}
	if len(rt.ended) != 0 {
		t.Fatal("control: a turn without an error leaves the request to its answer and the reminder")
	}
}

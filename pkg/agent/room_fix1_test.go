package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// WU-15 fix round 1, agent side. Expected values come from the spec (§6.11
// "held means only: the agent is stopped", FR-214, FR-216, FR-217) and the
// 2026-10-15 ruling A (a waiting room request is an input at the START of
// the turn that revives its member).

// fillInbound occupies every inbound slot so the next publish blocks.
func fillInbound(t *testing.T, b *bus.MessageBus) int {
	t.Helper()
	n := 0
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := b.PublishInbound(ctx, bus.InboundMessage{Channel: "filler", Content: "x"})
		cancel()
		if err != nil {
			return n
		}
		n++
	}
}

func drainAll(b *bus.MessageBus) {
	for {
		select {
		case <-b.InboundChan():
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

// Architect #3 / SF#4: a wake that could not be published is not "held" (the
// member is not stopped); the request stays queued, and a Retry wakes it.
func TestFix1_FailedWakeIsQueuedNotHeldAndRetryWakes(t *testing.T) {
	f := newAddrFixture(t)
	filled := fillInbound(t, f.bus)
	if filled == 0 {
		t.Fatal("instrument: the bus never filled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	res, err := f.al.AdmitRoomRequest(ctx, roomAdmission())
	cancel()
	if err != nil {
		t.Fatalf("a committed request whose wake failed returned %v", err)
	}
	if res.Held {
		t.Fatal("architect #3: a failed wake was reported as held (the member is not stopped)")
	}
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	c, found, gErr := f.al.RequestLedger().Get(mainID, roomTestDelivery)
	if gErr != nil || !found || c.Held || c.Delivered {
		t.Fatalf("capture = %+v found=%v err=%v; want recorded, not held, not delivered", c, found, gErr)
	}
	drainAll(f.bus)
	retry, err := f.al.AdmitRoomRequest(context.Background(), roomAdmission())
	if err != nil || !retry.AlreadyAdmitted || retry.Held {
		t.Fatalf("retry = %+v err=%v; want already admitted, not held", retry, err)
	}
	wake, ok := addrDrainInbound(t, f.bus)
	if !ok || wake.TranscriptEntryID != roomTestDelivery || !isNoJoinRoomWake(wake) {
		t.Fatalf("SF#4: the Retry did not wake the queued request (got %v, %+v)", ok, wake.Metadata)
	}
	entries, _ := f.al.GetSessionStore().ReadTranscript(mainID)
	if len(entries) != 1 {
		t.Fatalf("the retry wrote the request again: %d entries", len(entries))
	}
}

// Ruling A (FR-214, BDD-17.30): a room request that waited is handed to the
// turn being admitted at that turn's start - it is in the turn's steering
// queue when deliverHeldRequests returns, not published to arrive later.
func TestFix1_HeldRoomRequestIsATurnStartInput(t *testing.T) {
	f := newAddrFixture(t)
	rt := &fakeRoomRuntime{allow: true}
	f.al.SetRoomRuntime(rt)
	mainID, _ := session.MainSessionID(addrWS, addrReceiver)
	capture := addressing.Capture{
		RequestID: roomTestDelivery, ReceiverSessionID: mainID,
		Receiver:   addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver},
		Sender:     addressing.Sender{Principal: "user"},
		Source:     addressing.Source{Kind: addressing.SourceRoom, RoomID: "room-1", RoomName: "Launch"},
		AdmittedAt: time.Now().UTC(), Text: "Request d-0001 from the user in the group chat \"Launch\".\n\nstatus?", Held: true,
	}
	if _, err := f.al.GetSessionStore().GetOrCreateMainSession(addrWS, addrReceiver); err != nil {
		t.Fatal(err)
	}
	if err := f.al.RequestLedger().Put(capture); err != nil {
		t.Fatal(err)
	}
	f.al.deliverHeldRequests(mainID)
	msg := bus.InboundMessage{Channel: "webchat", ChatID: "request:" + roomTestDelivery, SessionID: mainID,
		TranscriptEntryID: roomTestDelivery, Content: capture.Text,
		Metadata: map[string]string{"agent_id": addrReceiver, "workspace_id": addrWS, ownerWakeMetadataKey: "1", roomIDMetadataKey: "room-1"}}
	route, _, err := f.al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatal(err)
	}
	msgs, ids := f.al.dequeueSteeringMessagesForScope(route.SessionKey)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "status?") || len(ids) != 1 || ids[0] != roomSteerCorrelation(roomTestDelivery) {
		t.Fatalf("ruling A: the waiting room request is not a turn-start input (steering %d msgs, ids %v)", len(msgs), ids)
	}
	if _, published := addrDrainInbound(t, f.bus); published {
		t.Fatal("ruling A: the waiting room request was published to arrive later instead of starting with the turn")
	}
	if c, _, _ := f.al.RequestLedger().Get(mainID, roomTestDelivery); !c.Delivered {
		t.Fatal("the handed-over request is not marked delivered")
	}
}

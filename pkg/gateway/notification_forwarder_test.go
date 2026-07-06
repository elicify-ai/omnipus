//go:build !cgo

// Notification WS-forwarder tests (#264): EventKindNotification → notification
// frame, delivered ONLY to the connection whose userID matches the payload
// Recipient (per-user filtering), with the admin-broadcast sentinel fanning out
// to every connection (unconditional broadcast — single-user product, no
// role/ownership gate).

package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

// notificationFrameDecoder decodes the fields the test asserts on.
type notificationFrameDecoder struct {
	Type             string  `json:"type"`
	ID               string  `json:"id"`
	NotificationType string  `json:"notification_type"`
	Title            string  `json:"title"`
	Severity         string  `json:"severity"`
	ScheduleID       *string `json:"schedule_id"`
}

func emitNotification(b *agent.EventBus, p agent.NotificationPayload) {
	b.Emit(agent.Event{Kind: agent.EventKindNotification, Payload: p})
}

func samplePayload(recipient string) agent.NotificationPayload {
	sid := "sched-1"
	return agent.NotificationPayload{
		Recipient:        recipient,
		ID:               "ntf-1",
		NotificationType: "schedule_failed",
		Title:            "Schedule failed",
		Severity:         "error",
		CreatedAtMs:      123,
		ScheduleID:       sid,
	}
}

// TestNotification_DeliveredToMatchingUser asserts only the matching userID
// connection receives the frame.
func TestNotification_DeliveredToMatchingUser(t *testing.T) {
	b := agent.NewEventBus()
	defer b.Close()
	h := makeMinimalHandler()

	wc, ch := makeForwarderTestConn(64)
	wc.userID = "alice"
	done := runForwarder(h, wc, "chat", b)

	emitNotification(b, samplePayload("alice"))
	b.Close()
	<-done

	select {
	case data := <-ch:
		var f notificationFrameDecoder
		require.NoError(t, json.Unmarshal(data, &f))
		assert.Equal(t, "notification", f.Type)
		assert.Equal(t, "ntf-1", f.ID)
		assert.Equal(t, "schedule_failed", f.NotificationType)
		require.NotNil(t, f.ScheduleID)
		assert.Equal(t, "sched-1", *f.ScheduleID)
	case <-time.After(2 * time.Second):
		t.Fatal("matching user did not receive notification frame")
	}
}

// TestNotification_NotDeliveredToOtherUser asserts a non-matching userID gets
// nothing.
func TestNotification_NotDeliveredToOtherUser(t *testing.T) {
	b := agent.NewEventBus()
	defer b.Close()
	h := makeMinimalHandler()

	wc, ch := makeForwarderTestConn(64)
	wc.userID = "bob"
	done := runForwarder(h, wc, "chat", b)

	emitNotification(b, samplePayload("alice")) // for alice, not bob
	b.Close()
	<-done

	select {
	case data := <-ch:
		t.Fatalf("non-matching user received a notification frame: %s", data)
	default:
	}
}

// TestNotification_AdminBroadcastReachesAllConnections asserts the
// admin-broadcast sentinel is delivered to any connected WS connection.
// Broadcast delivery is unconditional now (single-user product — the
// role/ownership gate that used to restrict it to admin-role connections has
// been removed).
func TestNotification_AdminBroadcastReachesAllConnections(t *testing.T) {
	b := agent.NewEventBus()
	defer b.Close()
	h := makeMinimalHandler()

	conn, ch := makeForwarderTestConn(64)
	conn.userID = "root"
	done := runForwarder(h, conn, "chat", b)

	emitNotification(b, samplePayload(agent.NotificationAdminBroadcast))
	b.Close()
	<-done

	select {
	case <-ch:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("connection did not receive broadcast notification")
	}
}

package gateway

import (
	"testing"
	"time"
)

// BS-6.16 / D-presence: a watching claim lasts 45 seconds without a refresh.
// Dropping the connection clears it immediately.
func TestChatPresence_ExpiresAt45Seconds(t *testing.T) {
	start := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	at := start
	store := newChatPresenceStore(func() time.Time { return at })
	conn := &wsConn{}
	store.set("sess", conn, true)
	if !store.watching("sess") {
		t.Fatal("a fresh claim must be watching")
	}
	at = start.Add(44 * time.Second)
	if !store.watching("sess") {
		t.Fatal("44s is inside the 45s window")
	}
	at = start.Add(45 * time.Second)
	if store.watching("sess") {
		t.Fatal("45s without a refresh must expire the claim")
	}
	at = start
	store.set("sess", conn, true)
	store.dropConn(conn)
	if store.watching("sess") {
		t.Fatal("a dropped connection must not stay watching")
	}
	store.set("sess", conn, true)
	store.set("sess", conn, false)
	if store.watching("sess") {
		t.Fatal("watching=false must clear the claim")
	}
}

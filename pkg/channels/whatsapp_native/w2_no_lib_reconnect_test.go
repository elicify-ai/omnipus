//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// Founder decision 2026-11-07 (issue #1687, W2 A): whatsmeow's own auto-reconnect
// is switched off; Omnipus's reconnect (reconnectWithBackoff in this package)
// stays the single owner of reconnecting. The oracles come from that decision,
// not from the code. Two facts about the dependency make them real requirements
// rather than restated defaults (read from whatsmeow
// v0.0.0-20260713112832-d8960d9575d2): NewClient ships EnableAutoReconnect: true,
// and a remote drop makes the library dispatch Disconnected AND go
// cli.autoReconnect(ctx) on the ConnectContext context, first delay
// AutoReconnectErrors*2s == 0 (client.go onDisconnect / autoReconnect; the
// reconnect is gated on EnableAutoReconnect && Store.ID != nil).
//
// Unlike the earlier train fixtures (fix1_r7_late_connect_test.go,
// final_r8_cancel_deadline_test.go), which set c.EnableAutoReconnect = false
// inside the test hook to keep the library out of their pictures, these tests
// set nothing: whether the library reconnects on its own is exactly what the
// production code must decide at its single whatsmeow.NewClient call site
// (whatsapp_native.go::Start — the only one in the package).
//
// The founder decision's third clause (a stop never races a library dial) is
// deliberately NOT covered here: see ta-report — on this branch the clause is
// not deterministically RED (cancel-first stop plus the library's 0-delay dial
// mask it); the clause belongs to PR #1681's pack.

// w2SeedLinkedStore builds the same store directory Start opens, holding one
// linked device, so Start takes the linked branch (client.Store.ID != nil —
// whatsmeow's autoReconnect returns early for an unpaired client, so a QR
// fixture could never exercise the behaviour clause).
func w2SeedLinkedStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open(sqliteDriver, "file:"+filepath.Join(dir, whatsappDBName)+"?_foreign_keys=on")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(context.Background(), "PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	container := sqlstore.NewWithDB(db, sqliteDriver, waLog.Noop)
	require.NoError(t, container.Upgrade(context.Background()))
	device := container.NewDevice()
	device.ID = &types.JID{User: "15550001111", Device: 1, Server: types.DefaultUserServer}
	device.Account = &waAdv.ADVSignedDeviceIdentity{
		Details: []byte{1}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64),
	}
	require.NoError(t, container.PutDevice(context.Background(), device))
	require.NoError(t, container.Close())
	return dir
}

// w2StartLinkedAgainstFakeServer starts the real channel's linked branch against
// the repo's fake WhatsApp endpoint (real WebSocket + authenticated Noise XX
// exchange) and returns the fixture, the endpoint, the exact client Start
// created, and the remote end of its authenticated socket. The test hook points
// the client at the endpoint and captures it; it deliberately sets nothing about
// auto-reconnect.
func w2StartLinkedAgainstFakeServer(t *testing.T) (*nativeFixture, *fix1NoiseServer, *whatsmeow.Client, interface{ CloseNow() error }) {
	t.Helper()
	server := fix1NewNoiseServer(t)
	f := newNativeFixture(t, w2SeedLinkedStore(t))
	var captured *whatsmeow.Client
	previous := nativeClientHook
	nativeClientHook = func(c *whatsmeow.Client) {
		captured = c
		c.MessengerConfig = &whatsmeow.MessengerConfig{BaseURL: server.server.URL, WebsocketURL: "ws" + strings.TrimPrefix(server.server.URL, "http")}
	}
	t.Cleanup(func() { nativeClientHook = previous })
	require.NoError(t, f.ch.Start(channels.WithPairingStart(context.Background(), "", true)),
		"instrument: the linked start connects through the fake endpoint")
	require.NotNil(t, captured, "instrument: the channel created its client at its single whatsmeow.NewClient call site")
	require.True(t, captured.IsConnected(), "instrument: the initial real Noise handshake succeeded")
	var remote interface{ CloseNow() error }
	select {
	case ws := <-server.connected:
		remote = ws
	case err := <-server.errors:
		t.Fatalf("HARNESS: real initial Noise handshake failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: initial authenticated connection was not acknowledged")
	}
	return f, server, captured, remote
}

// TestNoLibAutoReconnect_EveryClientTheChannelCreatesHasTheLibraryReconnectOff
// is the founder decision's configuration clause (issue #1687): the library's
// auto-reconnect is off for EVERY client the channel creates. The package has
// exactly one whatsmeow.NewClient call site (whatsapp_native.go::Start), so
// asserting the client the channel holds during a real start covers "every".
// The assertion is on that exact client — the same instance the channel stores
// under its mutex and its reconnect path dials — never on a client the test
// built.
func TestNoLibAutoReconnect_EveryClientTheChannelCreatesHasTheLibraryReconnectOff(t *testing.T) {
	f, _, captured, _ := w2StartLinkedAgainstFakeServer(t)
	f.ch.mu.Lock()
	held := f.ch.client
	f.ch.mu.Unlock()
	require.Same(t, captured, held, "the channel holds exactly the client its single creation point built")
	require.False(t, held.EnableAutoReconnect,
		"founder decision W2 A (issue #1687): every client the channel creates must have whatsmeow's EnableAutoReconnect off; the library must not own a second reconnect beside ours")
}

// TestNoLibAutoReconnect_AfterARemoteDropExactlyOneReconnectDialHappensAndItIsOurs
// is the founder decision's behaviour clause (issue #1687): a remote drop leads
// to exactly ONE reconnect and it is ours. The oracle is the fake endpoint's
// completed-Noise-handshake count: the initial connect plus exactly one dial,
// and that dial may only happen after our reconnectWithBackoff — held at its own
// log stage by the external clock seam (no native method replaced) — is
// released, so anything that dials while ours is still held is by construction
// not ours. With the library's auto-reconnect still on, whatsmeow schedules its
// own dial on the remote drop (first delay 0s), which lands while ours is held:
// the extra handshake is the defect this test pins.
//
// Bounds: absence of a library dial cannot be a pure counter, because the
// library's first own dial is 0-delay (whatsmeow client.go::autoReconnect) and
// only "it did not happen" needs a wait. That wait is the fixtures' standard
// 5s (fix1_r7_late_connect_test.go). It does not decide a pass: on the current
// code the library handshake lands in that window and the assertion below
// fails on the count. The post-release wait only gives OUR dial its chance;
// the following quiet window fails if anything dials again.
func TestNoLibAutoReconnect_AfterARemoteDropExactlyOneReconnectDialHappensAndItIsOurs(t *testing.T) {
	_, server, _, remote := w2StartLinkedAgainstFakeServer(t)
	require.Equal(t, int32(1), server.handshakes.Load(), "instrument: exactly the initial dial so far")
	// Hold OUR reconnect before it can dial, then drop the socket remotely.
	held, release := fix1HoldCapturedReconnect(t)
	require.NoError(t, remote.CloseNow(), "instrument: the remote drop reached the socket")
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: our reconnectWithBackoff did not reach its held stage after the remote drop")
	}
	// While ours is held, anything that dials is not ours. The library's first
	// own dial is 0-delay, so with the defect present this receipt lands almost
	// immediately. The 5s bound only ends the hold when the library stays silent.
	dialledWhileOursHeld := false
	select {
	case <-server.connected:
		dialledWhileOursHeld = true
	case <-time.After(5 * time.Second):
	}
	require.False(t, dialledWhileOursHeld,
		"founder decision W2 A (issue #1687): while Omnipus's reconnectWithBackoff is held, the WhatsApp library must not complete a handshake of its own")
	require.Equal(t, int32(1), server.handshakes.Load(),
		"founder decision W2 A (issue #1687): handshake count must still be the initial connect while our reconnect is held — a second handshake here is the library dialling beside reconnectWithBackoff")
	release() // only now may our reconnect dial
	select {
	case <-server.connected:
	case <-time.After(5 * time.Second):
		t.Fatal("our reconnectWithBackoff did not dial after it was released")
	}
	// Quiet window: nothing dials again (exactly one, and it was the one just released).
	select {
	case <-server.connected:
		t.Fatalf("a further reconnect dial completed after ours; exactly one is required (handshakes=%d)", server.handshakes.Load())
	case <-time.After(5 * time.Second):
	}
	require.Equal(t, int32(2), server.handshakes.Load(),
		"a remote drop must produce exactly one reconnect dial, and it must be the one reconnectWithBackoff made after it was released")
}

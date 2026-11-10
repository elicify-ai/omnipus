//go:build !mipsle && !netbsd && !(freebsd && arm)

package whatsapp

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// These detectors drive the REAL native channel's pairing lifecycle (gate r4
// pr-test R4-T2): Start's branches, the QR loop's per-attempt stamping, the
// attempt's own Stop handle and interruption. Only the platform is kept out:
// the client's transport points at a closed local port (nativeClientHook),
// and QR events are fed into the loop's own handler.

type nativeFrame struct {
	attemptID   string
	status      channels.PairingStatus
	qr, message string
}

type nativeFixture struct {
	ch     *WhatsAppNativeChannel
	store  *channels.PairingAttempts
	mu     sync.Mutex
	frames []nativeFrame
}

func newNativeFixture(t *testing.T, storeDir string) *nativeFixture {
	t.Helper()
	ch, err := NewWhatsAppNativeChannel(config.WhatsAppConfig{Enabled: true}, bus.NewMessageBus(), storeDir)
	require.NoError(t, err)
	native, ok := ch.(*WhatsAppNativeChannel)
	require.True(t, ok)
	f := &nativeFixture{ch: native, store: channels.NewPairingAttempts()}
	native.SetPairingAttempts(f.store, "whatsapp")
	native.SetPairingAttemptObserver(func(attemptID string, status channels.PairingStatus, qr, message string) {
		f.mu.Lock()
		f.frames = append(f.frames, nativeFrame{attemptID, status, qr, message})
		f.mu.Unlock()
	})
	t.Cleanup(func() { _ = native.Stop(context.Background()) })
	return f
}

func (f *nativeFixture) recorded() []nativeFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]nativeFrame(nil), f.frames...)
}

func (f *nativeFixture) admitReady(t *testing.T, kind channels.PairingAdmission, owner *channels.PairingOwner) string {
	t.Helper()
	admitted, err := f.store.Admit("whatsapp", kind, owner)
	require.NoError(t, err)
	require.True(t, f.store.MarkReady("whatsapp", admitted.AttemptID, "rev"))
	return admitted.AttemptID
}

// offlineClients points every client a Start builds at a closed local port.
func offlineClients(t *testing.T) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	proxy, err := url.Parse("http://" + addr)
	require.NoError(t, err)
	nativeClientHook = func(c *whatsmeow.Client) { c.SetProxy(http.ProxyURL(proxy)) }
	t.Cleanup(func() { nativeClientHook = nil })
}

var ownerA = &channels.PairingOwner{SessionID: "chat-a", RequestID: "req-a"}

// TestStart_WithoutATokenNoPairingRuns is gate r4 R4-A01 at the adapter: a
// non-boot start of an unpaired connector without its own admitted token
// (a revoked, stale or missing admission) starts no pairing loop and mints no
// attempt — never a fallback unowned QR.
func TestStart_WithoutATokenNoPairingRuns(t *testing.T) {
	f := newNativeFixture(t, t.TempDir())
	require.NoError(t, f.ch.Start(channels.WithPairingStart(context.Background(), "", false)))
	require.False(t, f.ch.IsRunning(), "nothing runs")
	require.Empty(t, f.recorded(), "no code, no status")
	_, ok := f.store.Latest("whatsapp")
	require.False(t, ok, "no attempt was minted")
}

// TestStart_TokenStartFailureEndsItsAttemptAndSaysWhy is gate r4 T2-SF-16: the
// admitted start activates its own attempt, then the platform cannot be
// reached; the attempt ends start_failed and its chat gets the value-free
// cause — not a fake QR expiry — and may Retry.
func TestStart_TokenStartFailureEndsItsAttemptAndSaysWhy(t *testing.T) {
	offlineClients(t)
	f := newNativeFixture(t, t.TempDir())
	token := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	err := f.ch.Start(channels.WithPairingStart(context.Background(), token, false))
	require.Error(t, err, "instrument: the platform is unreachable")
	got, ok := f.store.Attempt("whatsapp", token)
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeStartFailed, got.Outcome)
	frames := f.recorded()
	require.Len(t, frames, 1)
	require.Equal(t, token, frames[0].attemptID)
	require.Equal(t, channels.PairingStatusError, frames[0].status)
	require.Equal(t, channels.PairingStartFailureMessage(err), frames[0].message)
	require.NotContains(t, frames[0].message, "127.0.0.1", "no raw platform text")
	_, err = f.store.Admit("whatsapp", channels.PairingAdmissionRetry, ownerA)
	require.NoError(t, err, "its chat may Retry")
}

// TestStart_AlreadyLinkedDeviceNeverShowsThePairingFlow is the founder rule's
// positive control (gate r4 R4-A05 / R4-T2): a linked device's boot start
// takes the connect branch — no restart interruption, no QR, no attempt — and
// an admitted start of a linked device leaves its admission to the connect
// result (closed only once connected).
func TestStart_AlreadyLinkedDeviceNeverShowsThePairingFlow(t *testing.T) {
	offlineClients(t)
	dir := t.TempDir()
	// The same store the channel opens (Start), with one linked device in it.
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

	f := newNativeFixture(t, dir)
	err = f.ch.Start(channels.WithPairingStart(context.Background(), "", true))
	require.Error(t, err, "the linked branch tried to connect (the platform is unreachable here)")
	require.Contains(t, err.Error(), "connect")
	require.Empty(t, f.recorded(), "no interruption message, no QR")
	_, ok := f.store.Latest("whatsapp")
	require.False(t, ok, "no restart-interrupted attempt for a linked device")

	token := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	require.Error(t, f.ch.Start(channels.WithPairingStart(context.Background(), token, false)))
	require.Empty(t, f.recorded(), "a linked device is never offered a QR")
	require.True(t, f.store.IsReady("whatsapp", token), "the admission waits for the manager's start disposition")
}

// TestQRLoop_AnOldLoopsLateEventsNeverTouchItsSuccessor is gate r4 R4-T2 / M8:
// every QR event is stamped with its own loop's attempt id. After attempt A
// ended and the same chat's Retry B started, A's late code is still A's and
// A's late terminal event cannot end B.
func TestQRLoop_AnOldLoopsLateEventsNeverTouchItsSuccessor(t *testing.T) {
	f := newNativeFixture(t, t.TempDir())
	a := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	_, err := f.ch.beginAttempt(a)
	require.NoError(t, err)
	f.ch.handleQREvent(a, whatsmeow.QRChannelItem{Event: "code", Code: "QR-A"}, false)
	f.ch.handleQREvent(a, whatsmeow.QRChannelItem{Event: "timeout"}, false)

	b := f.admitReady(t, channels.PairingAdmissionRetry, ownerA)
	_, err = f.ch.beginAttempt(b)
	require.NoError(t, err)
	f.ch.handleQREvent(a, whatsmeow.QRChannelItem{Event: "code", Code: "QR-A-LATE"}, false)
	f.ch.handleQREvent(a, whatsmeow.QRChannelItem{Event: "timeout"}, false)
	f.ch.handleQREvent(b, whatsmeow.QRChannelItem{Event: "code", Code: "QR-B"}, false)

	require.Equal(t, []nativeFrame{
		{a, channels.PairingStatusCode, "QR-A", ""},
		{a, channels.PairingStatusTimeout, "", "the QR code expired before it was scanned"},
		{a, channels.PairingStatusCode, "QR-A-LATE", ""},
		{b, channels.PairingStatusCode, "QR-B", ""},
	}, f.recorded(), "A's late code keeps A's id; A's late timeout is not published")
	require.True(t, f.store.IsCurrent("whatsapp", b), "B keeps running")
}

// TestStopHandle_EndsOnlyItsOwnLoop is gate r4 R4-T2 / M6–M7 on the real
// adapter: an attempt's own Stop handle tears the channel down only while that
// attempt runs; the old handle of a replaced attempt is a no-op; and a
// disable/reload stop of a running pairing ends it interrupted, visibly.
func TestStopHandle_EndsOnlyItsOwnLoop(t *testing.T) {
	f := newNativeFixture(t, t.TempDir())
	a := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	_, err := f.ch.beginAttempt(a)
	require.NoError(t, err)
	selected := f.store.OwnedBy("chat-a")
	require.Len(t, selected, 1)
	stopA, ok := f.store.BeginStop(selected[0])
	require.True(t, ok)
	require.NoError(t, stopA.Cancel(context.Background()), "A's handle tears A down")
	done, ok := f.store.FinishStop("whatsapp", a, nil)
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeStopped, done.Outcome)

	b := f.admitReady(t, channels.PairingAdmissionRetry, ownerA)
	_, err = f.ch.beginAttempt(b)
	require.NoError(t, err)
	require.NoError(t, stopA.Cancel(context.Background()), "A's old handle again")
	require.True(t, f.store.IsCurrent("whatsapp", b), "the old handle did not touch B")

	require.NoError(t, f.ch.Stop(context.Background()), "a disable or reload stops the channel")
	got, ok := f.store.Attempt("whatsapp", b)
	require.True(t, ok)
	require.Equal(t, channels.PairingOutcomeInterrupted, got.Outcome)
	frames := f.recorded()
	require.Equal(t, nativeFrame{b, channels.PairingStatusError, "",
		"This pairing was interrupted because the WhatsApp connection was changed or restarted. Press Retry to get a new code."},
		frames[len(frames)-1], "the interruption is visible, never a silent dead code")
	_, err = f.store.Admit("whatsapp", channels.PairingAdmissionRetry, ownerA)
	require.NoError(t, err, "its chat may Retry")
}
